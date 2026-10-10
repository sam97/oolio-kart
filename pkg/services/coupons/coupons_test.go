package coupons

import (
	"compress/gzip"
	"context"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestEncodeRoundTripAndOrder(t *testing.T) {
	codes := []string{"00000000", "0000000000", "HAPPYHRS", "HAPPYHRSA", "HAPPYHRSZ", "Zzzzzzzzzz", "abcdefgh", "zzzzzzzzzz"}
	var packed []uint64
	for _, c := range codes {
		v, ok := encode([]byte(c))
		if !ok {
			t.Fatalf("encode(%q) failed", c)
		}
		if got := decode(v); got != c {
			t.Errorf("decode(encode(%q)) = %q", c, got)
		}
		packed = append(packed, v)
	}
	if !slices.IsSorted(packed) {
		t.Errorf("packed codes are not in string order: %v", packed)
	}

	for _, c := range []string{"", "SHORT77", "ELEVENCHARS", "HAPPY HR", "HAPPY-HR"} {
		if _, ok := encode([]byte(c)); ok {
			t.Errorf("encode(%q) succeeded, want failure", c)
		}
	}
}

func TestBuild(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", "HAPPYHRS\nSUPER100\nSUPER100\nSHORT77\nTOOLONGCODE1\nBOTHAB01\n")
	b := writeFile(t, dir, "b.txt", "HAPPYHRS\r\nBOTHAB01\r\nTENCHARS10\r\n")
	c := writeGzip(t, dir, "c.gz", "TENCHARS10\nHAPPYHRS\nSHORT77\nTOOLONGCODE1\nONLYINC1")

	got, err := Build([]string{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"BOTHAB01", "HAPPYHRS", "TENCHARS10"}
	if !slices.Equal(got, want) {
		t.Errorf("Build = %v, want %v", got, want)
	}
}

func TestMergeCount(t *testing.T) {
	tests := []struct {
		name string
		sets [][]uint64
		min  int
		want []uint64
	}{
		{"no sets", nil, 2, nil},
		{"all sets empty", [][]uint64{{}, {}, {}}, 2, nil},
		{"single set below min", [][]uint64{{1, 2, 3}}, 2, nil},
		{"single set min 1", [][]uint64{{1, 2, 3}}, 1, []uint64{1, 2, 3}},
		{"disjoint sets", [][]uint64{{1, 3}, {2, 4}, {5}}, 2, nil},
		{"identical sets", [][]uint64{{1, 2}, {1, 2}}, 2, []uint64{1, 2}},
		{"shared values", [][]uint64{{3, 7, 9}, {3, 9, 12}, {7, 9}}, 2, []uint64{3, 7, 9}},
		{"min 3 needs all", [][]uint64{{3, 7, 9}, {3, 9, 12}, {7, 9}}, 3, []uint64{9}},
		{"min above set count", [][]uint64{{1}, {1}}, 3, nil},
		{"uneven lengths", [][]uint64{{1}, {1, 2, 3, 4, 5}, {5}}, 2, []uint64{1, 5}},
		{"empty set among others", [][]uint64{{}, {4, 8}, {8}}, 2, []uint64{8}},
		{"zero value", [][]uint64{{0, 1}, {0}}, 2, []uint64{0}},
		{"max value", [][]uint64{{math.MaxUint64}, {1, math.MaxUint64}}, 2, []uint64{math.MaxUint64}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeCount(tt.sets, tt.min); !slices.Equal(got, tt.want) {
				t.Errorf("mergeCount(%v, %d) = %v, want %v", tt.sets, tt.min, got, tt.want)
			}
		})
	}
}

// TestMergeCountMatchesMapCount checks mergeCount against a plain map count on
// random sets.
func TestMergeCountMatchesMapCount(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for range 200 {
		sets := make([][]uint64, 1+rng.IntN(5))
		counts := map[uint64]int{}
		for i := range sets {
			for range rng.IntN(30) {
				sets[i] = append(sets[i], rng.Uint64N(50))
			}
			slices.Sort(sets[i])
			sets[i] = slices.Compact(sets[i])
			for _, value := range sets[i] {
				counts[value]++
			}
		}
		min := 1 + rng.IntN(len(sets))

		var want []uint64
		for value, count := range counts {
			if count >= min {
				want = append(want, value)
			}
		}
		slices.Sort(want)

		if got := mergeCount(sets, min); !slices.Equal(got, want) {
			t.Fatalf("mergeCount(%v, %d) = %v, want %v", sets, min, got, want)
		}
	}
}

// BenchmarkMergeCount measures mergeCount across set counts, set sizes and
// overlap. Overlap is controlled by the value range: values drawn from a range
// equal to the set size overlap heavily, values from a range 1000 times larger
// rarely overlap.
func BenchmarkMergeCount(b *testing.B) {
	for _, numSets := range []int{2, 3, 8} {
		for _, size := range []int{1_000, 100_000, 1_000_000} {
			for _, overlap := range []struct {
				name  string
				scale uint64
			}{{"high", 1}, {"low", 1000}} {
				name := fmt.Sprintf("sets=%d/size=%d/overlap=%s", numSets, size, overlap.name)
				b.Run(name, func(b *testing.B) {
					rng := rand.New(rand.NewPCG(1, 2))
					sets := make([][]uint64, numSets)
					for i := range sets {
						sets[i] = randomSet(rng, size, uint64(size)*overlap.scale)
					}
					b.ReportAllocs()
					for b.Loop() {
						mergeCount(sets, MinFiles)
					}
				})
			}
		}
	}
}

// randomSet returns up to size distinct values below limit, sorted.
func randomSet(rng *rand.Rand, size int, limit uint64) []uint64 {
	set := make([]uint64, size)
	for i := range set {
		set[i] = rng.Uint64N(limit)
	}
	slices.Sort(set)
	return slices.Compact(set)
}

func TestServiceReloadsOnChange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\nFIFTYOFF\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\n")
	writeFile(t, dir, ".ignored", "FIFTYOFF\n")

	loads := make(chan LoadResult, 10)
	s := New(Config{Dir: dir, PollInterval: 10 * time.Millisecond, OnLoad: func(r LoadResult) { loads <- r }})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go s.Run(ctx)

	waitLoad(t, loads)
	if got, want := s.ValidCoupons(), []string{"HAPPYHRS"}; !slices.Equal(got, want) {
		t.Fatalf("ValidCoupons = %v, want %v", got, want)
	}

	writeFile(t, dir, "c.txt", "FIFTYOFF\n")
	waitLoad(t, loads)
	if got, want := s.ValidCoupons(), []string{"FIFTYOFF", "HAPPYHRS"}; !slices.Equal(got, want) {
		t.Fatalf("ValidCoupons = %v, want %v", got, want)
	}
	if !s.IsValid("FIFTYOFF") || s.IsValid("SUPER100") {
		t.Error("IsValid disagrees with ValidCoupons")
	}
}

