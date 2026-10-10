package pebble

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/services/coupons/codec"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner/scannertest"
)

var (
	ampleBudget = scanner.Budget{Encoders: 4 << 20, Buckets: 4 << 20, Count: 16 << 20}
	// tinyBudget gives the smallest runs, so most inputs span many runs.
	tinyBudget = scanner.Budget{Encoders: 64 << 10, Buckets: 64 << 10, Count: 1 << 20}
)

func newScanner(t *testing.T, minFiles int, budget scanner.Budget) *Scanner {
	t.Helper()
	codes, err := codec.NewAlphanumeric(8, 10)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Dir: filepath.Join(t.TempDir(), "pebble"), Codec: codes, MinFiles: minFiles, Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func scan(t *testing.T, s *Scanner, sources ...couponsource.Source) ([]string, scanner.Stats) {
	t.Helper()
	var out scannertest.Collected
	stats, err := s.Scan(t.Context(), scannertest.Reader{Sources: sources}, sources, &out)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out.Codes)
	return out.Codes, stats
}

func TestNew(t *testing.T) {
	codes, err := codec.NewAlphanumeric(8, 10)
	if err != nil {
		t.Fatal(err)
	}
	for name, opts := range map[string]Options{
		"no dir":      {Codec: codes, MinFiles: 2},
		"no codec":    {Dir: t.TempDir(), MinFiles: 2},
		"no minFiles": {Dir: t.TempDir(), Codec: codes},
	} {
		if _, err := New(opts); err == nil {
			t.Errorf("%s: New accepted %+v", name, opts)
		}
	}
}

func TestScan(t *testing.T) {
	s := newScanner(t, 2, ampleBudget)
	got, stats := scan(t, s,
		scannertest.File{Name: "a", Data: "HAPPYHRS\nSUPER100\nSUPER100\nSHORT77\nTOOLONGCODE1\nBOTHAB01\n"},
		scannertest.File{Name: "b", Data: "HAPPYHRS\r\nBOTHAB01\r\nTENCHARS10\r\n"},
		scannertest.File{Name: "c", Data: "TENCHARS10\nHAPPYHRS\nSHORT77\nTOOLONGCODE1\nONLYINC1"},
	)
	if want := []string{"BOTHAB01", "HAPPYHRS", "TENCHARS10"}; !slices.Equal(got, want) {
		t.Errorf("Scan = %v, want %v", got, want)
	}
	if stats.Scanner != Name || stats.Codes != len(got) || stats.EncodeWorkers < 1 || stats.CountWorkers < 1 {
		t.Errorf("stats = %+v", stats)
	}
}

// TestSuite runs the shared edge and stress cases with an ample budget and
// with one small enough that most inputs span many runs.
func TestSuite(t *testing.T) {
	for name, budget := range map[string]scanner.Budget{"ample": ampleBudget, "tiny": tinyBudget} {
		t.Run(name, func(t *testing.T) {
			scannertest.Run(t, func(t *testing.T, minFiles int, sources []couponsource.Source, out couponstore.Batch) error {
				s := newScanner(t, minFiles, budget)
				_, err := s.Scan(t.Context(), scannertest.Reader{Sources: sources}, sources, out)
				return err
			})
		})
	}
}

// TestScanManyRuns checks a scan of many runs, ingested files and key ranges
// against the reference count.
func TestScanManyRuns(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	pool := make([]string, 50_000)
	for i := range pool {
		pool[i] = fmt.Sprintf("C%07d%s", rng.IntN(1e7), strings.Repeat("X", rng.IntN(2)))
	}
	sources := make([]couponsource.Source, 4)
	for i := range sources {
		var text strings.Builder
		for range 100_000 {
			text.WriteString(pool[rng.IntN(len(pool))] + "\n")
		}
		sources[i] = scannertest.File{Name: fmt.Sprint(i), Data: text.String()}
	}

	got, stats := scan(t, newScanner(t, 2, tinyBudget), sources...)
	if want := scannertest.Want(sources, 2); !slices.Equal(got, want) {
		t.Fatalf("found %d codes, want %d", len(got), len(want))
	}
	if stats.CountWorkers < 1 {
		t.Errorf("stats = %+v", stats)
	}
}

// TestScanRepeatedCode checks that a code repeated in one source counts once
// even when its repeats land in different runs, and so in different files.
func TestScanRepeatedCode(t *testing.T) {
	repeated := scannertest.File{Name: "repeated", Data: strings.Repeat("HAPPYHRS\n", 300_000)}

	got, _ := scan(t, newScanner(t, 2, tinyBudget), repeated, scannertest.File{Name: "other", Data: "SUPER100\n"})
	if len(got) != 0 {
		t.Errorf("a code in one source counted as %v", got)
	}

	got, _ = scan(t, newScanner(t, 2, tinyBudget), repeated, scannertest.File{Name: "other", Data: "HAPPYHRS\n"})
	if want := []string{"HAPPYHRS"}; !slices.Equal(got, want) {
		t.Errorf("Scan = %v, want %v", got, want)
	}
}

