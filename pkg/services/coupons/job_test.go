package coupons

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"runtime/metrics"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dustin/go-humanize"

	"github.com/sam97/oolio-kart/pkg/datasources"
	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/datasources/datasourcestest"
	envconfig "github.com/sam97/oolio-kart/pkg/helpers/config"
	"github.com/sam97/oolio-kart/pkg/models"
	"github.com/sam97/oolio-kart/pkg/services/coupons/codec"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner/clickhouse"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner/pebble"
)

// memoryStore is a couponstore.Store and couponstore.Settings in memory.
type memoryStore struct {
	mu        sync.Mutex
	settings  models.CouponSettings
	codes     []string
	published *couponstore.Fingerprint
	begun     int
	aborted   int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{settings: models.CouponSettings{MinLength: 8, MaxLength: 10, MinFiles: 2, DiscountPercent: 10}}
}

func (m *memoryStore) Settings(context.Context) (models.CouponSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings, nil
}

func (m *memoryStore) setSettings(update func(*models.CouponSettings)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	update(&m.settings)
}

func (m *memoryStore) Published(context.Context) (couponstore.Fingerprint, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.published == nil {
		return couponstore.Fingerprint{}, false, nil
	}
	return *m.published, true, nil
}

func (m *memoryStore) Begin(context.Context) (couponstore.Batch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.begun++
	return &memoryBatch{store: m}, nil
}

// publishedCodes returns the published codes, sorted.
func (m *memoryStore) publishedCodes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Sorted(slices.Values(m.codes))
}

type memoryBatch struct {
	store *memoryStore
	mu    sync.Mutex
	codes []string
	done  bool
}

func (b *memoryBatch) Add(code string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.codes = append(b.codes, code)
	return nil
}

func (b *memoryBatch) Commit(_ context.Context, manifest couponstore.Manifest) error {
	b.store.mu.Lock()
	defer b.store.mu.Unlock()
	b.store.codes, b.store.published, b.done = b.codes, &manifest.Fingerprint, true
	return nil
}

func (b *memoryBatch) Abort() {
	if b.done {
		return
	}
	b.store.mu.Lock()
	defer b.store.mu.Unlock()
	b.store.aborted++
	b.done = true
}

// memoryLock is a Locker; held means another run has it.
type memoryLock struct{ held bool }

func (l *memoryLock) TryLock(context.Context) (func(), bool, error) {
	if l.held {
		return nil, false, nil
	}
	l.held = true
	return func() { l.held = false }, true, nil
}

// jobOptions are the default pipeline's options for files in dir, published
// to store, with every scanner configured.
func jobOptions(t *testing.T, dir string, store *memoryStore) Options {
	return Options{
		Files:       datasources.CouponFiles(dir),
		BucketDir:   t.TempDir(),
		ClickHouse:  ClickHouseOptions{URL: "clickhouse://localhost:9000/coupons", Dir: "coupons"},
		PebbleDir:   t.TempDir(),
		MemoryLimit: MinMemoryLimit,
		Store:       store,
		Settings:    store,
		Locker:      &memoryLock{},
		Logger:      slog.New(slog.DiscardHandler),
	}
}

func newJob(t *testing.T, dir string, store *memoryStore) *Job {
	t.Helper()
	job, err := NewDefault(jobOptions(t, dir, store))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { job.Close() })
	return job
}

func runOnce(t *testing.T, job *Job, want Outcome) Result {
	t.Helper()
	result, err := job.RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != want {
		t.Fatalf("RunOnce = %s, want %s", result.Outcome, want)
	}
	return result
}

func TestJobBuildsOnlyWhenFilesChange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\nFIFTYOFF\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\n")
	writeFile(t, dir, ".ignored", "FIFTYOFF\n")
	store := newMemoryStore()
	job := newJob(t, dir, store)

	if result := runOnce(t, job, Built); result.Codes != 1 || len(result.Files) != 2 {
		t.Errorf("result = %+v, want 1 code from 2 files", result)
	}
	if got, want := store.publishedCodes(), []string{"HAPPYHRS"}; !slices.Equal(got, want) {
		t.Fatalf("published %v, want %v", got, want)
	}
	runOnce(t, job, Unchanged)

	writeFile(t, dir, "c.txt", "FIFTYOFF\n")
	runOnce(t, job, Built)
	if got, want := store.publishedCodes(), []string{"FIFTYOFF", "HAPPYHRS"}; !slices.Equal(got, want) {
		t.Fatalf("published %v, want %v", got, want)
	}
}

func TestJobRebuildsWhenRulesChange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\nFIFTYOFF\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\n")
	store := newMemoryStore()
	job := newJob(t, dir, store)
	runOnce(t, job, Built)

	// The discount is not a rule, so it needs no build.
	store.setSettings(func(s *models.CouponSettings) { s.DiscountPercent = 25 })
	runOnce(t, job, Unchanged)

	store.setSettings(func(s *models.CouponSettings) { s.MinFiles = 1 })
	runOnce(t, job, Built)
	if got, want := store.publishedCodes(), []string{"FIFTYOFF", "HAPPYHRS"}; !slices.Equal(got, want) {
		t.Errorf("published %v, want %v", got, want)
	}
}

