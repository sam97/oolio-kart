package scanner

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sam97/oolio-kart/pkg/datasources"
	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/services/coupons/codec"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner/scannertest"
)

// memSource is a source held in memory.
func memSource(name, data string) scannertest.File {
	return scannertest.File{Name: name, Data: data}
}

var defaultBudget = Budget{Encoders: 4 << 20, Buckets: 4 << 20, Count: 16 << 20}

func newScanner(t *testing.T, minFiles int, budget Budget) *Buckets {
	t.Helper()
	alnum, err := codec.NewAlphanumeric(8, 10)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBuckets(BucketOptions{Codec: alnum, MinFiles: minFiles, Dir: t.TempDir(), Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func scan(t *testing.T, b *Buckets, sources ...couponsource.Source) ([]string, Stats) {
	t.Helper()
	var out scannertest.Collected
	stats, err := b.Scan(t.Context(), scannertest.Reader{Sources: sources}, sources, &out)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out.Codes)
	return out.Codes, stats
}

func TestScan(t *testing.T) {
	b := newScanner(t, 2, defaultBudget)
	got, _ := scan(t, b,
		memSource("a", "HAPPYHRS\nSUPER100\nSUPER100\nSHORT77\nTOOLONGCODE1\nBOTHAB01\n"),
		memSource("b", "HAPPYHRS\r\nBOTHAB01\r\nTENCHARS10\r\n"),
		memSource("c", "TENCHARS10\nHAPPYHRS\nSHORT77\nTOOLONGCODE1\nONLYINC1"),
	)
	if want := []string{"BOTHAB01", "HAPPYHRS", "TENCHARS10"}; !slices.Equal(got, want) {
		t.Errorf("Scan = %v, want %v", got, want)
	}
}

// TestScanMatchesMapCount checks Scan against a plain map count on random
// input, with budgets that give many buckets, one bucket, and buckets too big
// for a slot, and with a hash that spreads codes badly.
func TestScanMatchesMapCount(t *testing.T) {
	configs := []struct {
		name     string
		minFiles int
		budget   Budget
		hash     func(uint64) uint64
	}{
		{"default", 2, defaultBudget, nil},
		{"many buckets", 2, Budget{Encoders: 1, Buckets: 1 << 20, Count: 64 << 10}, nil},
		{"min files 3", 3, Budget{Encoders: 1, Buckets: 1 << 20, Count: 64 << 10}, nil},
		{"oversized", 2, Budget{Encoders: 1, Buckets: 1, Count: 8 << 10}, nil},
		{"identity hash", 2, Budget{Encoders: 1, Buckets: 1 << 20, Count: 64 << 10}, func(x uint64) uint64 { return x }},
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for round := range 10 {
		sources, counts := scannertest.RandomSources(rng, 2+rng.IntN(5))
		for _, config := range configs {
			t.Run(fmt.Sprintf("round%d/%s", round, config.name), func(t *testing.T) {
				b := newScanner(t, config.minFiles, config.budget)
				if config.hash != nil {
					b.UseHashFunction("identity", config.hash)
				}
				var want []string
				for code, files := range counts {
					if files >= config.minFiles {
						want = append(want, code)
					}
				}
				slices.Sort(want)

				got, stats := scan(t, b, sources...)
				if !slices.Equal(got, want) {
					t.Fatalf("Scan = %v, want %v (stats %+v)", got, want, stats)
				}
				if config.name == "oversized" && stats.Oversized == 0 {
					t.Errorf("no bucket was oversized: %+v", stats)
				}
			})
		}
	}
}

func TestScanRepeatedCode(t *testing.T) {
	// One code repeated far past a slot always lands in one bucket.
	repeated := strings.Repeat("REPEATED\n", 20_000)
	b := newScanner(t, 2, Budget{Encoders: 1, Buckets: 1, Count: 8 << 10})
	got, stats := scan(t, b,
		memSource("a", repeated+"HAPPYHRS\n"),
		memSource("b", repeated),
		memSource("c", "HAPPYHRS\nSUPER100\n"),
	)
	if want := []string{"HAPPYHRS", "REPEATED"}; !slices.Equal(got, want) {
		t.Errorf("Scan = %v, want %v", got, want)
	}
	if stats.Oversized == 0 {
		t.Errorf("no bucket was oversized: %+v", stats)
	}
}

func TestScanManySources(t *testing.T) {
	var sources []couponsource.Source
	for i := range 20 {
		data := fmt.Sprintf("ONLYIN%02d\n", i)
		if i == 3 || i == 17 {
			data += "IN3AND17\n"
		}
		sources = append(sources, memSource(fmt.Sprintf("source%02d", i), data))
	}
	got, _ := scan(t, newScanner(t, 2, defaultBudget), sources...)
	if want := []string{"IN3AND17"}; !slices.Equal(got, want) {
		t.Errorf("Scan = %v, want %v", got, want)
	}
}

// TestBucketFiles checks the files a scan leaves behind: in each source's
// folder every bucket sorted and de-duplicated, as the layout describes.
func TestBucketFiles(t *testing.T) {
	b := newScanner(t, 2, Budget{Encoders: 1, Buckets: 1 << 20, Count: 64 << 10})
	b.UseHashFunction("identity", func(x uint64) uint64 { return x })
	keep := filepath.Join(b.opts.Dir, "keep.txt")
	if err := os.WriteFile(keep, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(3, 4))
	sources, _ := scannertest.RandomSources(rng, 3)
	_, stats := scan(t, b, sources...)

	m := stats.Layout
	if m.Buckets != stats.Buckets || m.Hash != "identity" || len(m.Sources) != 3 {
		t.Fatalf("layout = %+v, stats %+v", m, stats)
	}
	for _, source := range m.Sources {
		var codes int64
		for bucket := range m.Buckets {
			data, err := os.ReadFile(filepath.Join(b.opts.Dir, source.Dir, bucketName(bucket)))
			if err != nil {
				t.Fatal(err)
			}
			values := make([]uint64, len(data)/8)
			copy(asBytes(values), data)
			for i := 1; i < len(values); i++ {
				if values[i-1] >= values[i] {
					t.Fatalf("%s/%s is not sorted and de-duplicated", source.Dir, bucketName(bucket))
				}
			}
			codes += int64(len(values))
		}
		if codes != source.Codes {
			t.Errorf("%s holds %d codes, layout says %d", source.Dir, codes, source.Codes)
		}
	}

	// A second scan replaces the first one's folders, and nothing else.
	scan(t, b, memSource("other", "HAPPYHRS\n"))
	entries, err := os.ReadDir(b.opts.Dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{"keep.txt", sourceDir("other")}; !slices.Equal(names, want) {
		t.Errorf("bucket folder holds %v, want %v", names, want)
	}
}

func TestScanCancelled(t *testing.T) {
	b := newScanner(t, 2, defaultBudget)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	sources := []couponsource.Source{memSource("a", "HAPPYHRS\n"), memSource("b", "HAPPYHRS\n")}
	if _, err := b.Scan(ctx, scannertest.Reader{Sources: sources}, sources, &scannertest.Collected{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Scan = %v, want context.Canceled", err)
	}
}

// TestSuite runs the shared edge and stress cases with an ample budget, with
// a budget so small that most buckets are sorted on disk, and reading real
// files, plain and gzipped, through the default file reader.
func TestSuite(t *testing.T) {
	budgets := map[string]Budget{
		"ample":     defaultBudget,
		"oversized": {Encoders: 1, Buckets: 1, Count: 8 << 10},
	}
	for name, budget := range budgets {
		t.Run(name, func(t *testing.T) {
			scannertest.Run(t, func(t *testing.T, minFiles int, sources []couponsource.Source, out couponstore.Batch) error {
				b := newScanner(t, minFiles, budget)
				stats, err := b.Scan(t.Context(), scannertest.Reader{Sources: sources}, sources, out)
				checkOpenFiles(t, stats, len(sources))
				return err
			})
		})
	}

	for _, gzipped := range []bool{false, true} {
		t.Run(fmt.Sprintf("files gzip=%v", gzipped), func(t *testing.T) {
			scannertest.Run(t, func(t *testing.T, minFiles int, sources []couponsource.Source, out couponstore.Batch) error {
				dir := t.TempDir()
				for _, source := range sources {
					writeSource(t, dir, source.(scannertest.File), gzipped)
				}
				reader := datasources.CouponFiles(dir)(4 << 20)
				listed, err := reader.List(t.Context())
				if err != nil {
					return err
				}
				if len(listed) != len(sources) {
					t.Fatalf("listed %d files, wrote %d", len(listed), len(sources))
				}
				b := newScanner(t, minFiles, defaultBudget)
				stats, err := b.Scan(t.Context(), reader, listed, out)
				checkOpenFiles(t, stats, len(listed))
				return err
			})
		})
	}
}

// checkOpenFiles checks that the bucket files open at once stay within
// maxBucketFiles; with more sources than that, one bucket each.
func checkOpenFiles(t *testing.T, stats Stats, sources int) {
	t.Helper()
	if sources > 0 && stats.Buckets*sources > max(maxBucketFiles, sources) {
		t.Errorf("%d buckets × %d sources is over the %d open files allowed", stats.Buckets, sources, maxBucketFiles)
	}
}

// writeSource writes file into dir, gzipped with a .gz suffix if asked.
func writeSource(t *testing.T, dir string, file scannertest.File, gzipped bool) {
	t.Helper()
	path := filepath.Join(dir, file.Name)
	if !gzipped {
		if err := os.WriteFile(path, []byte(file.Data), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	out, err := os.Create(path + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	writer := gzip.NewWriter(out)
	if _, err := io.WriteString(writer, file.Data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMerge(t *testing.T) {
	tests := []struct {
		name    string
		streams [][]uint64
		want    map[uint64]int
	}{
		{"no streams", nil, map[uint64]int{}},
		{"all empty", [][]uint64{{}, {}}, map[uint64]int{}},
		{"one stream", [][]uint64{{1, 2, 3}}, map[uint64]int{1: 1, 2: 1, 3: 1}},
		{"disjoint", [][]uint64{{1, 3}, {2, 4}}, map[uint64]int{1: 1, 2: 1, 3: 1, 4: 1}},
		{"identical", [][]uint64{{1, 2}, {1, 2}}, map[uint64]int{1: 2, 2: 2}},
		{"shared", [][]uint64{{3, 7, 9}, {3, 9, 12}, {7, 9}}, map[uint64]int{3: 2, 7: 2, 9: 3, 12: 1}},
		{"uneven", [][]uint64{{1}, {1, 2, 3, 4, 5}, {5}}, map[uint64]int{1: 2, 2: 1, 3: 1, 4: 1, 5: 2}},
		{"extremes", [][]uint64{{0, 1 << 63}, {0, 1 << 63}}, map[uint64]int{0: 2, 1 << 63: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cursors []*cursor
			for _, stream := range tt.streams {
				cursors = append(cursors, memoryCursor(stream))
			}
			got := map[uint64]int{}
			var order []uint64
			err := merge(cursors, func(code uint64, count int) error {
				got[code] = count
				order = append(order, code)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(got, tt.want) || !slices.IsSorted(order) {
				t.Errorf("merge = %v in order %v, want %v", got, order, tt.want)
			}
		})
	}
}

// TestMergeFiles streams files through windows smaller than the files.
func TestMergeFiles(t *testing.T) {
	dir := t.TempDir()
	streams := [][]uint64{{1, 4, 5, 8, 9, 12}, {2, 4, 6, 8}, {9}}
	var cursors []*cursor
	for i, stream := range streams {
		path := filepath.Join(dir, fmt.Sprint(i))
		if err := writeValues(path, stream); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		c, err := fileCursor(file, int64(len(stream)), make([]uint64, 2))
		if err != nil {
			t.Fatal(err)
		}
		cursors = append(cursors, c)
	}
	var shared []uint64
	err := merge(cursors, func(code uint64, count int) error {
		if count >= 2 {
			shared = append(shared, code)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint64{4, 8, 9}; !slices.Equal(shared, want) {
		t.Errorf("shared = %v, want %v", shared, want)
	}
}

// TestNewPlan checks the sizes for the real coupon files at a 256 MiB cap.
func TestNewPlan(t *testing.T) {
	sources := []couponsource.Source{sized(965_346_993), sized(1_072_607_752), sized(1_084_227_656)}
	budget := Budget{Encoders: 48 << 20, Buckets: 48 << 20, Count: 192 << 20}
	p := newPlan(budget, 8, 1<<20, sources)

	if p.codesPerSlot*8*p.countWorkers > int(budget.Count) {
		t.Errorf("count slots %d × %d bytes exceed %d", p.countWorkers, p.codesPerSlot*8, budget.Count)
	}
	if p.writerSize*p.buckets*len(sources) > int(budget.Buckets) {
		t.Errorf("writers %d × %d exceed %d", p.buckets*len(sources), p.writerSize, budget.Buckets)
	}
	// Bucket i across all sources should fit one slot.
	if bucketBytes := int64(3_122_182_401) / 9 * 8 / int64(p.buckets); bucketBytes > int64(p.codesPerSlot)*8 {
		t.Errorf("%d buckets of %d bytes do not fit %d-byte slots", p.buckets, bucketBytes, p.codesPerSlot*8)
	}

	// When even 4 KiB writers would not fit, the bucket count drops.
	small := newPlan(Budget{Encoders: 1, Buckets: 3 * 4 << 10, Count: 1 << 20}, 8, 1<<20, sources)
	if small.buckets != 1 || small.writerSize != minWriterSize {
		t.Errorf("small plan = %+v, want one bucket of 4 KiB writers", small)
	}
}

type sized int64

func (s sized) Info() couponsource.Info          { return couponsource.Info{} }
func (s sized) Open() (io.ReadSeekCloser, error) { return nil, errors.New("not readable") }
func (s sized) EstimatedSize() int64             { return int64(s) }
