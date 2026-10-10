package clickhouse

import (
	"cmp"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/models"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner/scannertest"
)

var settings = models.CouponSettings{MinLength: 8, MaxLength: 10, MinFiles: 2}

// lazyConn connects on first use, so it needs no server until a scan.
func lazyConn(t *testing.T, url string) *Scanner {
	t.Helper()
	options, err := ch.ParseDSN(url)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := ch.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &Scanner{opts: Options{Conn: conn, Dir: "coupons", Settings: settings}}
}

func TestNewRejects(t *testing.T) {
	conn := lazyConn(t, "clickhouse://localhost:9000/coupons").opts.Conn
	tests := map[string]Options{
		"no conn":         {Dir: "coupons", Settings: settings},
		"no dir":          {Conn: conn, Settings: settings},
		"glob in dir":     {Conn: conn, Dir: "coup*", Settings: settings},
		"quote in dir":    {Conn: conn, Dir: "it's", Settings: settings},
		"no min length":   {Conn: conn, Dir: "coupons", Settings: models.CouponSettings{MaxLength: 10, MinFiles: 2}},
		"too long":        {Conn: conn, Dir: "coupons", Settings: models.CouponSettings{MinLength: 8, MaxLength: 11, MinFiles: 2}},
		"no min files":    {Conn: conn, Dir: "coupons", Settings: models.CouponSettings{MinLength: 8, MaxLength: 10}},
		"lengths swapped": {Conn: conn, Dir: "coupons", Settings: models.CouponSettings{MinLength: 9, MaxLength: 8, MinFiles: 2}},
	}
	for name, opts := range tests {
		if _, err := New(opts); err == nil {
			t.Errorf("%s: New accepted %+v", name, opts)
		}
	}
}

func TestPath(t *testing.T) {
	scan := lazyConn(t, "clickhouse://localhost:9000/coupons")
	sources := func(names ...string) []couponsource.Source {
		var out []couponsource.Source
		for _, name := range names {
			out = append(out, scannertest.File{Name: name})
		}
		return out
	}

	tests := map[string]string{
		"couponbase1.gz":                "coupons/couponbase1.gz",
		"couponbase1.gz couponbase2.gz": "coupons/{couponbase1.gz,couponbase2.gz}",
	}
	for names, want := range tests {
		got, err := scan.path(sources(strings.Fields(names)...))
		if err != nil || got != want {
			t.Errorf("path(%s) = %q, %v; want %q", names, got, err, want)
		}
	}
	for _, name := range []string{"a,b", "a{1}", "a*", "a?", "it's", `a\b`} {
		if _, err := scan.path(sources("ok.txt", name)); err == nil {
			t.Errorf("path accepted a file named %q", name)
		}
	}
}

// TestScanServer scans files with a real server. It needs TEST_CLICKHOUSE_URL
// and TEST_CLICKHOUSE_DIR, a local folder the server sees as
// user_files/TEST_CLICKHOUSE_SERVER_DIR (default: test). For example, with
// the folder mounted into a ClickHouse container at
// /var/lib/clickhouse/user_files/test:
//
//	TEST_CLICKHOUSE_URL=clickhouse://kart:kart@localhost:9000/coupons \
//	TEST_CLICKHOUSE_DIR=/tmp/chtest go test -run TestScanServer ./pkg/services/coupons/scanner/clickhouse
func TestScanServer(t *testing.T) {
	url, localDir := os.Getenv("TEST_CLICKHOUSE_URL"), os.Getenv("TEST_CLICKHOUSE_DIR")
	if url == "" || localDir == "" {
		t.Skip("set TEST_CLICKHOUSE_URL and TEST_CLICKHOUSE_DIR to scan with a ClickHouse server")
	}
	serverDir := cmp.Or(os.Getenv("TEST_CLICKHOUSE_SERVER_DIR"), "test")
	conn := lazyConn(t, url).opts.Conn

	// scan writes sources into a fresh subfolder and scans them there.
	scan := func(t *testing.T, minFiles int, sources []couponsource.Source) []string {
		t.Helper()
		sub := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
		dir := filepath.Join(localDir, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		for _, source := range sources {
			file := source.(scannertest.File)
			if err := os.WriteFile(filepath.Join(dir, file.Name), []byte(file.Data), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		rules := settings
		rules.MinFiles = minFiles
		scanner, err := New(Options{Conn: conn, Dir: serverDir + "/" + sub, Settings: rules})
		if err != nil {
			t.Fatal(err)
		}
		var out scannertest.Collected
		stats, err := scanner.Scan(t.Context(), nil, sources, &out)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Scanner != Name || stats.Codes != len(out.Codes) {
			t.Errorf("stats = %+v, with %d codes found", stats, len(out.Codes))
		}
		slices.Sort(out.Codes)
		return out.Codes
	}

	t.Run("matches map count", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(1, 2))
		for round := range 3 {
			sources, counts := scannertest.RandomSources(rng, 2+rng.IntN(5))
			for _, minFiles := range []int{2, 3} {
				t.Run(fmt.Sprintf("round%d/min%d", round, minFiles), func(t *testing.T) {
					var want []string
					for code, files := range counts {
						if files >= minFiles {
							want = append(want, code)
						}
					}
					slices.Sort(want)
					if got := scan(t, minFiles, sources); !slices.Equal(got, want) {
						t.Fatalf("Scan = %v, want %v", got, want)
					}
				})
			}
		}
	})

	t.Run("suite", func(t *testing.T) {
		scannertest.Run(t, func(t *testing.T, minFiles int, sources []couponsource.Source, out couponstore.Batch) error {
			sub := strings.NewReplacer("/", "_", " ", "_", ",", "_").Replace(t.Name())
			dir := filepath.Join(localDir, sub)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			for _, source := range sources {
				file := source.(scannertest.File)
				if err := os.WriteFile(filepath.Join(dir, file.Name), []byte(file.Data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			rules := settings
			rules.MinFiles = minFiles
			scanner, err := New(Options{Conn: conn, Dir: serverDir + "/" + sub, Settings: rules})
			if err != nil {
				t.Fatal(err)
			}
			_, err = scanner.Scan(t.Context(), nil, sources, out)
			return err
		})
	})

	t.Run("repeated within a file", func(t *testing.T) {
		got := scan(t, 2, []couponsource.Source{
			scannertest.File{Name: "a.txt", Data: "SUPER100\nSUPER100\nHAPPYHRS\r\n"},
			scannertest.File{Name: "b.txt", Data: "HAPPYHRS\nSHORT77\nTOOLONGCODE1\n"},
		})
		if want := []string{"HAPPYHRS"}; !slices.Equal(got, want) {
			t.Errorf("Scan = %v, want %v", got, want)
		}
	})
}
