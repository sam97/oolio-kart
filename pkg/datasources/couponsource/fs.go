package couponsource

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// FS reads the coupon base files in one folder: every regular file whose
// name does not start with a dot.
type FS struct {
	dir           string
	memory        int64
	decompressors []Decompressor
}

// NewFS reads dir using at most about memory bytes for decompression and
// chunk buffers. Files that match none of decompressors are read as text.
func NewFS(dir string, memory int64, decompressors ...Decompressor) *FS {
	return &FS{dir: dir, memory: memory, decompressors: decompressors}
}

func (fs *FS) List(ctx context.Context) ([]Source, error) {
	entries, err := os.ReadDir(fs.dir) // sorted by name
	if err != nil {
		return nil, err
	}
	var sources []Source
	for _, entry := range entries {
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if os.IsNotExist(err) { // removed since ReadDir
			continue
		}
		if err != nil {
			return nil, err
		}
		sources = append(sources, &file{
			path:          filepath.Join(fs.dir, entry.Name()),
			info:          Info{Name: entry.Name(), Size: info.Size(), ModTime: info.ModTime()},
			decompressors: fs.decompressors,
		})
	}
	return sources, nil
}

func (fs *FS) ChunkSize(sources int) int {
	return fs.plan(sources).chunkSize
}

// readPlan is how Read spends its memory.
type readPlan struct {
	concurrent         int   // sources read at once
	decompressorMemory int64 // per source being read
	chunkSize          int
	buffers            int
}

const (
	minChunkSize = 64 << 10
	maxChunkSize = 1 << 20
)

// plan splits the reader's memory between decompressors and a fixed pool of
// chunk buffers. Each reading source and each consumer, taken as one per
// core, can hold a buffer, and two buffers each keep both sides busy:
//
//	memory ─┬─ ¼ decompressors, split between the sources read at once
//	        └─ ¾ chunk pool = buffers × chunkSize
//	             chunkSize = pool / (2 × (sources read at once + cores))
//
// Ex: 48 MiB, 3 sources, 16 cores
//
//	decompressors 12 MiB  →  4 MiB per source
//	chunk pool    36 MiB  →  chunkSize = 36 MiB / 38 ≈ 970 KiB, 38 buffers
func (fs *FS) plan(sources int) readPlan {
	cores := runtime.GOMAXPROCS(0)
	// Reading is CPU-bound, so readers beyond one per core only share cores
	// while each holds its own buffers.
	concurrent := min(max(sources, 1), cores)
	pool := fs.memory * 3 / 4
	// Each reader fills one buffer and each consumer, assumed one per core,
	// encodes one; ×2 leaves a spare for each, so neither side waits.
	chunkSize := min(max(pool/int64(2*(concurrent+cores)), minChunkSize), maxChunkSize)
	return readPlan{
		concurrent:         concurrent,
		decompressorMemory: fs.memory / 4 / int64(concurrent),
		chunkSize:          int(chunkSize),
		// Never fewer than two per reader, even when the clamp leaves the
		// pool too small for that.
		buffers: max(int(pool/chunkSize), 2*concurrent),
	}
}

