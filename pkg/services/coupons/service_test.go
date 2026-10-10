package coupons

import (
	"cmp"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"runtime/metrics"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dustin/go-humanize"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	envconfig "github.com/sam97/oolio-kart/pkg/helpers/config"
	"github.com/sam97/oolio-kart/pkg/models"
	"github.com/sam97/oolio-kart/pkg/services/coupons/codec"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner"
)

// startService runs the default Service on dir and returns its load results.
// stop cancels it and waits for Run to return, so no write is still in
// flight.
func startService(t *testing.T, dir, bucketDir string, poll time.Duration) (service *Service, loads <-chan LoadResult, stop func()) {
	t.Helper()
	results := make(chan LoadResult, 10)
	service, err := NewDefault(Settings{
		Dir:          dir,
		BucketDir:    bucketDir,
		MemoryLimit:  MinMemoryLimit,
		PollInterval: poll,
		Logger:       slog.New(slog.DiscardHandler),
		OnLoad:       func(result LoadResult) { results <- result },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		service.Run(ctx)
		close(done)
	}()
	stop = func() {
		cancel()
		<-done
	}
	t.Cleanup(stop)
	return service, results, stop
}

func TestServiceReloadsOnChange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\nFIFTYOFF\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\n")
	writeFile(t, dir, ".ignored", "FIFTYOFF\n")

	service, loads, _ := startService(t, dir, t.TempDir(), 10*time.Millisecond)
	waitLoad(t, loads)
	if got, want := service.ValidCoupons(), []string{"HAPPYHRS"}; !slices.Equal(got, want) {
		t.Fatalf("ValidCoupons = %v, want %v", got, want)
	}

	writeFile(t, dir, "c.txt", "FIFTYOFF\n")
	waitLoad(t, loads)
	if got, want := service.ValidCoupons(), []string{"FIFTYOFF", "HAPPYHRS"}; !slices.Equal(got, want) {
		t.Fatalf("ValidCoupons = %v, want %v", got, want)
	}
	if !service.IsValid("FIFTYOFF") || service.IsValid("SUPER100") {
		t.Error("IsValid disagrees with ValidCoupons")
	}
}

