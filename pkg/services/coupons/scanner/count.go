package scanner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"

	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
)

// countAll counts every bucket on a pool of workers, each owning one slot of
// codesPerSlot values for the whole phase, and adds the valid codes to out from
// a single goroutine.
func (b *Buckets) countAll(ctx context.Context, files [][]*bucketFile, codesPerSlot, workers int, out couponstore.Batch) (codes, oversized int, err error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	// Workers send each bucket's valid codes to found; one collector
	// decodes them and feeds out, so the Batch needs no locking.
	found := make(chan []uint64, workers)
	collected := make(chan error, 1)
	go func() {
		var err error
		for batch := range found { // keep draining after an error, so workers never block
			for _, code := range batch {
				if err != nil {
					break
				}
				if err = out.Add(b.opts.Codec.Decode(code)); err != nil {
					cancel(err)
				} else {
					codes++
				}
			}
		}
		collected <- err
	}()

	// Unbuffered: a bucket is handed out only when a worker is free, so the
	// workers take buckets in turn and finish around the same time.
	buckets := make(chan int)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			// One slot per worker for the whole phase: memory is set by
			// the worker count, not by how many buckets there are.
			w := countWorker{b: b, slot: make([]uint64, codesPerSlot)}
			for bucket := range buckets {
				if ctx.Err() != nil { // drain without work once cancelled
					continue
				}
				// Bucket i of every source: together they hold every
				// occurrence of the codes that hash to i.
				parts := make([]*bucketFile, len(files))
				for source := range files {
					parts[source] = files[source][bucket]
				}
				valid, big, err := w.count(parts)
				if err != nil {
					cancel(fmt.Errorf("bucket %d: %w", bucket, err))
					continue
				}
				if big {
					mu.Lock()
					oversized++
					mu.Unlock()
				}
				if len(valid) > 0 {
					found <- valid
				}
			}
		})
	}
	for bucket := range len(files[0]) {
		buckets <- bucket
	}
	close(buckets)
	wg.Wait()
	close(found) // every worker is done sending; lets the collector finish

	// An Add error first, otherwise a worker error or the parent's
	// cancellation.
	if err := <-collected; err != nil {
		return codes, oversized, err
	}
	return codes, oversized, context.Cause(ctx)
}

type countWorker struct {
	b    *Buckets
	slot []uint64
}

// count sorts and de-duplicates each source's part of one bucket, writes it
// back sorted, and returns the codes found in at least MinFiles sources.
// big reports that the parts did not fit in the slot together.
//
// Ex: bucket 7 of three sources fits in the slot
//
//	slot:   [ src0/b007: 9 4 7 4 | src1/b007: 7 2 | src2/b007: 4 1 ]   loaded
//	        [ 4 7 9 · | 2 7 | 1 4 ]                                     sorted, de-duplicated, written back
//	merge:  1→1  2→1  4→2  7→2  9→1                                     with MinFiles 2: 4, 7
//
// Parts that do not fit together are sorted one at a time, on disk if need
// be, and merged as streams; see countOnDisk.
func (w *countWorker) count(parts []*bucketFile) (valid []uint64, big bool, err error) {
	var total int64
	for _, part := range parts {
		total += part.codes
	}
	if total > int64(len(w.slot)) {
		valid, err = w.countOnDisk(parts)
		return valid, true, err
	}

	// Lay the parts side by side in the slot. Each keeps its full length
	// here, though Compact may shrink it, so the parts never overlap.
	cursors := make([]*cursor, 0, len(parts))
	offset := 0
	for _, part := range parts {
		values := w.slot[offset : offset+int(part.codes)]
		offset += len(values)
		if err := readValues(part.path, values); err != nil {
			return nil, false, err
		}
		slices.Sort(values)
		values = slices.Compact(values) // repeats within one source count once
		// Kept sorted on disk for a later incremental build.
		if err := writeValues(part.path, values); err != nil {
			return nil, false, err
		}
		part.codes = int64(len(values)) // now the distinct count, for the manifest
		cursors = append(cursors, memoryCursor(values))
	}
	valid, err = w.collect(cursors)
	return valid, false, err
}

// countOnDisk handles a bucket too big for the slot. Each source's part is
// sorted on its own (see sortOnDisk), then all parts are merged as streams,
// each read through an equal window of the slot.
func (w *countWorker) countOnDisk(parts []*bucketFile) ([]uint64, error) {
	// Sort the parts one at a time; each may use the whole slot.
	for _, part := range parts {
		if err := w.sortOnDisk(part); err != nil {
			return nil, err
		}
	}

	// Then reuse the slot as read windows, one per part.
	windows := w.windows(len(parts))
	cursors := make([]*cursor, len(parts))
	for i, part := range parts {
		file, err := os.Open(part.path)
		if err != nil {
			return nil, err
		}
		defer file.Close() // open until the merge below finishes
		if cursors[i], err = fileCursor(file, part.codes, windows[i]); err != nil {
			return nil, err
		}
	}
	return w.collect(cursors)
}