// Read streams the sources with one goroutine each, of which at most one per
// core reads at a time. The first error cancels the rest.
func (fs *FS) Read(ctx context.Context, sources []Source, out chan<- Chunk) error {
	plan := fs.plan(len(sources))
	// The pool is the only source of chunk buffers, so it bounds the input
	// in flight. Its capacity fits every buffer, so releasing never blocks.
	pool := make(chan []byte, plan.buffers)
	for range plan.buffers {
		pool <- make([]byte, plan.chunkSize)
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// reading is a semaphore: a slot per reader allowed to run at once.
	reading := make(chan struct{}, plan.concurrent)
	var wg sync.WaitGroup
	for index, source := range sources {
		wg.Go(func() {
			select {
			case reading <- struct{}{}:
			case <-ctx.Done(): // cancelled while waiting for a slot
				return
			}
			defer func() { <-reading }()
			if err := fs.readSource(ctx, index, source, plan, pool, out); err != nil {
				cancel(fmt.Errorf("%s: %w", source.Info().Name, err))
			}
		})
	}
	wg.Wait()
	// The first reader's error, the parent's cancellation, or nil.
	return context.Cause(ctx)
}

func (fs *FS) readSource(ctx context.Context, index int, source Source, plan readPlan, pool chan []byte, out chan<- Chunk) error {
	raw, err := source.Open()
	if err != nil {
		return err
	}
	defer raw.Close()

	decompressor, err := NewDecompressorFor(raw, fs.decompressors)
	if err != nil {
		return err
	}
	// Plain text needs no buffering of its own: readChunks reads straight
	// into large chunk buffers.
	var text io.Reader = raw
	if decompressor != nil {
		decompressed, err := decompressor.NewReader(raw, plan.decompressorMemory)
		if err != nil {
			return fmt.Errorf("%s: %w", decompressor.Name(), err)
		}
		defer decompressed.Close()
		text = decompressed
	}
	return readChunks(ctx, text, index, pool, out)
}

// readChunks fills pooled buffers from r and sends the whole lines in each.
// The partial line at the end of a buffer is carried to the start of the
// next:
//
//	buffer 1: "HAPPYHRS\nFIFTYOFF\nSIXT"           sends "HAPPYHRS\nFIFTYOFF\n", carries "SIXT"
//	buffer 2: "SIXT" + "YOFF\nGNULINUX\nBIRT"      sends "SIXTYOFF\nGNULINUX\n", carries "BIRT"
//
// A line that fills a whole buffer cannot be sent whole, so it is skipped up
// to its newline. At the end of the input, the last line needs no newline.
func readChunks(ctx context.Context, r io.Reader, source int, pool chan []byte, out chan<- Chunk) error {
	var carry []byte
	skipping := false // inside a line too long for a buffer
	for {
		var buffer []byte
		select {
		case buffer = <-pool:
		case <-ctx.Done():
			return ctx.Err()
		}
		// The previous buffer's partial line goes first, then fresh input.
		carried := copy(buffer, carry)
		read, done, err := fill(r, buffer[carried:])
		if err != nil {
			pool <- buffer
			return err
		}
		data := buffer[:carried+read]

		// Drop the rest of an over-long line, up to and including its
		// newline.
		start := 0
		if skipping {
			newline := bytes.IndexByte(data, '\n')
			if newline < 0 { // the whole buffer is still inside that line
				pool <- buffer
				if done {
					return nil
				}
				continue
			}
			start, skipping = newline+1, false
		}

		// Send up to the last newline. carry has its own backing array,
		// because buffer goes out with the chunk.
		end := len(data)
		carry = carry[:0]
		if !done {
			end = start
			if last := bytes.LastIndexByte(data[start:], '\n'); last >= 0 {
				end = start + last + 1
			}
			// A newline-free tail that fills the buffer would fill the next
			// one too and never make progress, so skip that line instead.
			if tail := data[end:]; len(tail) == len(buffer) {
				skipping = true
			} else {
				carry = append(carry, tail...)
			}
		}

		// The consumer returns the buffer through Release; otherwise it
		// goes straight back to the pool.
		if end > start {
			chunk := Chunk{Source: source, Lines: data[start:end], release: func() { pool <- buffer }}
			select {
			case out <- chunk:
			case <-ctx.Done():
				pool <- buffer
				return ctx.Err()
			}
		} else {
			pool <- buffer
		}
		if done {
			return nil
		}
	}
}

// fill reads into buffer until it is full or r ends, and reports whether r
// ended. Unlike io.ReadFull, it never mistakes a decompressor's
// io.ErrUnexpectedEOF, a truncated file, for the end of the input.
func fill(r io.Reader, buffer []byte) (n int, done bool, err error) {
	for n < len(buffer) {
		read, err := r.Read(buffer[n:])
		n += read
		if err == io.EOF {
			return n, true, nil
		}
		if err != nil {
			return n, false, err
		}
	}
	return n, false, nil
}

// file is a Source in an FS folder.
type file struct {
	path          string
	info          Info
	decompressors []Decompressor
}

func (f *file) Info() Info { return f.info }

func (f *file) Open() (io.ReadSeekCloser, error) { return os.Open(f.path) }

func (f *file) EstimatedSize() int64 {
	opened, err := os.Open(f.path)
	if err != nil {
		return 0
	}
	defer opened.Close()

	if decompressor, err := NewDecompressorFor(opened, f.decompressors); err == nil && decompressor != nil {
		return decompressor.DecompressedSize(opened, f.info.Size)
	}
	return f.info.Size
}
