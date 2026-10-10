// Package pebble is a scanner.Scanner that sorts codes on disk with Pebble,
// CockroachDB's key-value store, so its memory use is set by its Budget
// rather than by the size of the input.
//
// Each line becomes a key of its packed code followed by its source index,
// with an empty value. Pebble keeps keys sorted, so every occurrence of a code
// ends up next to the others, and a code repeated within one source is a
// single key:
//
//	source 1: HAPPYHRS, SUPER100, SUPER100     keys (code ‖ source)    sources per code
//	source 2: HAPPYHRS                    ──▶  HAPPYHRS ‖ 1  ┐
//	source 3: FIFTYOFF                         HAPPYHRS ‖ 2  ┘  2 ✓
//	                                           FIFTYOFF ‖ 3     1
//	                                           SUPER100 ‖ 1     1
//
// Keys are loaded by ingesting runs that are already sorted, rather than
// written one at a time, then scanned in key ranges:
//
//	                      load                                          scan, per key range
//	source 0 ─┐                           ┌─ sort ─ run 1.sst ─┐
//	source 1 ─┼─ chunks ─ loaders ────────┼─ sort ─ run 2.sst ─┼─ ingest ─▶ L0 ─▶ workers walk the keys
//	source 2 ─┘   (code ‖ source) runs    └─ …                 ┘                 in order, keep codes in
//	                                                                             ≥ MinFiles sources
//
// Pebble's own write path, batches into memtables, measured far slower here:
// its memtables cost about 40 bytes per key, so within a few hundred MiB it
// flushed over 1,800 small files that took 19 minutes to scan, where
// ingesting runs took about a minute.
package pebble

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	pebbledb "github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/objstorage/objstorageprovider"
	"github.com/cockroachdb/pebble/v2/sstable"
	"github.com/cockroachdb/pebble/v2/vfs"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/services/coupons/codec"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner"
)

// Name is how COUPONS_SCANNER and the build rules refer to this scanner.
const Name = "pebble"

// MaxSources is the most sources a scan accepts: source indexes are 2 bytes
// of each key.
const MaxSources = math.MaxUint16

// checkEvery is how many keys a scan worker walks between checks for
// cancellation.
const checkEvery = 64 << 10

type Options struct {
	// Dir holds the database during a build; it is deleted afterwards.
	Dir string

	Codec    codec.Codec
	MinFiles int
	Budget   scanner.Budget

	// Logger receives Pebble's errors. It defaults to slog.Default().
	Logger *slog.Logger
}

type Scanner struct {
	opts Options
}

func New(opts Options) (*Scanner, error) {
	if opts.Codec == nil || opts.Dir == "" {
		return nil, errors.New("pebble scanner: Codec and Dir are required")
	}
	if opts.MinFiles < 1 {
		return nil, errors.New("pebble scanner: MinFiles must be at least 1")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Scanner{opts: opts}, nil
}

func (s *Scanner) Scan(ctx context.Context, reader couponsource.Reader, sources []couponsource.Source, out couponstore.Batch) (stats scanner.Stats, err error) {
	stats.Scanner = Name
	if len(sources) > MaxSources {
		return stats, fmt.Errorf("pebble scanner: %d sources, at most %d are supported", len(sources), MaxSources)
	}
	if err := os.RemoveAll(s.opts.Dir); err != nil {
		return stats, err
	}
	if len(sources) == 0 {
		return stats, nil
	}
	runsDir := filepath.Join(s.opts.Dir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		return stats, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(s.opts.Dir)) }()

	cores := runtime.GOMAXPROCS(0)
	plan := newPlan(s.opts.Budget, cores)
	stats.EncodeWorkers = plan.loaders

	cache := pebbledb.NewCache(plan.cache)
	defer cache.Unref()
	dbOpts := s.options(cache)
	db, err := pebbledb.Open(filepath.Join(s.opts.Dir, "db"), dbOpts)
	if err != nil {
		return stats, fmt.Errorf("open pebble: %w", err)
	}
	defer func() { err = errors.Join(err, db.Close()) }()

	start := time.Now()
	samples, err := s.load(ctx, db, dbOpts, reader, sources, runsDir, plan)
	if err != nil {
		return stats, err
	}
	stats.Scatter = time.Since(start)

	// The runs are garbage now. Return them to the OS before the scan
	// allocates its blocks, so the two phases never add up.
	debug.FreeOSMemory()

	start = time.Now()
	files := int(db.Metrics().Levels[0].TablesCount)
	stats.CountWorkers = scanWorkers(s.opts.Budget, plan.cache, files, cores)
	stats.Codes, err = s.scan(ctx, db, splitPoints(samples, stats.CountWorkers*rangesPerWorker), stats.CountWorkers, out)
	if err != nil {
		return stats, err
	}
	stats.Count = time.Since(start)
	return stats, nil
}

