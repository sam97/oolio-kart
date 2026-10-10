package coupons

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// startService runs a Service on dir and returns its load results. stop
// cancels it and waits for Run to return, so no write is still in flight.
func startService(t *testing.T, dir string, poll time.Duration) (service *Service, loads <-chan LoadResult, stop func()) {
	t.Helper()
	results := make(chan LoadResult, 10)
	service = New(Config{
		Dir:          dir,
		PollInterval: poll,
		Logger:       slog.New(slog.DiscardHandler),
		OnLoad:       func(result LoadResult) { results <- result },
	})
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

func TestServiceRestoresSavedCodes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\nFIFTYOFF\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\n")

	_, loads, stop := startService(t, dir, 10*time.Millisecond)
	if result := waitLoad(t, loads); result.Restored {
		t.Fatal("first load was restored, want built")
	}
	stop()
	if _, err := os.Stat(filepath.Join(dir, SavedName)); err != nil {
		t.Fatalf("saved file not written: %v", err)
	}

	// With an hour between polls, only a restore can load the codes in time.
	service, loads, _ := startService(t, dir, time.Hour)
	select {
	case result := <-loads:
		if !result.Restored || result.Codes != 1 {
			t.Errorf("load = %+v, want 1 restored code", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("saved codes were not restored on start")
	}
	if got, want := service.ValidCoupons(), []string{"HAPPYHRS"}; !slices.Equal(got, want) {
		t.Errorf("ValidCoupons = %v, want %v", got, want)
	}
	if !service.IsValid("HAPPYHRS") || service.IsValid("FIFTYOFF") {
		t.Error("IsValid disagrees with the restored codes")
	}
}

func TestServiceRebuildsWhenFilesChange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\nFIFTYOFF\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\n")

	_, loads, stop := startService(t, dir, 10*time.Millisecond)
	waitLoad(t, loads)
	stop()

	writeFile(t, dir, "b.txt", "HAPPYHRS\nFIFTYOFF\n")
	service, loads, _ := startService(t, dir, 10*time.Millisecond)
	if result := waitLoad(t, loads); result.Restored {
		t.Fatal("restored codes built from older files")
	}
	if got, want := service.ValidCoupons(), []string{"FIFTYOFF", "HAPPYHRS"}; !slices.Equal(got, want) {
		t.Errorf("ValidCoupons = %v, want %v", got, want)
	}
}

func TestServiceIgnoresCorruptSavedFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "HAPPYHRS\n")
	writeFile(t, dir, "b.txt", "HAPPYHRS\n")
	writeFile(t, dir, SavedName, "{not json")

	service, loads, stop := startService(t, dir, 10*time.Millisecond)
	if result := waitLoad(t, loads); result.Restored {
		t.Fatal("restored a corrupt saved file")
	}
	if !service.IsValid("HAPPYHRS") {
		t.Error("HAPPYHRS should be valid")
	}
	stop()

	// The rebuild replaces the corrupt file with a usable one.
	if _, err := readSaved(dir); err != nil {
		t.Errorf("saved file after rebuild: %v", err)
	}
}

func TestSavedCodesMatches(t *testing.T) {
	snap := snapshot{
		filepath.Join("data", "a.txt"): {size: 10, modTime: 1},
		filepath.Join("data", "b.txt"): {size: 20, modTime: 2},
	}
	saved := newSavedCodes(snap, []string{"HAPPYHRS"})

	// The folder may be mounted at another path; only base names count.
	moved := snapshot{
		filepath.Join("mnt", "a.txt"): {size: 10, modTime: 1},
		filepath.Join("mnt", "b.txt"): {size: 20, modTime: 2},
	}
	if !saved.matches(moved) {
		t.Error("saved codes should match the same files in another folder")
	}

	tests := map[string]func(*savedCodes){
		"changed size":     func(s *savedCodes) { s.Files["a.txt"] = savedFile{Size: 11, ModTime: 1} },
		"changed mod time": func(s *savedCodes) { s.Files["a.txt"] = savedFile{Size: 10, ModTime: 9} },
		"extra file":       func(s *savedCodes) { s.Files["c.txt"] = savedFile{} },
		"missing file":     func(s *savedCodes) { delete(s.Files, "b.txt") },
		"other min files":  func(s *savedCodes) { s.MinFiles++ },
		"other max length": func(s *savedCodes) { s.MaxLength-- },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			changed := newSavedCodes(snap, []string{"HAPPYHRS"})
			change(&changed)
			if changed.matches(snap) {
				t.Error("matches = true, want false")
			}
		})
	}
}

func TestReadSavedRejectsBadCodes(t *testing.T) {
	tests := map[string][]string{
		"unsorted":  {"HAPPYHRS", "FIFTYOFF"},
		"duplicate": {"HAPPYHRS", "HAPPYHRS"},
		"too short": {"SHORT"},
		"bad char":  {"HAPPY-HRS"},
	}
	for name, codes := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := writeSaved(dir, savedCodes{Codes: codes}); err != nil {
				t.Fatal(err)
			}
			if _, err := readSaved(dir); err == nil {
				t.Errorf("readSaved accepted %v", codes)
			}
		})
	}
}
