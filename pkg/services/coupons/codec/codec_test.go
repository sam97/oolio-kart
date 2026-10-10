package codec

import (
	"slices"
	"testing"
)

func TestAlphanumericRoundTripAndOrder(t *testing.T) {
	codec, err := NewAlphanumeric(8, 10)
	if err != nil {
		t.Fatal(err)
	}
	codes := []string{"00000000", "0000000000", "HAPPYHRS", "HAPPYHRSA", "HAPPYHRSZ", "Zzzzzzzzzz", "abcdefgh", "zzzzzzzzzz"}
	var packed []uint64
	for _, code := range codes {
		value, ok := codec.Encode([]byte(code))
		if !ok {
			t.Fatalf("Encode(%q) failed", code)
		}
		if got := codec.Decode(value); got != code {
			t.Errorf("Decode(Encode(%q)) = %q", code, got)
		}
		packed = append(packed, value)
	}
	if !slices.IsSorted(packed) {
		t.Errorf("packed codes are not in string order: %v", packed)
	}

	for _, code := range []string{"", "SHORT77", "ELEVENCHARS", "HAPPY HR", "HAPPY-HR", "HAPPYHRS\r"} {
		if _, ok := codec.Encode([]byte(code)); ok {
			t.Errorf("Encode(%q) succeeded, want failure", code)
		}
	}
}

func TestAlphanumericLengths(t *testing.T) {
	codec, err := NewAlphanumeric(3, 4)
	if err != nil {
		t.Fatal(err)
	}
	if codec.MinLength() != 3 {
		t.Errorf("MinLength = %d, want 3", codec.MinLength())
	}
	for code, want := range map[string]bool{"AB": false, "ABC": true, "ABCD": true, "ABCDE": false} {
		value, ok := codec.Encode([]byte(code))
		if ok != want {
			t.Errorf("Encode(%q) ok = %v, want %v", code, ok, want)
		}
		if ok && codec.Decode(value) != code {
			t.Errorf("Decode(Encode(%q)) = %q", code, codec.Decode(value))
		}
	}

	for _, lengths := range [][2]int{{0, 5}, {6, 5}, {8, 11}} {
		if _, err := NewAlphanumeric(lengths[0], lengths[1]); err == nil {
			t.Errorf("NewAlphanumeric(%d, %d) succeeded", lengths[0], lengths[1])
		}
	}
}
