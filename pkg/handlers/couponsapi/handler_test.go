package couponsapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sam97/oolio-kart/pkg/services/coupons"
)

func get(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestHandler runs the handler against a real coupons.Service watching a
// folder of small base files.
func TestHandler(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"a.txt": "HAPPYHRS\nSUPER100\n",
		"b.txt": "HAPPYHRS\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var loaded atomic.Bool
	loads := make(chan coupons.LoadResult, 1)
	service, err := coupons.NewDefault(coupons.Settings{
		Dir:          dir,
		BucketDir:    t.TempDir(),
		MemoryLimit:  coupons.MinMemoryLimit,
		PollInterval: 10 * time.Millisecond,
		Logger:       slog.New(slog.DiscardHandler),
		OnLoad: func(result coupons.LoadResult) {
			if result.Err == nil {
				loaded.Store(true)
			}
			loads <- result
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewRouter(service, loaded.Load, 10, slog.New(slog.DiscardHandler))

	// Before the first load every lookup is unavailable, not invalid.
	if rec := get(t, handler, "/v1/coupons/HAPPYHRS"); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Errorf("lookup before load = %d %v", rec.Code, rec.Header())
	}
	if rec := get(t, handler, "/ready"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/ready before load = %d", rec.Code)
	}
	if rec := get(t, handler, "/health"); rec.Code != http.StatusOK {
		t.Errorf("/health before load = %d", rec.Code)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go service.Run(ctx)
	select {
	case result := <-loads:
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("coupons did not load")
	}

	rec := get(t, handler, "/v1/coupons/HAPPYHRS")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"code":"HAPPYHRS","discountPercent":10}` {
		t.Errorf("valid lookup = %d %s", rec.Code, rec.Body)
	}
	for _, code := range []string{"SUPER100", "happyhrs", "NOPE"} {
		if rec := get(t, handler, "/v1/coupons/"+code); rec.Code != http.StatusNotFound {
			t.Errorf("lookup %s = %d, want 404", code, rec.Code)
		}
	}
	if rec := get(t, handler, "/ready"); rec.Code != http.StatusOK {
		t.Errorf("/ready after load = %d", rec.Code)
	}
}