func TestServiceRestoresStoredCodes(t *testing.T) {
	dir, bucketDir := t.TempDir(), t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\nFIFTYOFF\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\n")

	_, loads, stop := startService(t, dir, bucketDir, 10*time.Millisecond)
	if result := waitLoad(t, loads); result.Restored {
		t.Fatal("first load was restored, want scanned")
	}
	stop()
	if _, err := os.Stat(filepath.Join(dir, couponstore.FileName)); err != nil {
		t.Fatalf("stored result not written: %v", err)
	}

	// With an hour between polls, only a restore can load the codes in time.
	service, loads, _ := startService(t, dir, bucketDir, time.Hour)
	select {
	case result := <-loads:
		if !result.Restored || result.Codes != 1 {
			t.Errorf("load = %+v, want 1 restored code", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stored codes were not restored on start")
	}
	if !service.IsValid("HAPPYHRS") || service.IsValid("FIFTYOFF") {
		t.Error("IsValid disagrees with the restored codes")
	}
}

func TestServiceRescansWhenFilesChange(t *testing.T) {
	dir, bucketDir := t.TempDir(), t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\nFIFTYOFF\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\n")

	_, loads, stop := startService(t, dir, bucketDir, 10*time.Millisecond)
	waitLoad(t, loads)
	stop()

	writeFile(t, dir, "b.txt", "HAPPYHRS\nFIFTYOFF\n")
	service, loads, _ := startService(t, dir, bucketDir, 10*time.Millisecond)
	if result := waitLoad(t, loads); result.Restored {
		t.Fatal("restored codes built from older files")
	}
	if got, want := service.ValidCoupons(), []string{"FIFTYOFF", "HAPPYHRS"}; !slices.Equal(got, want) {
		t.Errorf("ValidCoupons = %v, want %v", got, want)
	}
}

func TestServiceIgnoresCorruptStoredFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\n")
	writeFile(t, dir, couponstore.FileName, "{not json")

	service, loads, _ := startService(t, dir, t.TempDir(), 10*time.Millisecond)
	if result := waitLoad(t, loads); result.Restored {
		t.Fatal("restored a corrupt stored file")
	}
	if !service.IsValid("HAPPYHRS") {
		t.Error("HAPPYHRS should be valid")
	}
}

func TestNewDefaultRejectsSmallMemoryLimit(t *testing.T) {
	_, err := NewDefault(Settings{Dir: t.TempDir(), BucketDir: t.TempDir(), MemoryLimit: MinMemoryLimit - 1})
	if err == nil {
		t.Error("NewDefault accepted a memory limit below the minimum")
	}
}

func TestBudget(t *testing.T) {
	budget := NewBudget(256 << 20)
	want := Budget{
		Total:   256 << 20,
		Reserve: 64 << 20,
		Reader:  48 << 20,
		Scanner: scanner.Budget{Encoders: 48 << 20, Buckets: 48 << 20, Count: 192 << 20},
	}
	if budget != want {
		t.Errorf("NewBudget(256 MiB) = %+v, want %+v", budget, want)
	}
}

// TestBaseFiles builds the valid coupons from the real coupon base files in
// COUPONS_DIR, or the repository's data/coupons, under the cap in
// COUPONS_MEMORY_LIMIT, or 256 MiB, and fails if memory goes over it. Bucket
// files go to COUPONS_BUCKET_DIR, or a temporary folder. Run it with:
//
//	go test -v -run TestBaseFiles ./pkg/services/coupons
func TestBaseFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("reads ~3 GB of coupon data")
	}
	dir := cmp.Or(os.Getenv("COUPONS_DIR"), filepath.Join("..", "..", "..", "data", "coupons"))
	bucketDir := cmp.Or(os.Getenv("COUPONS_BUCKET_DIR"), t.TempDir())
	var limit envconfig.ByteSize
	if err := limit.UnmarshalText([]byte(cmp.Or(os.Getenv("COUPONS_MEMORY_LIMIT"), "256MiB"))); err != nil {
		t.Fatal(err)
	}

	budget := NewBudget(int64(limit))
	reader := couponsource.NewFS(dir, budget.Reader, couponsource.NewGzip())
	sources, err := reader.List(t.Context())
	if err != nil || len(sources) < 3 {
		t.Skipf("copy couponbase1, couponbase2 and couponbase3 (.txt or .gz) into %s/ to run this test", dir)
	}
	codes, err := codec.NewAlphanumeric(models.CouponMinLength, models.CouponMaxLength)
	if err != nil {
		t.Fatal(err)
	}
	buckets, err := scanner.NewBuckets(scanner.BucketOptions{Codec: codes, MinFiles: 2, Dir: bucketDir, Budget: budget.Scanner})
	if err != nil {
		t.Fatal(err)
	}

	defer debug.SetMemoryLimit(debug.SetMemoryLimit(int64(limit)))
	debug.FreeOSMemory()
	peak := startMemorySampler()
	var found collected
	stats, err := buckets.Scan(t.Context(), reader, sources, &found)
	footprint := peak()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(found.codes)

	t.Logf("%d valid coupons from %d files: %v", len(found.codes), len(sources), found.codes)
	t.Logf("stats: %+v", stats)
	t.Logf("peak Go memory %s, limit %s", humanize.IBytes(footprint), humanize.IBytes(uint64(limit)))

	want := []string{"BIRTHDAY", "BUYGETON", "FIFTYOFF", "FREEZAAA", "GNULINUX", "HAPPYHRS", "OVER9000", "SIXTYOFF"}
	if !slices.Equal(found.codes, want) {
		t.Errorf("valid coupons = %v, want %v", found.codes, want)
	}
	if footprint > uint64(limit) {
		t.Errorf("peak Go memory %s is over the %s limit", humanize.IBytes(footprint), humanize.IBytes(uint64(limit)))
	}
}

// collected is a couponstore.Batch that keeps codes in memory.
type collected struct{ codes []string }

func (c *collected) Add(code string) error { c.codes = append(c.codes, code); return nil }
func (c *collected) Commit() error         { return nil }
func (c *collected) Abort()                {}

// startMemorySampler polls the memory the Go runtime holds from the OS until
// the returned function is called, which reports the peak.
func startMemorySampler() (stop func() uint64) {
	samples := []metrics.Sample{
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
	}
	done := make(chan struct{})
	var peak uint64
	var wg sync.WaitGroup
	wg.Go(func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			metrics.Read(samples)
			peak = max(peak, samples[0].Value.Uint64()-samples[1].Value.Uint64())
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	})
	return func() uint64 {
		close(done)
		wg.Wait()
		return peak
	}
}

func waitLoad(t *testing.T, loads <-chan LoadResult) LoadResult {
	t.Helper()
	select {
	case result := <-loads:
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		return result
	case <-time.After(5 * time.Minute):
		t.Fatal("timed out waiting for coupons to load")
		return LoadResult{}
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
