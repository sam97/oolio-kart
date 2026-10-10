// Package coupons builds the set of valid promo codes from a set of coupon
// base files.
//
// A promo code is valid when it is 8 to 10 characters long and appears in at
// least two of the files. Each file is loaded into memory as packed uint64
// codes, sorted and de-duplicated on its own, and the sorted files are then
// merged in a single pass that counts how many files contain each code.
package coupons

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/sam97/oolio-kart/pkg/models"
)

const (
	MinLength = models.CouponMinLength
	MaxLength = models.CouponMaxLength

	// MinFiles is the number of distinct files a code must appear in.
	MinFiles = 2

	bitsPerChar = 6
)

const noSymbol = 0xFF

// symbolOf maps a code byte to its 6-bit symbol. 0 is reserved for padding so
// shorter codes sort before longer codes with the same prefix; noSymbol marks
// bytes that cannot be packed. The mapping keeps ASCII order, so the order of
// packed codes is the byte order of the strings.
//
// 256 because that is the ASCII range our codes are expected to fall into.
var symbolOf = func() (table [256]byte) {
	for i := range table { // initialize table
		table[i] = noSymbol
	}

	// assign a symbol to each possible character in the coupon codes
	next := byte(1)
	for _, span := range [][2]byte{{'0', '9'}, {'A', 'Z'}, {'a', 'z'}} {
		for char := span[0]; char <= span[1]; char++ {
			table[char] = next
			next++
		}
	}
	return table
}()

// charOf is the reverse of symbolOf. It converts a symbole to its character.
// 64 because the possible characters are 62 in total.
var charOf = func() (table [64]byte) {
	for char, symbol := range symbolOf {
		if symbol != noSymbol {
			table[symbol] = byte(char)
		}
	}
	return table
}()

// encode packs a code of valid length of alphanumeric characters into a
// uint64, left-aligned in 60 bits. It reports false for any other input.
//
// Ex: "HAPPYHRS" (8 chars)
//
//	H      A      P      P      Y      H      R      S      pad    pad
//	001111 000100 011001 011001 100010 001111 011011 011100 000000 000000
//	└────────────────────── 60 bits in a uint64 ───────────────────────┘
func encode(code []byte) (uint64, bool) {
	if len(code) < MinLength || len(code) > MaxLength {
		return 0, false
	}
	var packed uint64
	for _, char := range code {
		symbol := symbolOf[char]
		if symbol == noSymbol {
			return 0, false
		}
		packed = packed << bitsPerChar // push the earlier bits to the left by 6
		packed |= uint64(symbol)       // write the 6-bit symbol on the right
	}
	// ensure all encodings are 60 bits by padding shorter codes with 0s.
	return packed << (bitsPerChar * (MaxLength - len(code))), true
}

const bitMask = (1<<bitsPerChar - 1) // 0011 1111

func decode(packed uint64) string {
	/*
		Example, decoding "HAPPYHRS":

			packed = 001111 000100 011001 011001 100010 001111 011011 011100 000000 000000
					pos=9  pos=8  pos=7  pos=6  pos=5  pos=4  pos=3  pos=2  pos=1  pos=0

			pos = 9:  packed >> 54  →  ........................................ 001111
			          & 111111      →  001111  = 15  → charOf[15] = 'H'

			pos = 8:  packed >> 48  →  .............................. 001111 000100
			          & 111111      →  000100  =  4  → charOf[4]  = 'A'
	*/

	var code [MaxLength]byte
	length := 0
	for pos := MaxLength - 1; pos >= 0; pos-- {
		isolated := packed >> (bitsPerChar * pos)
		symbol := isolated & bitMask
		if symbol == 0 {
			break
		}
		code[length] = charOf[symbol]
		length++
	}
	return string(code[:length])
}

// Build reads every file in paths and returns the valid promo codes in sorted
// order. Files ending in .gz are decompressed. Files are loaded and sorted
// concurrently, so memory use peaks at about 8 bytes per line across all
// files.
func Build(paths []string) ([]string, error) {
	sets := make([][]uint64, len(paths)) // sets of codes in each file
	errs := make([]error, len(paths))
	var wg sync.WaitGroup

	for i, p := range paths {
		wg.Go(func() {
			sets[i], errs[i] = loadSorted(p)
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	merged := mergeCount(sets, MinFiles)
	codes := make([]string, len(merged))
	for i, v := range merged {
		codes[i] = decode(v)
	}
	return codes, nil
}

// loadSorted returns the distinct codes of one file in ascending order.
func loadSorted(path string) ([]uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	size, err := uncompressedSize(f, path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var r io.Reader = f
	if isGzip(path) {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		defer zr.Close()
		r = zr
	}

	// Every kept line takes at least MinLength bytes plus a newline, so this
	// is an upper bound that avoids regrowing a slice of ~100M entries.
	codes := make([]uint64, 0, size/(MinLength+1)+1)
	err = eachLine(r, func(line []byte) {
		if v, ok := encode(line); ok {
			codes = append(codes, v)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	slices.Sort(codes)
	return slices.Compact(codes), nil
}

// eachLine calls fn with every line of r, without its line ending. Lines that
// do not fit in the read buffer are skipped since they can never be codes.
func eachLine(r io.Reader, fn func([]byte)) error {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			for err == bufio.ErrBufferFull {
				_, err = br.ReadSlice('\n')
			}
		} else {
			line = bytes.TrimSuffix(line, []byte{'\n'})
			fn(bytes.TrimSuffix(line, []byte{'\r'}))
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func isGzip(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".gz")
}

// uncompressedSize estimates the decompressed size of f, used only to size
// buffers. For gzip it reads the ISIZE trailer, which is the size modulo 2^32;
// a value smaller than the compressed size means it wrapped and is ignored.
func uncompressedSize(f *os.File, path string) (int64, error) {
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := st.Size()

	if !isGzip(path) {
		return size, nil
	}
	if size < 4 {
		return 0, nil
	}

	var trailer [4]byte
	if _, err := f.ReadAt(trailer[:], size-4); err != nil {
		return 0, err
	}
	if realSize := int64(binary.LittleEndian.Uint32(trailer[:])); realSize >= size {
		return realSize, nil
	}
	return size, nil
}

// mergeCount merges sorted, de-duplicated sets and returns, in order, the
// values that appear in at least min of them.
func mergeCount(sets [][]uint64, min int) []uint64 {
	pos := make([]int, len(sets)) // cursor inside each set
	var out []uint64
	for {
		var smallestCode uint64
		found := false

		// find the smallest code at each cursor
		for i, codes := range sets {
			if pos[i] >= len(codes) {
				continue
			}
			if !found || codes[pos[i]] < smallestCode {
				smallestCode, found = codes[pos[i]], true
			}
		}
		if !found { // no code in any file
			return out
		}

		// find the sets with the smallest code and increment their cursors
		count := 0 // number of sets containing the smallest code
		for i, codes := range sets {
			if pos[i] < len(codes) && codes[pos[i]] == smallestCode {
				pos[i]++
				count++
			}
		}

		// store the valid code
		if count >= min {
			out = append(out, smallestCode)
		}
	}
}
