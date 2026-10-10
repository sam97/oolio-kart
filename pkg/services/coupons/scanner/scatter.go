package scanner

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
)

// bucketFile is one source's part of one bucket. Encode workers append to it
// under mu during scatter; afterwards one count worker owns it.
type bucketFile struct {
	path  string
	codes int64 // written during scatter; distinct codes once counted

	mu     sync.Mutex
	file   *os.File
	writer *bufio.Writer
}

func (bf *bucketFile) append(values []uint64) error {
	bf.mu.Lock()
	defer bf.mu.Unlock()
	bf.codes += int64(len(values))
	_, err := bf.writer.Write(asBytes(values))
	return err
}

// createBucketFiles opens every bucket file, indexed [source][bucket].
func createBucketFiles(dir string, sources []couponsource.Source, plan plan) ([][]*bucketFile, error) {
	files := make([][]*bucketFile, len(sources))
	for i, source := range sources {
		sourcePath := filepath.Join(dir, sourceDir(source.Info().Name))
		if err := os.Mkdir(sourcePath, 0o755); err != nil {
			return files, errors.Join(err, closeBucketFiles(files))
		}
		files[i] = make([]*bucketFile, plan.buckets)
		for bucket := range files[i] {
			path := filepath.Join(sourcePath, bucketName(bucket))
			file, err := os.Create(path)
			if err != nil {
				return files, errors.Join(err, closeBucketFiles(files))
			}
			files[i][bucket] = &bucketFile{path: path, file: file, writer: bufio.NewWriterSize(file, plan.writerSize)}
		}
	}
	return files, nil
}

// closeBucketFiles flushes and closes every open bucket file, and drops the
// write buffers so they can be collected.
func closeBucketFiles(files [][]*bucketFile) error {
	var errs []error
	for _, source := range files {
		for _, bf := range source {
			if bf == nil || bf.file == nil {
				continue
			}
			errs = append(errs, bf.writer.Flush(), bf.file.Close())
			bf.file, bf.writer = nil, nil
		}
	}
	return errors.Join(errs...)
}

// scatter reads the sources on the reader's goroutines and encodes and
// writes their codes on plan.encoders workers. The first error cancels the
// rest.
func (b *Buckets) scatter(ctx context.Context, reader couponsource.Reader, sources []couponsource.Source, files [][]*bucketFile, plan plan) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	chunks := make(chan couponsource.Chunk, plan.encoders)
	var readErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		readErr = reader.Read(ctx, sources, chunks)
		close(chunks)
	})
	for range plan.encoders {
		wg.Go(func() {
			encoder := b.newEncoder(files, plan.buckets)
			failed := false
			for chunk := range chunks {
				// Keep draining after a failure, so the reader gets its
				// buffers back and can see the cancellation.
				if !failed {
					if err := encoder.encode(chunk); err != nil {
						cancel(err)
						failed = true
					}
				}
				chunk.Release()
			}
		})
	}
	wg.Wait()

	if err := context.Cause(ctx); err != nil {
		return err
	}
	return readErr
}

// encoder is one encode worker's state, reused for every chunk.
type encoder struct {
	b       *Buckets
	files   [][]*bucketFile
	values  []uint64 // codes in line order
	owners  []uint32 // bucket of each code
	grouped []uint64 // codes grouped by bucket
	starts  []int    // where each bucket's group starts in grouped
	next    []int
}

func (b *Buckets) newEncoder(files [][]*bucketFile, buckets int) *encoder {
	return &encoder{b: b, files: files, starts: make([]int, buckets+1), next: make([]int, buckets)}
}

// encode packs every code in the chunk, groups the codes by bucket with a
// counting sort, and appends each group to its bucket file in one write.
//
// Ex: a chunk of source 1 with 3 buckets
//
//	lines:    HAPPYHRS   SUPER100   FIFTYOFF   SHORT
//	owners:   2          0          2          (not a code)
//	starts:   [0, 1, 1, 3]           bucket 0 = [0,1), bucket 1 = [1,1), bucket 2 = [1,3)
//	grouped:  SUPER100 | HAPPYHRS FIFTYOFF
//	writes:   src1/b000.dat += SUPER100
//	          src1/b002.dat += HAPPYHRS FIFTYOFF
func (e *encoder) encode(chunk couponsource.Chunk) error {
	codec, hash, buckets := e.b.opts.Codec, e.b.hash, uint64(len(e.next))
	e.values, e.owners = e.values[:0], e.owners[:0]
	for rest := chunk.Lines; len(rest) > 0; {
		var line []byte
		line, rest, _ = bytes.Cut(rest, []byte{'\n'})
		if code, ok := codec.Encode(bytes.TrimSuffix(line, []byte{'\r'})); ok {
			e.values = append(e.values, code)
			e.owners = append(e.owners, uint32(hash(code)%buckets))
		}
	}

	clear(e.starts)
	for _, owner := range e.owners {
		e.starts[owner+1]++
	}
	for bucket := range e.next {
		e.starts[bucket+1] += e.starts[bucket]
	}
	copy(e.next, e.starts)
	e.grouped = slices.Grow(e.grouped[:0], len(e.values))[:len(e.values)]
	for i, value := range e.values {
		owner := e.owners[i]
		e.grouped[e.next[owner]] = value
		e.next[owner]++
	}

	for bucket, file := range e.files[chunk.Source] {
		if group := e.grouped[e.starts[bucket]:e.starts[bucket+1]]; len(group) > 0 {
			if err := file.append(group); err != nil {
				return err
			}
		}
	}
	return nil
}