// sortOnDisk sorts and de-duplicates one part in place. A part that fits in
// the slot is sorted in memory. A larger one is an external merge sort: it is
// cut into slot-sized runs, each sorted and written out, and the runs are
// merged back into the part's file.
//
// Ex: a part of 10 codes with a 4-code slot
//
//	b007.dat:       9 4 7 4 | 3 8 1 3 | 2 9
//	runs:           4 7 9   | 1 3 8   | 2 9     each sorted and de-duplicated
//	merged back:    1 2 3 4 7 8 9               9 appears in two runs, kept once
func (w *countWorker) sortOnDisk(part *bucketFile) error {
	if part.codes <= int64(len(w.slot)) {
		values := w.slot[:part.codes]
		if err := readValues(part.path, values); err != nil {
			return err
		}
		slices.Sort(values)
		values = slices.Compact(values)
		part.codes = int64(len(values))
		return writeValues(part.path, values)
	}

	file, err := os.Open(part.path)
	if err != nil {
		return err
	}
	// Runs are temporary. Deferred first, so it runs last: after the run
	// files are closed, which Windows needs before it can remove them.
	var runs []string
	var sizes []int64
	defer func() {
		for _, run := range runs {
			os.Remove(run)
		}
	}()
	// Phase 1: cut the part into slot-sized runs, each sorted on its own.
	for left := part.codes; left > 0; {
		values := w.slot[:min(left, int64(len(w.slot)))]
		left -= int64(len(values))
		if _, err := io.ReadFull(file, asBytes(values)); err != nil {
			file.Close()
			return err
		}
		slices.Sort(values)
		values = slices.Compact(values)
		run := fmt.Sprintf("%s.run%d", part.path, len(runs))
		if err := os.WriteFile(run, asBytes(values), 0o644); err != nil {
			file.Close()
			return err
		}
		runs, sizes = append(runs, run), append(sizes, int64(len(values)))
	}
	if err := file.Close(); err != nil {
		return err
	}

	// Phase 2: merge the runs back into one sorted file, streaming each
	// through a window of the slot, which the runs no longer need.
	windows := w.windows(len(runs))
	cursors := make([]*cursor, len(runs))
	for i, run := range runs {
		file, err := os.Open(run)
		if err != nil {
			return err
		}
		defer file.Close()
		if cursors[i], err = fileCursor(file, sizes[i], windows[i]); err != nil {
			return err
		}
	}

	// Write to a temporary file and rename it over the part, so a crash
	// never leaves a half-merged bucket.
	temp, err := os.Create(part.path + ".tmp")
	if err != nil {
		return err
	}
	defer temp.Close() // on error paths; closed explicitly before the rename
	writer := bufio.NewWriterSize(temp, 64<<10)
	var distinct int64
	var one [1]uint64 // a reusable one-code slice, so asBytes needs no allocation
	// The count is ignored: a code in several runs is written once, which
	// de-duplicates across runs.
	err = merge(cursors, func(code uint64, _ int) error {
		one[0] = code
		distinct++
		_, err := writer.Write(asBytes(one[:]))
		return err
	})
	if err != nil {
		return err
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	part.codes = distinct
	return os.Rename(temp.Name(), part.path)
}

// windows cuts the slot into n equal windows for streaming merges. A slot
// smaller than n gets one value per window from fresh memory.
func (w *countWorker) windows(n int) [][]uint64 {
	slot := w.slot
	size := len(slot) / n
	if size == 0 {
		size, slot = 1, make([]uint64, n)
	}
	windows := make([][]uint64, n)
	for i := range windows {
		windows[i] = slot[i*size : (i+1)*size]
	}
	return windows
}

// collect merges the cursors and keeps the codes held by at least MinFiles
// of them.
func (w *countWorker) collect(cursors []*cursor) ([]uint64, error) {
	var valid []uint64
	err := merge(cursors, func(code uint64, count int) error {
		if count >= w.b.opts.MinFiles {
			valid = append(valid, code)
		}
		return nil
	})
	return valid, err
}

// readValues fills values from the start of the file at path.
func readValues(path string, values []uint64) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.ReadFull(file, asBytes(values))
	return err
}
