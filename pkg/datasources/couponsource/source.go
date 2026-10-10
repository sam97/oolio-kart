// Package couponsource defines how coupon base files are listed and
// streamed in chunks of lines. datasources.CouponFiles opens a folder of them.
package couponsource

import (
	"context"
	"io"
	"time"
)

// Info identifies one version of a source. Two scans of the same files see
// equal Infos, so a change to any of them means the files changed.
type Info struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

func (info Info) Equal(other Info) bool {
	return info.Name == other.Name && info.Size == other.Size && info.ModTime.Equal(other.ModTime)
}

// SameInfos reports whether two listings describe the same versions of the
// same sources, in the same order.
func SameInfos(a, b []Info) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

// Infos returns the Info of each source.
func Infos(sources []Source) []Info {
	infos := make([]Info, len(sources))
	for i, source := range sources {
		infos[i] = source.Info()
	}
	return infos
}

// Source is one coupon base file.
type Source interface {
	Info() Info

	// Open returns the raw, possibly compressed, contents. Seeking lets the
	// format be detected from the first bytes before reading from the start.
	Open() (io.ReadSeekCloser, error)

	// EstimatedSize is the decompressed size in bytes, or 0 if unknown. It
	// only sizes buffers: a wrong guess costs speed, never correctness.
	EstimatedSize() int64
}

// Chunk is a run of whole lines from one source. Lines stays valid until
// Release, which hands the buffer back to the reader for reuse.
type Chunk struct {
	// Source is the index of the source in the slice given to Read.
	Source int
	Lines  []byte

	release func()
}

// NewChunk returns a chunk of lines from source; release hands its buffer
// back to the reader.
func NewChunk(source int, lines []byte, release func()) Chunk {
	return Chunk{Source: source, Lines: lines, release: release}
}

func (c Chunk) Release() {
	if c.release != nil {
		c.release()
	}
}

// Reader lists sources and streams their lines.
type Reader interface {
	// List returns the current sources, sorted by name.
	List(ctx context.Context) ([]Source, error)

	// Read streams every source into out and returns once all are read.
	// The caller closes out, and must Release every chunk it receives, or
	// Read runs out of buffers and stalls.
	Read(ctx context.Context, sources []Source, out chan<- Chunk) error

	// ChunkSize is the largest Lines that Read sends when reading this many
	// sources, so consumers can size their own buffers.
	ChunkSize(sources int) int
}

// Opener opens a Reader that may hold about memory bytes of buffers.
type Opener func(memory int64) Reader