func TestScanCancelled(t *testing.T) {
	s := newScanner(t, 2, ampleBudget)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	sources, _ := scannertest.RandomSources(rand.New(rand.NewPCG(3, 4)), 3)
	var out scannertest.Collected
	if _, err := s.Scan(ctx, scannertest.Reader{Sources: sources}, sources, &out); !errors.Is(err, context.Canceled) {
		t.Errorf("Scan = %v, want context.Canceled", err)
	}
	if len(out.Codes) != 0 {
		t.Errorf("a cancelled scan added %d codes", len(out.Codes))
	}
}

// TestScanRemovesDir checks that the database is deleted after a scan, and
// after a scan that failed.
func TestScanRemovesDir(t *testing.T) {
	sources, _ := scannertest.RandomSources(rand.New(rand.NewPCG(5, 6)), 3)

	s := newScanner(t, 2, ampleBudget)
	if _, err := s.Scan(t.Context(), scannertest.Reader{Sources: sources}, sources, &scannertest.Collected{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.opts.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after a scan, %s: %v", s.opts.Dir, err)
	}

	failing := failingBatch{errors.New("store is down")}
	if _, err := s.Scan(t.Context(), scannertest.Reader{Sources: sources}, sources, failing); !errors.Is(err, failing.err) {
		t.Fatalf("Scan = %v, want %v", err, failing.err)
	}
	if _, err := os.Stat(s.opts.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after a failed scan, %s: %v", s.opts.Dir, err)
	}
}

func TestScanTooManySources(t *testing.T) {
	sources := make([]couponsource.Source, MaxSources+1)
	for i := range sources {
		sources[i] = sized(0)
	}
	s := newScanner(t, 2, ampleBudget)
	if _, err := s.Scan(t.Context(), scannertest.Reader{}, sources, &scannertest.Collected{}); err == nil {
		t.Errorf("Scan accepted %d sources", len(sources))
	}
}

func TestPlan(t *testing.T) {
	// A 256 MiB cap on 16 cores, as in newPlan's example.
	budget := scanner.Budget{Encoders: 48 << 20, Buckets: 48 << 20, Count: 192 << 20}
	p := newPlan(budget, 16)
	if p.loaders != 4 || p.runCodes != 1_572_864 || p.cache != 8<<20 {
		t.Errorf("256 MiB plan = %+v", p)
	}
	if load := int64(p.loaders * p.runCodes * entryBytes); load > budget.Encoders+budget.Buckets {
		t.Errorf("runs take %d bytes, over the %d budget", load, budget.Encoders+budget.Buckets)
	}
	if workers := scanWorkers(budget, p.cache, 210, 16); workers != 16 {
		t.Errorf("scan workers for 210 files = %d, want 16", workers)
	}

	// The 32 MiB minimum cap.
	small := scanner.Budget{Encoders: 6 << 20, Buckets: 6 << 20, Count: 24 << 20}
	p = newPlan(small, 16)
	if p.loaders != 1 || p.runCodes != 786_432 || p.cache != 3<<20 {
		t.Errorf("32 MiB plan = %+v", p)
	}
	if workers := scanWorkers(small, p.cache, 400, 16); workers != 1 {
		t.Errorf("scan workers for 400 files = %d, want 1", workers)
	}

	// Runs never shrink below the minimum, and a few cores mean few loaders.
	p = newPlan(scanner.Budget{Encoders: 1, Buckets: 1, Count: 1}, 2)
	if p.loaders != 1 || p.runCodes != minRunCodes {
		t.Errorf("tiny plan = %+v", p)
	}
}

func TestSplitPoints(t *testing.T) {
	if points := splitPoints(nil, 8); len(points) != 0 {
		t.Errorf("splitPoints with no samples = %v", points)
	}
	if points := splitPoints([]uint64{5, 5, 5, 5}, 4); !slices.Equal(points, []uint64{5}) {
		t.Errorf("splitPoints of equal samples = %v, want [5]", points)
	}
	samples := []uint64{90, 10, 70, 30, 50, 20, 80, 40, 60, 0}
	if points := splitPoints(samples, 5); !slices.Equal(points, []uint64{20, 40, 60, 80}) {
		t.Errorf("splitPoints = %v, want [20 40 60 80]", points)
	}
}

type sized int64

func (s sized) Info() couponsource.Info          { return couponsource.Info{} }
func (s sized) Open() (io.ReadSeekCloser, error) { return nil, errors.New("not readable") }
func (s sized) EstimatedSize() int64             { return int64(s) }

// failingBatch fails every Add.
type failingBatch struct{ err error }

func (b failingBatch) Add(string) error                                   { return b.err }
func (b failingBatch) Commit(context.Context, couponstore.Manifest) error { return nil }
func (b failingBatch) Abort()                                             {}