// options are Pebble's settings for a database that is written once by
// ingestion, read once in order, then deleted.
func (s *Scanner) options(cache *pebbledb.Cache) *pebbledb.Options {
	opts := &pebbledb.Options{
		Cache:              cache,
		FormatMajorVersion: pebbledb.FormatNewest, // a new database every build
		// Deleted after the build, so it needs no crash recovery, and read
		// only once, so it needs no compaction.
		DisableWAL:                  true,
		DisableAutomaticCompactions: true,
		// With compaction off, level 0 only grows; never stall because of it.
		L0CompactionThreshold:     math.MaxInt32,
		L0CompactionFileThreshold: math.MaxInt32,
		L0StopWritesThreshold:     math.MaxInt32,
		// Ingestion bypasses the memtable.
		MemTableSize: 1 << 20,
		Logger:       logger{s.opts.Logger},
	}
	opts.Levels[0].BlockSize = blockSize
	// Packed codes are close to random and do not compress.
	opts.Levels[0].Compression = func() *sstable.CompressionProfile { return sstable.NoCompression }
	opts.EnsureDefaults()
	return opts
}

// load reads the sources on the reader's goroutines and sorts and ingests
// their codes on plan.loaders loaders. It returns the codes the runs sampled
// for choosing scan ranges. The first error cancels the rest.
func (s *Scanner) load(ctx context.Context, db *pebbledb.DB, dbOpts *pebbledb.Options, reader couponsource.Reader, sources []couponsource.Source, runsDir string, plan plan) ([]uint64, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	shared := &loadState{
		db:         db,
		runsDir:    runsDir,
		writerOpts: dbOpts.MakeWriterOptions(0, db.TableFormat()),
	}
	chunks := make(chan couponsource.Chunk, plan.loaders)
	var readErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		readErr = reader.Read(ctx, sources, chunks)
		close(chunks)
	})
	for range plan.loaders {
		wg.Go(func() {
			l := loader{s: s, shared: shared, runCodes: plan.runCodes}
			failed := false
			for chunk := range chunks {
				// Keep draining after a failure, so the reader gets its
				// buffers back and can see the cancellation.
				if !failed {
					if err := l.add(ctx, chunk); err != nil {
						cancel(err)
						failed = true
					}
				}
				chunk.Release()
			}
			if !failed && ctx.Err() == nil {
				if err := l.flush(ctx); err != nil {
					cancel(err)
				}
			}
		})
	}
	wg.Wait()

	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return shared.samples, readErr
}

// loadState is shared by every loader of one scan.
type loadState struct {
	db         *pebbledb.DB
	runsDir    string
	writerOpts sstable.WriterOptions
	runs       atomic.Int64

	mu      sync.Mutex
	samples []uint64
}

// entry is one code of a run.
type entry struct {
	code   uint64
	source uint16
}

func compareEntries(a, b entry) int {
	return cmp.Or(cmp.Compare(a.code, b.code), cmp.Compare(a.source, b.source))
}

// loader is one loader's state, reused for every run.
type loader struct {
	s        *Scanner
	shared   *loadState
	runCodes int
	run      []entry
	key      []byte
}

