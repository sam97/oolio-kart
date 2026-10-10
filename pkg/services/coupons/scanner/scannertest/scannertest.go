// Package scannertest has test helpers shared by the scanner
// implementations.
package scannertest

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
)

// File is a source held in memory.
type File struct {
	Name string
	Data string
}

func (f File) Info() couponsource.Info {
	return couponsource.Info{Name: f.Name, Size: int64(len(f.Data)), ModTime: time.Unix(1, 0)}
}

func (f File) Open() (io.ReadSeekCloser, error) {
	return nopCloser{strings.NewReader(f.Data)}, nil
}

func (f File) EstimatedSize() int64 { return int64(len(f.Data)) }

type nopCloser struct{ io.ReadSeeker }

func (nopCloser) Close() error { return nil }

// Reader is a couponsource.Reader of Files. It sends each in chunks of a few
// lines, so chunks from different sources interleave. Files over 1 MiB go in
// bigger chunks, to keep stress tests quick.
type Reader struct{ Sources []couponsource.Source }

func (r Reader) List(ctx context.Context) ([]couponsource.Source, error) { return r.Sources, nil }
func (r Reader) ChunkSize(sources int) int                               { return 64 << 10 }

func (r Reader) Read(ctx context.Context, sources []couponsource.Source, out chan<- couponsource.Chunk) error {
	for i, source := range sources {
		data := source.(File).Data
		perChunk := 7
		if len(data) > 1<<20 {
			perChunk = 8192
		}
		lines := strings.SplitAfter(data, "\n")
		for len(lines) > 0 {
			n := min(len(lines), perChunk)
			select {
			case out <- couponsource.Chunk{Source: i, Lines: []byte(strings.Join(lines[:n], ""))}:
			case <-ctx.Done():
				return ctx.Err()
			}
			lines = lines[n:]
		}
	}
	return nil
}

// Collected is a couponstore.Batch that keeps codes in memory.
type Collected struct {
	mu    sync.Mutex
	Codes []string
}

func (c *Collected) Add(code string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Codes = append(c.Codes, code)
	return nil
}

func (c *Collected) Abort() {}

func (c *Collected) Commit(context.Context, couponstore.Manifest) error { return nil }

// RandomSources returns n Files drawn from a small pool of 8 to 10 character
// codes, so codes overlap across sources and repeat within them, mixed with
// lines that are not codes. counts maps each code to the number of sources it
// appears in.
func RandomSources(rng *rand.Rand, n int) ([]couponsource.Source, map[string]int) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	pool := make([]string, 300)
	for i := range pool {
		code := make([]byte, 8+rng.IntN(3))
		for j := range code {
			code[j] = alphabet[rng.IntN(len(alphabet))]
		}
		pool[i] = string(code)
	}
	junk := []string{"", "SHORT", "HAPPY-HR", "WAYTOOLONGFORACODE"}

	sources := make([]couponsource.Source, n)
	counts := map[string]int{}
	for i := range sources {
		var text strings.Builder
		seen := map[string]bool{}
		newline := "\n"
		if rng.IntN(2) == 0 {
			newline = "\r\n"
		}
		for range rng.IntN(3000) {
			if rng.IntN(10) == 0 {
				text.WriteString(junk[rng.IntN(len(junk))] + newline)
				continue
			}
			code := pool[rng.IntN(len(pool))]
			seen[code] = true
			text.WriteString(code + newline)
		}
		for code := range seen {
			counts[code]++
		}
		sources[i] = File{fmt.Sprint(i), text.String()}
	}
	return sources, counts
}