// TestBaseFiles builds the valid coupons from the real coupon base files in
// COUPONS_DIR, or the repository's data/coupons, and prints them. It calls
// Build directly so it never reads a saved result. Run it with:
// go test -v -run TestBaseFiles ./pkg/services/coupons
func TestBaseFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("loads ~3 GB of coupon data")
	}
	dir := os.Getenv("COUPONS_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "data", "coupons")
	}
	snap, err := scan(dir)
	if err != nil || len(snap) < 3 {
		t.Skipf("copy couponbase1.txt, couponbase2.txt and couponbase3.txt into %s/ to run this test", dir)
	}

	files := slices.Sorted(maps.Keys(snap))
	start := time.Now()
	codes, err := Build(files)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d valid coupons from %d files in %s:", len(codes), len(files), time.Since(start).Round(time.Millisecond))
	for _, c := range codes {
		t.Log("  " + c)
	}

	want := []string{"BIRTHDAY", "BUYGETON", "FIFTYOFF", "FREEZAAA", "GNULINUX", "HAPPYHRS", "OVER9000", "SIXTYOFF"}
	if !slices.Equal(codes, want) {
		t.Errorf("valid coupons = %v, want %v", codes, want)
	}
	for _, c := range []string{"SUPER100", "MOODYHRS", "HAPPYHOURS", "BUYGETONE"} {
		if slices.Contains(codes, c) {
			t.Errorf("%s should be invalid", c)
		}
	}
}

func waitLoad(t *testing.T, loads <-chan LoadResult) LoadResult {
	t.Helper()
	select {
	case r := <-loads:
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		return r
	case <-time.After(5 * time.Minute):
		t.Fatal("timed out waiting for coupons to load")
		return LoadResult{}
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeGzip(t *testing.T, dir, name, content string) string {
	t.Helper()
	var b strings.Builder
	zw := gzip.NewWriter(&b)
	if _, err := zw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeFile(t, dir, name, b.String())
}
