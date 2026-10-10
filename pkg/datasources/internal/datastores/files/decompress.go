package files

import (
	"bytes"
	"encoding/binary"
	"io"

	"github.com/klauspost/pgzip"
)

// Decompressor is a compressed format, recognised by its leading magic bytes
// rather than by a file extension.
type Decompressor interface {
	Name() string

	// Match reports whether header, the first bytes of a file, starts this
	// format. header is at most HeaderSize bytes and may be shorter.
	Match(header []byte) bool

	// NewReader decompresses r, holding at most about memory bytes of
	// buffers.
	NewReader(r io.Reader, memory int64) (io.ReadCloser, error)

	// DecompressedSize estimates the decompressed size of a whole
	// compressed file of the given size.
	DecompressedSize(file io.ReaderAt, size int64) int64

	// SetParallelism sets how many goroutines may work on one stream.
	SetParallelism(n int)
}

// HeaderSize is how many leading bytes are offered to Match.
const HeaderSize = 16

// NewDecompressorFor returns the decompressor among decompressors whose
// magic bytes start file, or nil for plain text. It reads the first
// HeaderSize bytes, then seeks file back to the start.
func NewDecompressorFor(file io.ReadSeeker, decompressors []Decompressor) (Decompressor, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	header := make([]byte, HeaderSize)
	n, err := io.ReadFull(file, header)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF { // a short file just matches nothing
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	for _, d := range decompressors {
		if d.Match(header[:n]) {
			return d, nil
		}
	}
	return nil, nil
}

// Gzip decompresses gzip, including multistream files, with
// github.com/klauspost/pgzip. Inflating one stream is sequential; pgzip
// reads ahead and checks the CRC on other goroutines, so the consumer rarely
// waits on I/O.
type Gzip struct {
	blocks int
}

const (
	defaultGzipBlocks = 4
	minGzipBlock      = 64 << 10
	maxGzipBlock      = 1 << 20
)

func NewGzip() *Gzip {
	return &Gzip{blocks: defaultGzipBlocks}
}

func (g *Gzip) Name() string { return "gzip" }

func (g *Gzip) Match(header []byte) bool {
	return bytes.HasPrefix(header, []byte{0x1f, 0x8b})
}

// SetParallelism sets how many blocks pgzip decompresses ahead of the
// reader. Call it before reading starts.
func (g *Gzip) SetParallelism(n int) {
	g.blocks = max(n, 1)
}

// NewReader splits memory into read-ahead blocks: blocks × blockSize ≈
// memory, with blockSize kept between 64 KiB and 1 MiB.
//
// Ex: memory = 4 MiB, 4 blocks  →  4 × 1 MiB
//
//	memory = 1 MiB, 4 blocks  →  4 × 256 KiB
func (g *Gzip) NewReader(r io.Reader, memory int64) (io.ReadCloser, error) {
	blockSize := min(max(memory/int64(g.blocks), minGzipBlock), maxGzipBlock)
	return pgzip.NewReaderN(r, int(blockSize), g.blocks)
}

// DecompressedSize reads the ISIZE trailer, which is the decompressed size
// modulo 2^32. A value below the compressed size means it wrapped, so the
// compressed size is the better guess.
func (g *Gzip) DecompressedSize(file io.ReaderAt, size int64) int64 {
	var trailer [4]byte
	if size < int64(len(trailer)) {
		return size
	}
	if _, err := file.ReadAt(trailer[:], size-int64(len(trailer))); err != nil {
		return size
	}
	return max(int64(binary.LittleEndian.Uint32(trailer[:])), size)
}