// add packs every code in the chunk into the run, ingesting the run each
// time it fills.
func (l *loader) add(ctx context.Context, chunk couponsource.Chunk) error {
	if l.run == nil {
		l.run = make([]entry, 0, l.runCodes)
	}
	source := uint16(chunk.Source)
	for rest := chunk.Lines; len(rest) > 0; {
		var line []byte
		line, rest, _ = bytes.Cut(rest, []byte{'\n'})
		code, ok := l.s.opts.Codec.Encode(bytes.TrimSuffix(line, []byte{'\r'}))
		if !ok {
			continue
		}
		l.run = append(l.run, entry{code, source})
		if len(l.run) == cap(l.run) {
			if err := l.flush(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// flush sorts the run, writes it as an sstable and ingests it.
//
// Ex: a run of 6 codes from 2 sources
//
//	run:      SUPER100‖0  HAPPYHRS‖1  HAPPYHRS‖0  SUPER100‖0  BIRTHDAY‖1  HAPPYHRS‖1
//	sorted:   BIRTHDAY‖1  HAPPYHRS‖0  HAPPYHRS‖1  HAPPYHRS‖1  SUPER100‖0  SUPER100‖0
//	written:  BIRTHDAY‖1  HAPPYHRS‖0  HAPPYHRS‖1  SUPER100‖0
//
// A key repeated in a later run of the same loader or another loader is
// ingested again and hides the earlier one, so it still counts once.
func (l *loader) flush(ctx context.Context) error {
	if len(l.run) == 0 {
		return nil
	}
	slices.SortFunc(l.run, compareEntries)
	l.run = slices.Compact(l.run)
	l.sample()

	path := filepath.Join(l.shared.runsDir, fmt.Sprintf("%06d.sst", l.shared.runs.Add(1)))
	if err := l.write(path); err != nil {
		return fmt.Errorf("write run: %w", err)
	}
	l.run = l.run[:0]
	if err := l.shared.db.Ingest(ctx, []string{path}); err != nil {
		return fmt.Errorf("ingest run: %w", err)
	}
	// Ingestion links the file into the database; drop the run's own name.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// sample records samplesPerRun evenly spaced codes of the sorted run.
func (l *loader) sample() {
	l.shared.mu.Lock()
	defer l.shared.mu.Unlock()
	for i := range samplesPerRun {
		l.shared.samples = append(l.shared.samples, l.run[i*len(l.run)/samplesPerRun].code)
	}
}

func (l *loader) write(path string) error {
	file, err := vfs.Default.Create(path, vfs.WriteCategoryUnspecified)
	if err != nil {
		return err
	}
	writer := sstable.NewWriter(objstorageprovider.NewFileWritable(file), l.shared.writerOpts)
	for _, e := range l.run {
		l.key = binary.BigEndian.AppendUint16(binary.BigEndian.AppendUint64(l.key[:0], e.code), e.source)
		if err := writer.Set(l.key, nil); err != nil {
			return errors.Join(err, writer.Close())
		}
	}
	return writer.Close() // also closes file
}

// splitPoints picks up to ranges−1 distinct, evenly spaced codes from the
// samples, to split the key space into ranges of about equal size. Samples
// come from every run, so the ranges stay even whatever order the sources
// list their codes in.
func splitPoints(samples []uint64, ranges int) []uint64 {
	slices.Sort(samples)
	var points []uint64
	for i := 1; i < ranges && len(samples) > 0; i++ {
		points = append(points, samples[i*len(samples)/ranges])
	}
	return slices.Compact(points)
}

// scan walks the key ranges between points on workers, and adds each code
// with keys from at least MinFiles sources to out.
func (s *Scanner) scan(ctx context.Context, db *pebbledb.DB, points []uint64, workers int, out couponstore.Batch) (int, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	// Range i is [points[i-1], points[i]) on the 8-byte code prefix, open at
	// either end. Every key of a code shares its prefix, so no code is split
	// between ranges.
	bound := func(i int) []byte {
		if i < 0 || i >= len(points) {
			return nil
		}
		return binary.BigEndian.AppendUint64(nil, points[i])
	}
	ranges := make(chan int)
	var codes atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range ranges {
				if ctx.Err() != nil { // drain without work once cancelled
					continue
				}
				found, err := s.scanRange(ctx, db, bound(i-1), bound(i), out)
				codes.Add(int64(found))
				if err != nil {
					cancel(err)
				}
			}
		})
	}
	for i := range len(points) + 1 {
		ranges <- i
	}
	close(ranges)
	wg.Wait()

	return int(codes.Load()), context.Cause(ctx)
}

// scanRange counts the keys of each code in [lower, upper), which arrive
// side by side, one per source containing the code.
func (s *Scanner) scanRange(ctx context.Context, db *pebbledb.DB, lower, upper []byte, out couponstore.Batch) (codes int, err error) {
	iter, err := db.NewIter(&pebbledb.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, iter.Close()) }()

	var current uint64
	count, walked := 0, 0 // count: sources containing current
	for valid := iter.First(); valid; valid = iter.Next() {
		if walked++; walked%checkEvery == 0 && ctx.Err() != nil {
			return codes, context.Cause(ctx)
		}
		code := binary.BigEndian.Uint64(iter.Key())
		if count > 0 && code == current {
			count++
		} else {
			current, count = code, 1
		}
		if count == s.opts.MinFiles {
			if err := out.Add(s.opts.Codec.Decode(code)); err != nil {
				return codes, fmt.Errorf("add code: %w", err)
			}
			codes++
		}
	}
	return codes, iter.Error()
}

// logger passes Pebble's errors to slog and drops its info messages, which
// report every ingestion.
type logger struct{ *slog.Logger }

func (l logger) Infof(string, ...any) {}

func (l logger) Errorf(format string, args ...any) {
	l.Error("pebble: " + fmt.Sprintf(format, args...))
}

// Fatalf reports a broken invariant inside Pebble. Like Pebble's default
// logger, it exits.
func (l logger) Fatalf(format string, args ...any) {
	l.Error("pebble: " + fmt.Sprintf(format, args...))
	os.Exit(1)
}
