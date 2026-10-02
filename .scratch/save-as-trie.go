package main

import (
	"bufio"
	"bytes"
	"os"
)

func main() {
	f, err := os.Open("C:/Users/smaha/Downloads/couponbase1.txt")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	print("opened file")

	// err = read(f, 1)
	s := bufio.NewScanner(f)
	// s.Buffer([]byte{}, int(^uint(0)>>1))
	max := 100
	for s.Scan() {
		if max > len(s.Bytes()) {
			max = len(s.Bytes())
		}
	}
	err = s.Err()
	if err != nil {
		panic(err)
	}
	print(max)
}

func read(f *os.File, linesAtOnce int) error {
	s := bufio.NewScanner(f)
	linesCount := 0
	s.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		// copied from bufio.ScanLines
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			linesCount++
			if linesCount >= linesAtOnce {
				linesCount = 0
				// We have a full newline-terminated line.
				return i + 1, dropCR(data[0:i]), nil
			}
		}
		// If we're at EOF, we have a final, non-terminated line. Return it.
		if atEOF {
			return len(data), dropCR(data), nil
		}
		// Request more data.
		return 0, nil, nil
	})
	// for s.Scan() {
	// 	print(linesCount)
	// }
	s.Buffer([]byte{}, int(^uint(0)>>1))
	s.Scan()
	if err := s.Err(); err != nil {
		print(linesCount)
		return err
	}
	return nil
}

// dropCR drops a terminal \r from the data.
func dropCR(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] == '\r' {
		return data[0 : len(data)-1]
	}
	return data
}
