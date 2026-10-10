package scanner

import (
	"container/heap"
	"io"
	"os"
)

// cursor walks one sorted, de-duplicated stream of codes: a slice already in
// memory, or a file read through a window of memory.
type cursor struct {
	values []uint64 // unread part of the window
	window []uint64
	file   *os.File // nil for a stream fully in memory
	left   int64    // codes still in the file
}

func memoryCursor(values []uint64) *cursor {
	return &cursor{values: values}
}

// fileCursor streams count codes from file through window.
func fileCursor(file *os.File, count int64, window []uint64) (*cursor, error) {
	c := &cursor{window: window, file: file, left: count}
	return c, c.fill()
}

func (c *cursor) fill() error {
	n := min(int64(len(c.window)), c.left)
	c.values = c.window[:n]
	c.left -= n
	_, err := io.ReadFull(c.file, asBytes(c.values))
	return err
}

func (c *cursor) empty() bool { return len(c.values) == 0 }

func (c *cursor) head() uint64 { return c.values[0] }

func (c *cursor) advance() error {
	c.values = c.values[1:]
	if len(c.values) == 0 && c.left > 0 {
		return c.fill()
	}
	return nil
}

// cursorHeap orders cursors by their next code, for container/heap.
type cursorHeap []*cursor

func (h cursorHeap) Len() int           { return len(h) }
func (h cursorHeap) Less(i, j int) bool { return h[i].head() < h[j].head() }
func (h cursorHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *cursorHeap) Push(x any)        { *h = append(*h, x.(*cursor)) }
func (h *cursorHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

// merge walks the cursors together in code order, and calls visit once per
// distinct code with the number of cursors that hold it. Each cursor must be
// sorted and hold a code at most once.
//
// Ex: one cursor per source, MinFiles = 2
//
//	src0: BIRTHDAY HAPPYHRS SUPER100
//	src1: BIRTHDAY MOODYHRS
//	src2: BIRTHDAY HAPPYHRS
//
//	visit(BIRTHDAY, 3)  visit(HAPPYHRS, 2)  visit(MOODYHRS, 1)  visit(SUPER100, 1)
//	valid: BIRTHDAY, HAPPYHRS
//
// The heap keeps the cursor with the smallest next code on top, so each step
// costs O(log k) for k cursors.
func merge(cursors []*cursor, visit func(code uint64, count int) error) error {
	h := make(cursorHeap, 0, len(cursors))
	for _, c := range cursors {
		if !c.empty() {
			h = append(h, c)
		}
	}
	heap.Init(&h)

	for h.Len() > 0 {
		code, count := h[0].head(), 0
		for h.Len() > 0 && h[0].head() == code {
			count++
			top := h[0]
			if err := top.advance(); err != nil {
				return err
			}
			if top.empty() {
				heap.Pop(&h)
			} else {
				heap.Fix(&h, 0)
			}
		}
		if err := visit(code, count); err != nil {
			return err
		}
	}
	return nil
}
