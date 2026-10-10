package files

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
)

// TestReadChunksBoundaries feeds lines of every length through buffers of
// many sizes, so every line lands across a buffer boundary at some point. A
// line is sent whole when it fits in a buffer with its newline, and skipped
// otherwise.
func TestReadChunksBoundaries(t *testing.T) {
	var lines []string
	for length := range 40 {
		lines = append(lines, strings.Repeat(string(rune('A'+length%26)), length))
	}
	lines = append(lines, "WITHCR\r", "LAST") // CRLF passes through; the last line has no newline
	input := strings.Join(lines, "\n")

	for size := 8; size <= 48; size++ {
		got := readAll(t, strings.NewReader(input), size)
		var want []string
		for _, line := range lines {
			if len(line) < size {
				want = append(want, line)
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("buffer size %d: lines = %q, want %q", size, got, want)
		}
	}
}

// readAll runs readChunks with a pool of two buffers of size bytes and
// returns the lines it sends.
func readAll(t *testing.T, r io.Reader, size int) []string {
	t.Helper()
	pool := make(chan []byte, 2)
	pool <- make([]byte, size)
	pool <- make([]byte, size)
	out := make(chan couponsource.Chunk)
	var err error
	go func() {
		err = readChunks(t.Context(), r, 0, pool, out)
		close(out)
	}()
	var lines []string
	for chunk := range out {
		for line := range strings.SplitSeq(strings.TrimSuffix(string(chunk.Lines), "\n"), "\n") {
			lines = append(lines, line)
		}
		chunk.Release()
	}
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestFSListAndRead(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", []byte("HAPPYHRS\nSUPER100\n"))
	// Two gzip members back to back: a multistream file.
	writeFile(t, dir, "b.gz", append(gzipped(t, "FIFTYOFF\n"), gzipped(t, "HAPPYHRS\n")...))
	writeFile(t, dir, ".hidden", []byte("NOPE1234\n"))
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}

	fs := NewFS(dir, 1<<20, NewGzip())
	sources, err := fs.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := names(sources); !slices.Equal(got, []string{"a.txt", "b.gz"}) {
		t.Fatalf("List = %v, want [a.txt b.gz]", got)
	}
	if size := sources[0].EstimatedSize(); size != sources[0].Info().Size {
		t.Errorf("EstimatedSize of text = %d, want %d", size, sources[0].Info().Size)
	}

	got := readSources(t, fs, sources)
	want := map[int]string{0: "HAPPYHRS\nSUPER100\n", 1: "FIFTYOFF\nHAPPYHRS\n"}
	if got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Read = %v, want %v", got, want)
	}
}

func TestGzipEstimatedSize(t *testing.T) {
	dir := t.TempDir()
	text := strings.Repeat("HAPPYHRS\n", 1000)
	writeFile(t, dir, "a.gz", gzipped(t, text))
	sources, err := NewFS(dir, 1<<20, NewGzip()).List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if size := sources[0].EstimatedSize(); size != int64(len(text)) {
		t.Errorf("EstimatedSize = %d, want %d", size, len(text))
	}
}

func TestReadErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bad.gz", []byte{0x1f, 0x8b, 0x08, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3}) // gzip header, broken body
	writeFile(t, dir, "good.txt", []byte(strings.Repeat("HAPPYHRS\n", 100_000)))
	fs := NewFS(dir, 1<<20, NewGzip())
	sources, err := fs.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan couponsource.Chunk)
	done := make(chan error)
	go func() { done <- fs.Read(t.Context(), sources, out) }()
	go func() {
		for chunk := range out {
			chunk.Release()
		}
	}()
	err = <-done
	close(out)
	if err == nil || !strings.Contains(err.Error(), "bad.gz") {
		t.Errorf("Read = %v, want an error naming bad.gz", err)
	}
}

func TestReadCancelled(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", []byte(strings.Repeat("HAPPYHRS\n", 100_000)))
	fs := NewFS(dir, 1<<20)
	sources, err := fs.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// Nobody receives from out: Read must still return.
	if err := fs.Read(ctx, sources, make(chan couponsource.Chunk)); !errors.Is(err, context.Canceled) {
		t.Errorf("Read = %v, want context.Canceled", err)
	}
}

// TestPlanStaysInBudget checks that the decompressors and the chunk pool
// together never exceed the reader's memory, at any number of sources.
func TestPlanStaysInBudget(t *testing.T) {
	for _, memory := range []int64{1 << 20, 12 << 20, 48 << 20, 1 << 30} {
		fs := NewFS(t.TempDir(), memory)
		for _, sources := range []int{1, 3, 20} {
			plan := fs.plan(sources)
			used := plan.decompressorMemory*int64(plan.concurrent) + int64(plan.buffers*plan.chunkSize)
			if plan.buffers < 2*plan.concurrent {
				t.Errorf("memory %d, %d sources: %d buffers for %d readers", memory, sources, plan.buffers, plan.concurrent)
			}
			// Small budgets round up to the minimum chunk size.
			if used > memory && plan.chunkSize > minChunkSize {
				t.Errorf("memory %d, %d sources: plan %+v uses %d", memory, sources, plan, used)
			}
			if fs.ChunkSize(sources) != plan.chunkSize {
				t.Errorf("ChunkSize disagrees with the plan")
			}
		}
	}
}

func readSources(t *testing.T, reader couponsource.Reader, sources []couponsource.Source) map[int]string {
	t.Helper()
	out := make(chan couponsource.Chunk)
	var mu sync.Mutex
	got := map[int]string{}
	go func() {
		defer close(out)
		if err := reader.Read(t.Context(), sources, out); err != nil {
			t.Error(err)
		}
	}()
	for chunk := range out {
		mu.Lock()
		got[chunk.Source] += string(chunk.Lines)
		mu.Unlock()
		chunk.Release()
	}
	return got
}

func names(sources []couponsource.Source) []string {
	var names []string
	for _, source := range sources {
		names = append(names, source.Info().Name)
	}
	return names
}

func gzipped(t *testing.T, content string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	if _, err := zw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func writeFile(t *testing.T, dir, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
}
