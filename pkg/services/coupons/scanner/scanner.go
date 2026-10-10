// Package scanner finds the coupon codes that appear in at least MinFiles
// sources.
package scanner

import (
	"context"
	"errors"
	"runtime/debug"
	"sync"
	"time"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/services/coupons/codec"
)

// Scanner reads every source and adds each valid code to out. It does not
// commit out.
type Scanner interface {
	Scan(ctx context.Context, reader couponsource.Reader, sources []couponsource.Source, out couponstore.Batch) (Stats, error)
}

// Stats describes one scan.
type Stats struct {
	Buckets       int
	EncodeWorkers int
	CountWorkers  int
	SlotBytes     int64 // memory each count worker holds

	BucketBytes   int64 // written during scatter
	LargestBucket int64 // one bucket summed across sources

	// Oversized counts buckets that did not fit in a slot and were sorted
	// on disk instead.
	Oversized int

	Codes   int
	Scatter time.Duration
	Count   time.Duration
}

// Budget is the memory, in bytes, the Buckets scanner may use for each part
// of its work. Scatter uses Encoders and Buckets together; Count runs after
// scatter has finished and may use the same memory again.
type Budget struct {
	Encoders int64 // each encode worker's arrays
	Buckets  int64 // write buffers, one per (source, bucket)
	Count    int64 // one slot per count worker
}

type BucketOptions struct {
	Codec    codec.Codec
	MinFiles int

	// Dir holds the bucket files. Each scan replaces the previous scan's
	// folders and manifest in it, and leaves anything else alone.
	Dir string

	// Rules describes the validity rules; it is recorded in the manifest.
	Rules string

	Budget Budget
}

// Buckets scans in two phases, scatter and count, with bucket files on disk
// in between, so its memory use is set by its Budget rather than by the size
// of the input:
//
//	                       scatter                                  count, per bucket i
//	source 0 ─┐                           ┌─ src0/b000.dat … b189.dat ─┐
//	source 1 ─┼─ chunks ─ encode workers ─┼─ src1/b000.dat … b189.dat ─┼─ sort each source's b_i, k-way
//	source 2 ─┘          code → hash % P  └─ src2/b000.dat … b189.dat ─┘  merge them, keep codes in
//	                                                                       ≥ MinFiles sources
//
// Every occurrence of a code lands in the same bucket number in each source,
// so each bucket can be counted on its own, in a fixed amount of memory.
type Buckets struct {
	opts     BucketOptions
	hashName string
	hash     func(uint64) uint64
}

func NewBuckets(opts BucketOptions) (*Buckets, error) {
	if opts.Codec == nil || opts.Dir == "" {
		return nil, errors.New("scanner: Codec and Dir are required")
	}
	if opts.MinFiles < 1 {
		return nil, errors.New("scanner: MinFiles must be at least 1")
	}
	return &Buckets{opts: opts, hashName: "splitmix64", hash: splitmix64}, nil
}

// UseHashFunction replaces the function that spreads codes over buckets. It
// should mix all 64 bits, since buckets are hash(code) % P. name is recorded
// in the manifest.
func (b *Buckets) UseHashFunction(name string, hash func(uint64) uint64) {
	b.hashName, b.hash = name, hash
}

func (b *Buckets) Scan(ctx context.Context, reader couponsource.Reader, sources []couponsource.Source, out couponstore.Batch) (Stats, error) {
	if err := wipe(b.opts.Dir); err != nil {
		return Stats{}, err
	}
	if len(sources) == 0 {
		return Stats{}, nil
	}

	plan := newPlan(b.opts.Budget, b.opts.Codec.MinLength(), reader.ChunkSize(len(sources)), sources)
	stats := Stats{
		Buckets:       plan.buckets,
		EncodeWorkers: plan.encoders,
		CountWorkers:  plan.countWorkers,
	}

	start := time.Now()
	files, err := createBucketFiles(b.opts.Dir, sources, plan)
	if err != nil {
		return stats, err
	}
	err = b.scatter(ctx, reader, sources, files, plan)
	if err = errors.Join(err, closeBucketFiles(files)); err != nil {
		return stats, err
	}
	stats.Scatter = time.Since(start)
	for bucket := range plan.buckets {
		var total int64
		for source := range files {
			total += files[source][bucket].codes * 8
		}
		stats.BucketBytes += total
		stats.LargestBucket = max(stats.LargestBucket, total)
	}

	// The scatter buffers are garbage now. Return them to the OS before the
	// count phase allocates its slots, so the two phases never add up.
	debug.FreeOSMemory()

	start = time.Now()
	codesPerSlot := min(int64(plan.codesPerSlot), stats.LargestBucket/8)
	stats.SlotBytes = codesPerSlot * 8
	stats.Codes, stats.Oversized, err = b.countAll(ctx, files, int(codesPerSlot), plan.countWorkers, out)
	if err != nil {
		return stats, err
	}
	stats.Count = time.Since(start)

	return stats, writeManifest(b.opts.Dir, b.newManifest(sources, files, plan))
}

// firstError keeps the first error reported by any goroutine.
type firstError struct {
	mu  sync.Mutex
	err error
}

func (e *firstError) set(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err == nil {
		e.err = err
	}
}

func (e *firstError) get() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}