func TestJobSkips(t *testing.T) {
	store := newMemoryStore()
	runOnce(t, newJob(t, t.TempDir(), store), NoFiles)

	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\n")
	job := newJob(t, dir, store)
	job.cfg.Locker = &memoryLock{held: true}
	runOnce(t, job, Locked)
	if store.begun != 0 {
		t.Errorf("skipped runs began %d batches", store.begun)
	}
}

// changingReader changes a file once the scan has read it, as happens when a
// file is still being copied in.
type changingReader struct {
	couponsource.Reader
	path string
}

func (r changingReader) Read(ctx context.Context, sources []couponsource.Source, out chan<- couponsource.Chunk) error {
	err := r.Reader.Read(ctx, sources, out)
	os.WriteFile(r.path, []byte("HAPPYHRS\nFIFTYOFF\nSIXTYOFF\n"), 0o644)
	return err
}

func TestJobDiscardsBuildWhenFilesChangeDuringScan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\nFIFTYOFF\n")
	path := writeFile(t, dir, "b.txt", "HAPPYHRS\n")
	store := newMemoryStore()
	job := newJob(t, dir, store)
	reader := job.cfg.Reader
	job.cfg.Reader = changingReader{Reader: reader, path: path}

	runOnce(t, job, FilesChanged)
	if _, found, _ := store.Published(t.Context()); found || store.aborted != 1 {
		t.Fatalf("published %v, aborted %d; want nothing published and one abort", found, store.aborted)
	}

	job.cfg.Reader = reader
	runOnce(t, job, Built)
	if got, want := store.publishedCodes(), []string{"FIFTYOFF", "HAPPYHRS"}; !slices.Equal(got, want) {
		t.Errorf("published %v, want %v", got, want)
	}
}

type failingScanner struct{}

func (failingScanner) Scan(context.Context, couponsource.Reader, []couponsource.Source, couponstore.Batch) (scanner.Stats, error) {
	return scanner.Stats{}, errors.New("disk full")
}

func TestJobAbortsFailedScan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\n")
	store := newMemoryStore()
	job := newJob(t, dir, store)
	job.cfg.ScannerFor = func(models.CouponSettings) (scanner.Scanner, string, error) {
		return failingScanner{}, "rules", nil
	}
	result, err := job.RunOnce(t.Context())
	if err == nil || result.Outcome != Failed {
		t.Fatalf("RunOnce = %s, %v; want failed", result.Outcome, err)
	}
	if _, found, _ := store.Published(t.Context()); found || store.aborted != 1 {
		t.Errorf("published %v, aborted %d; want nothing published and one abort", found, store.aborted)
	}
}

func TestJobRebuildsWhenScannerChanges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\nFIFTYOFF\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\n")
	store := newMemoryStore()
	job := newJob(t, dir, store)
	runOnce(t, job, Built)
	runOnce(t, job, Unchanged)

	// Another scanner finds the same codes, but its builds are compared, so
	// switching to it rebuilds.
	bucketsFor := job.cfg.ScannerFor
	job.cfg.ScannerFor = func(settings models.CouponSettings) (scanner.Scanner, string, error) {
		scan, _, err := bucketsFor(settings)
		return scan, Rules(settings, "other"), err
	}
	runOnce(t, job, Built)
	runOnce(t, job, Unchanged)
}

func TestNewDefaultScanners(t *testing.T) {
	store := newMemoryStore()
	settings, _ := store.Settings(t.Context())
	tests := map[string]struct {
		want     scanner.Scanner
		wantName string
	}{
		"":           {&scanner.Buckets{}, "go"},
		"go":         {&scanner.Buckets{}, "go"},
		"clickhouse": {&clickhouse.Scanner{}, "clickhouse"},
		"pebble":     {&pebble.Scanner{}, "pebble"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			opts := jobOptions(t, t.TempDir(), store)
			opts.Scanner = name
			job, err := NewDefault(opts)
			if err != nil {
				t.Fatal(err)
			}
			defer job.Close()

			// The ClickHouse connection is lazy, so no server is needed.
			scan, rules, err := job.cfg.ScannerFor(settings)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := reflect.TypeOf(scan), reflect.TypeOf(test.want); got != want {
				t.Errorf("scanner is a %v, want %v", got, want)
			}
			if want := ";scanner=" + test.wantName; !strings.HasSuffix(rules, want) {
				t.Errorf("rules %q do not end in %q", rules, want)
			}
		})
	}

	opts := jobOptions(t, t.TempDir(), store)
	opts.Scanner = "duckdb"
	if _, err := NewDefault(opts); err == nil {
		t.Error("NewDefault accepted an unknown scanner")
	}
}

