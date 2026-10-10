package couponstore

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
)

func fingerprint() Fingerprint {
	return Fingerprint{
		Sources: []couponsource.Info{
			{Name: "a.txt", Size: 10, ModTime: time.Unix(0, 1)},
			{Name: "b.txt", Size: 20, ModTime: time.Unix(0, 2)},
		},
		Rules: "alphanumeric:8-10;minFiles=2",
	}
}

func commit(t *testing.T, store *JSONFile, fp Fingerprint, codes ...string) {
	t.Helper()
	batch, err := store.Begin(t.Context(), fp)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range codes {
		if err := batch.Add(code); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestJSONFileRoundTrip(t *testing.T) {
	store := NewJSONFile(t.TempDir())
	if _, found, err := store.Load(t.Context(), fingerprint()); found || err != nil {
		t.Fatalf("Load before any commit = %v, %v; want not found", found, err)
	}

	// Codes arrive in any order, possibly repeated.
	commit(t, store, fingerprint(), "SIXTYOFF", "HAPPYHRS", "BIRTHDAY", "HAPPYHRS")
	codes, found, err := store.Load(t.Context(), fingerprint())
	if err != nil || !found {
		t.Fatalf("Load = %v, %v", found, err)
	}
	if want := []string{"BIRTHDAY", "HAPPYHRS", "SIXTYOFF"}; !slices.Equal(codes, want) {
		t.Errorf("codes = %v, want %v", codes, want)
	}
}

func TestJSONFileOutOfDate(t *testing.T) {
	tests := map[string]func(*Fingerprint){
		"changed size":     func(fp *Fingerprint) { fp.Sources[0].Size++ },
		"changed mod time": func(fp *Fingerprint) { fp.Sources[0].ModTime = time.Unix(0, 9) },
		"renamed file":     func(fp *Fingerprint) { fp.Sources[0].Name = "c.txt" },
		"extra file":       func(fp *Fingerprint) { fp.Sources = append(fp.Sources, couponsource.Info{Name: "c.txt"}) },
		"missing file":     func(fp *Fingerprint) { fp.Sources = fp.Sources[:1] },
		"other rules":      func(fp *Fingerprint) { fp.Rules = "alphanumeric:8-10;minFiles=3" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			store := NewJSONFile(t.TempDir())
			commit(t, store, fingerprint(), "HAPPYHRS")
			changed := fingerprint()
			change(&changed)
			if _, found, err := store.Load(t.Context(), changed); found || err != nil {
				t.Errorf("Load = %v, %v; want not found", found, err)
			}
		})
	}
}

func TestJSONFileAbort(t *testing.T) {
	store := NewJSONFile(t.TempDir())
	commit(t, store, fingerprint(), "HAPPYHRS")

	batch, err := store.Begin(t.Context(), fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	batch.Add("FIFTYOFF")
	batch.Abort()

	codes, _, _ := store.Load(t.Context(), fingerprint())
	if want := []string{"HAPPYHRS"}; !slices.Equal(codes, want) {
		t.Errorf("codes after Abort = %v, want %v", codes, want)
	}
}

func TestJSONFileRejectsBadFiles(t *testing.T) {
	tests := map[string]string{
		"not json":  "{not json",
		"unsorted":  `{"codes": ["HAPPYHRS", "FIFTYOFF"]}`,
		"duplicate": `{"codes": ["HAPPYHRS", "HAPPYHRS"]}`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, found, err := NewJSONFile(dir).Load(t.Context(), fingerprint()); found || err == nil {
				t.Errorf("Load = %v, %v; want an error", found, err)
			}
		})
	}
}
