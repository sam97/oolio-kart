// Package codec packs coupon codes into integers, so hundreds of millions of
// them can be sorted and compared cheaply.
package codec

import (
	"errors"
	"fmt"
)

// Codec converts between coupon codes and packed integers. Packed codes must
// sort in the same order as the strings they encode.
type Codec interface {
	// Encode packs line. It reports false for a line that is not a code.
	Encode(line []byte) (uint64, bool)

	// Decode reverses Encode.
	Decode(packed uint64) string

	// MinLength is the length of the shortest code, used to estimate how
	// many codes a file of a given size can hold.
	MinLength() int
}

const (
	bitsPerChar = 6
	charMask    = 1<<bitsPerChar - 1 // 0011 1111

	// MaxPackedLength is the longest code that fits: 10 × 6 = 60 bits.
	MaxPackedLength = 64 / bitsPerChar

	noSymbol = 0xFF
)

// symbolOf maps a code byte to its 6-bit symbol. 0 is reserved for padding so
// shorter codes sort before longer codes with the same prefix; noSymbol marks
// bytes that cannot be packed. The mapping keeps ASCII order, so the order of
// packed codes is the byte order of the strings.
var symbolOf = func() (table [256]byte) {
	for i := range table {
		table[i] = noSymbol
	}
	next := byte(1)
	for _, span := range [][2]byte{{'0', '9'}, {'A', 'Z'}, {'a', 'z'}} {
		for char := span[0]; char <= span[1]; char++ {
			table[char] = next
			next++
		}
	}
	return table
}()

// charOf is the reverse of symbolOf. 64 entries cover the 62 characters and
// padding.
var charOf = func() (table [64]byte) {
	for char, symbol := range symbolOf {
		if symbol != noSymbol {
			table[symbol] = byte(char)
		}
	}
	return table
}()

// Alphanumeric packs codes of [0-9A-Za-z], 6 bits per character.
type Alphanumeric struct {
	minLength int
	maxLength int
}

// NewAlphanumeric accepts codes of minLength to maxLength characters, at most
// MaxPackedLength.
func NewAlphanumeric(minLength, maxLength int) (*Alphanumeric, error) {
	if minLength < 1 || minLength > maxLength {
		return nil, errors.New("code lengths must satisfy 1 <= min <= max")
	}
	if maxLength > MaxPackedLength {
		return nil, fmt.Errorf("codes longer than %d characters do not fit in 64 bits", MaxPackedLength)
	}
	return &Alphanumeric{minLength: minLength, maxLength: maxLength}, nil
}

func (a *Alphanumeric) MinLength() int { return a.minLength }

// Encode packs a code into the low bits of a uint64, left-aligned to
// maxLength characters so integer order is string order.
//
// Ex: "HAPPYHRS" (8 chars) with maxLength 10
//
//	H      A      P      P      Y      H      R      S      pad    pad
//	001111 000100 011001 011001 100010 001111 011011 011100 000000 000000
//	└────────────────────── 60 bits in a uint64 ───────────────────────┘
func (a *Alphanumeric) Encode(line []byte) (uint64, bool) {
	if len(line) < a.minLength || len(line) > a.maxLength {
		return 0, false
	}
	var packed uint64
	for _, char := range line {
		symbol := symbolOf[char]
		if symbol == noSymbol {
			return 0, false
		}
		packed = packed<<bitsPerChar | uint64(symbol) // shift earlier symbols left, write this one on the right
	}
	return packed << (bitsPerChar * (a.maxLength - len(line))), true // pad short codes with 0s
}

// Decode reads symbols from the most significant end until the padding.
//
// Ex: decoding "HAPPYHRS" from the value above
//
//	pos = 9:  packed >> 54 & 111111  →  001111 = 15  →  'H'
//	pos = 8:  packed >> 48 & 111111  →  000100 =  4  →  'A'
//	…
//	pos = 1:  packed >>  6 & 111111  →  000000       →  padding, stop
func (a *Alphanumeric) Decode(packed uint64) string {
	var code [MaxPackedLength]byte
	length := 0
	for pos := a.maxLength - 1; pos >= 0; pos-- {
		symbol := packed >> (bitsPerChar * pos) & charMask
		if symbol == 0 {
			break
		}
		code[length] = charOf[symbol]
		length++
	}
	return string(code[:length])
}