// TestJobPebble runs the default pipeline with the pebble scanner, reading
// real files, and checks it publishes the codes and leaves no database
// behind.
func TestJobPebble(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\nFIFTYOFF\nSUPER100\nSUPER100\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\r\nMOODYHRS\r\n")
	writeFile(t, dir, "c.txt", "FIFTYOFF\n")
	store := newMemoryStore()
	opts := jobOptions(t, dir, store)
	opts.Scanner = pebble.Name
	job, err := NewDefault(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()

	if result := runOnce(t, job, Built); result.Scan.Scanner != pebble.Name || result.Codes != 2 {
		t.Errorf("result = %+v, want 2 codes from the pebble scanner", result)
	}
	if got, want := store.publishedCodes(), []string{"FIFTYOFF", "HAPPYHRS"}; !slices.Equal(got, want) {
		t.Errorf("published %v, want %v", got, want)
	}
	if _, err := os.Stat(opts.PebbleDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after the build, %s: %v", opts.PebbleDir, err)
	}
	runOnce(t, job, Unchanged)
}

// every runs at a fixed interval.
type every time.Duration

func (e every) Next(now time.Time) time.Time { return now.Add(time.Duration(e)) }

func TestJobRun(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\nFIFTYOFF\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\n")
	store := newMemoryStore()
	job := newJob(t, dir, store)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error)
	go func() { done <- job.Run(ctx, every(10*time.Millisecond)) }()

	waitForCodes(t, store, []string{"HAPPYHRS"})
	writeFile(t, dir, "c.txt", "FIFTYOFF\n")
	waitForCodes(t, store, []string{"FIFTYOFF", "HAPPYHRS"})

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
}

func waitForCodes(t *testing.T, store *memoryStore, want []string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !slices.Equal(store.publishedCodes(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("published %v, want %v", store.publishedCodes(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestJobDatabase runs the default pipeline against the real data stores.
func TestJobDatabase(t *testing.T) {
	stores := datasourcestest.Open(t, datasources.CacheConfig{})
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\nFIFTYOFF\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\n")
	job, err := NewDefault(Options{
		Files:       datasources.CouponFiles(dir),
		BucketDir:   t.TempDir(),
		MemoryLimit: MinMemoryLimit,
		Store:       stores.Coupons,
		Settings:    stores.CouponSettings,
		Locker:      stores.BuildLock,
		Logger:      slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	runOnce(t, job, Built)
	runOnce(t, job, Unchanged)
	for code, want := range map[string]bool{"HAPPYHRS": true, "FIFTYOFF": false} {
		if valid, published, err := stores.CouponLookup.Contains(t.Context(), code); err != nil || !published || valid != want {
			t.Errorf("Contains(%s) = %v, %v, %v; want %v", code, valid, published, err, want)
		}
	}
}

func TestNewDefaultRejectsSmallMemoryLimit(t *testing.T) {
	store := newMemoryStore()
	_, err := NewDefault(Options{
		Files:       datasources.CouponFiles(t.TempDir()),
		BucketDir:   t.TempDir(),
		MemoryLimit: MinMemoryLimit - 1,
		Store:       store,
		Settings:    store,
		Locker:      &memoryLock{},
	})
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
// COUPONS_MEMORY_LIMIT, or 256 MiB, and fails if memory goes over it. It uses
// the scanner COUPONS_SCANNER names, go or pebble, defaulting to go. Bucket
// files go to COUPONS_BUCKET_DIR and the Pebble database to
// COUPONS_PEBBLE_DIR, or to temporary folders. Run it with:
//
//	go test -v -run TestBaseFiles ./pkg/services/coupons
//	COUPONS_SCANNER=pebble go test -v -run TestBaseFiles ./pkg/services/coupons
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
	reader := datasources.CouponFiles(dir)(budget.Reader)
	sources, err := reader.List(t.Context())
	if err != nil || len(sources) < 3 {
		t.Skipf("copy couponbase1, couponbase2 and couponbase3 (.txt or .gz) into %s/ to run this test", dir)
	}
	codes, err := codec.NewAlphanumeric(8, 10)
	if err != nil {
		t.Fatal(err)
	}
	var scan scanner.Scanner
	switch name := cmp.Or(os.Getenv("COUPONS_SCANNER"), scanner.BucketsName); name {
	case scanner.BucketsName:
		scan, err = scanner.NewBuckets(scanner.BucketOptions{Codec: codes, MinFiles: 2, Dir: bucketDir, Budget: budget.Scanner})
	case pebble.Name:
		pebbleDir := cmp.Or(os.Getenv("COUPONS_PEBBLE_DIR"), filepath.Join(t.TempDir(), "pebble"))
		scan, err = pebble.New(pebble.Options{Dir: pebbleDir, Codec: codes, MinFiles: 2, Budget: budget.Scanner})
	default:
		t.Skipf("COUPONS_SCANNER=%s is not supported by this test", name)
	}
	if err != nil {
		t.Fatal(err)
	}

	defer debug.SetMemoryLimit(debug.SetMemoryLimit(int64(limit)))
	debug.FreeOSMemory()
	peak := startMemorySampler()
	var found collected
	stats, err := scan.Scan(t.Context(), reader, sources, &found)
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
type collected struct {
	mu    sync.Mutex
	codes []string
}

func (c *collected) Add(code string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.codes = append(c.codes, code)
	return nil
}

func (c *collected) Abort() {}

func (c *collected) Commit(context.Context, couponstore.Manifest) error { return nil }

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

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
