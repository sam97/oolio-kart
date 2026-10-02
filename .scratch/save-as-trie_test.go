package main

import (
	"os"
	"strconv"
	"testing"
)

func BenchmarkTriesSingleLine(b *testing.B) {
	f, err := os.Open("C:/Users/smaha/Downloads/couponbase1.txt")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	for b.Loop() {
		for range 100 {
			if err := read(f, 100); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkTriesTenLines(b *testing.B) {
	f, err := os.Open("C:/Users/smaha/Downloads/couponbase1.txt")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	for b.Loop() {
		for range 10 {
			if err := read(f, 1000); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkTriesHundredLines(b *testing.B) {
	// f, err := os.Open("C:/Users/smaha/Downloads/couponbase1.txt")
	// if err != nil {
	// 	panic(err)
	// }
	// defer f.Close()

	// for b.Loop() {
	// 	for range 1 {
	// 		if err := read(f, 10000); err != nil {
	// 			b.Fatal(err)
	// 		}
	// 	}
	// }

	const loop = 300000000
	i := 30000
	j := loop / i
	for i <= loop {
		b.Run(strconv.Itoa(i), func(b *testing.B) {
			f, err := os.Open("C:/Users/smaha/Downloads/couponbase1.txt")
			if err != nil {
				panic(err)
			}
			defer f.Close()

			for b.Loop() {
				for range j {
					if err := read(f, i); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
		i *= 10
		j /= 10
	}
}
