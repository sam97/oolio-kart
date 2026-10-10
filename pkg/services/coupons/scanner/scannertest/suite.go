package scannertest

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
)

// ScanFunc runs the scanner under test over sources, adding the codes found
// in at least minFiles of them to out. Codes are 8 to 10 letters or digits.
// It may call t.Skip, e.g. for a scanner that is not implemented yet.
type ScanFunc func(t *testing.T, minFiles int, sources []couponsource.Source, out couponstore.Batch) error

// Case is one input for Run. Its expected codes come from Want, a plain map
// count, so every case checks the scanner against the same reference.
type Case struct {
	Name     string
	MinFiles int
	Sources  func() []couponsource.Source

	// Stress cases build tens of MB of input; Run skips them with -short.
	Stress bool
}

// Run checks scan against Want on every case of Cases.
func Run(t *testing.T, scan ScanFunc) {
	for _, c := range Cases() {
		t.Run(c.Name, func(t *testing.T) {
			if c.Stress && testing.Short() {
				t.Skip("stress case; run without -short")
			}
			sources := c.Sources()
			want := Want(sources, c.MinFiles)

			var out Collected
			if err := scan(t, c.MinFiles, sources, &out); err != nil {
				t.Fatal(err)
			}
			got := slices.Sorted(slices.Values(out.Codes))
			if distinct := len(slices.Compact(slices.Clone(got))); distinct != len(got) {
				t.Errorf("a code was added more than once: %d codes, %d distinct", len(got), distinct)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("found %d codes, want %d\n got: %s\nwant: %s", len(got), len(want), preview(got), preview(want))
			}
		})
	}
}

// Want is the reference answer: the codes of 8 to 10 letters or digits that
// appear in at least minFiles sources, sorted. A trailing \r is not part of a
// line, and a code repeated within one source counts once.
func Want(sources []couponsource.Source, minFiles int) []string {
	files := map[string]int{}
	for _, source := range sources {
		seen := map[string]bool{}
		for line := range strings.Lines(source.(File).Data) {
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if wellFormed(line) && !seen[line] {
				seen[line] = true
				files[line]++
			}
		}
	}
	var want []string
	for code, count := range files {
		if count >= minFiles {
			want = append(want, code)
		}
	}
	slices.Sort(want)
	return want
}

func wellFormed(line string) bool {
	if len(line) < 8 || len(line) > 10 {
		return false
	}
	for _, char := range []byte(line) {
		if !('0' <= char && char <= '9' || 'A' <= char && char <= 'Z' || 'a' <= char && char <= 'z') {
			return false
		}
	}
	return true
}

func preview(codes []string) string {
	if len(codes) > 20 {
		return strings.Join(codes[:20], " ") + fmt.Sprintf(" … (%d more)", len(codes)-20)
	}
	return strings.Join(codes, " ")
}

// Cases are the edge and stress cases every scanner must pass.
func Cases() []Case {
	return []Case{
		// Edge cases.
		{Name: "no codes at all", MinFiles: 2, Sources: func() []couponsource.Source {
			return files(map[string]string{"empty": "", "newlines": "\n\n\r\n", "junk": "SHORT\nWAYTOOLONGCODE\nHAPPY-HR\nHAPPY HR\n"})
		}},
		{Name: "length boundaries and case", MinFiles: 2, Sources: func() []couponsource.Source {
			lines := "SEVEN77\nEIGHT888\nNINE99999\nTEN1010101\nELEVEN11111\nhappyhrs\nHAPPYHRS\n"
			return files(map[string]string{"a": lines, "b": lines})
		}},
		{Name: "line endings and no final newline", MinFiles: 2, Sources: func() []couponsource.Source {
			return files(map[string]string{
				"lf":   "FIRSTONE\nLASTLINE",
				"crlf": "FIRSTONE\r\nLASTLINE\r\n",
				"cr":   "FIRSTONE\r\nLASTLINE\r",
			})
		}},
		{Name: "repeats within one file never count twice", MinFiles: 2, Sources: func() []couponsource.Source {
			return files(map[string]string{"only": strings.Repeat("SUPER100\n", 1000)})
		}},
		{Name: "min files 1 keeps every code", MinFiles: 1, Sources: func() []couponsource.Source {
			sources, _ := RandomSources(rand.New(rand.NewPCG(11, 12)), 3)
			return sources
		}},
		{Name: "min files above the file count keeps none", MinFiles: 4, Sources: func() []couponsource.Source {
			sources, _ := RandomSources(rand.New(rand.NewPCG(13, 14)), 3)
			return sources
		}},
		{Name: "code in every file", MinFiles: 5, Sources: func() []couponsource.Source {
			sources, _ := RandomSources(rand.New(rand.NewPCG(15, 16)), 5)
			for i, source := range sources {
				file := source.(File)
				file.Data = "EVERYWHERE\n" + file.Data + "EVERYWHERE\n"
				sources[i] = file
			}
			return sources
		}},

		// Many files.
		{Name: "many small files", MinFiles: 2, Sources: func() []couponsource.Source {
			return manySmallFiles(rand.New(rand.NewPCG(21, 22)), 300)
		}},
		{Name: "thousands of small files", MinFiles: 2, Stress: true, Sources: func() []couponsource.Source {
			return manySmallFiles(rand.New(rand.NewPCG(23, 24)), 3000)
		}},
		{Name: "small and big files mixed", MinFiles: 2, Sources: func() []couponsource.Source {
			return mixedFiles(rand.New(rand.NewPCG(25, 26)), 40, 3, 50_000)
		}},
		{Name: "many small and big files mixed", MinFiles: 3, Stress: true, Sources: func() []couponsource.Source {
			return mixedFiles(rand.New(rand.NewPCG(27, 28)), 500, 6, 1_000_000)
		}},
		{Name: "many distinct codes", MinFiles: 2, Stress: true, Sources: func() []couponsource.Source {
			return distinctCodes(rand.New(rand.NewPCG(29, 30)), 3, 500_000)
		}},

		// One repeated code.
		{Name: "a few files of one repeated code", MinFiles: 3, Sources: func() []couponsource.Source {
			return repeatedCode(3, 100_000)
		}},
		{Name: "a few big files of one repeated code", MinFiles: 3, Stress: true, Sources: func() []couponsource.Source {
			return repeatedCode(4, 3_000_000)
		}},
		{Name: "big files of one repeated code, too few files", MinFiles: 5, Stress: true, Sources: func() []couponsource.Source {
			return repeatedCode(4, 1_000_000)
		}},
	}
}

func files(data map[string]string) []couponsource.Source {
	var sources []couponsource.Source
	for _, name := range slices.Sorted(maps.Keys(data)) {
		sources = append(sources, File{Name: name, Data: data[name]})
	}
	return sources
}

// randomCode returns a random code of 8 to 10 characters.
func randomCode(rng *rand.Rand) string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	code := make([]byte, 8+rng.IntN(3))
	for i := range code {
		code[i] = alphabet[rng.IntN(len(alphabet))]
	}
	return string(code)
}

// manySmallFiles returns n files of up to 20 lines each, drawn from a pool
// small enough that many codes are in several files.
func manySmallFiles(rng *rand.Rand, n int) []couponsource.Source {
	pool := make([]string, n)
	for i := range pool {
		pool[i] = randomCode(rng)
	}
	sources := make([]couponsource.Source, n)
	for i := range sources {
		var text strings.Builder
		for range rng.IntN(21) {
			text.WriteString(pool[rng.IntN(len(pool))] + "\n")
		}
		sources[i] = File{Name: fmt.Sprintf("small%05d.txt", i), Data: text.String()}
	}
	return sources
}

// mixedFiles returns small files and big files of bigLines lines. Big files
// are mostly codes of their own, so the shared codes are few and spread
// through every file.
func mixedFiles(rng *rand.Rand, small, big, bigLines int) []couponsource.Source {
	sources := manySmallFiles(rng, small)
	shared := make([]string, 200)
	for i := range shared {
		shared[i] = randomCode(rng)
	}
	for i := range big {
		var text strings.Builder
		text.Grow(bigLines * 11)
		for range bigLines {
			if rng.IntN(1000) == 0 {
				text.WriteString(shared[rng.IntN(len(shared))] + "\n")
			} else {
				text.WriteString(randomCode(rng) + "\n")
			}
		}
		sources = append(sources, File{Name: fmt.Sprintf("big%02d.txt", i), Data: text.String()})
	}
	return sources
}

// distinctCodes returns files of lines distinct random codes each, with about
// a tenth drawn from a shared pool, so the result is large.
func distinctCodes(rng *rand.Rand, n, lines int) []couponsource.Source {
	shared := make([]string, lines/5)
	for i := range shared {
		shared[i] = randomCode(rng)
	}
	sources := make([]couponsource.Source, n)
	for i := range sources {
		var text strings.Builder
		text.Grow(lines * 11)
		for range lines {
			code := randomCode(rng)
			if rng.IntN(10) == 0 {
				code = shared[rng.IntN(len(shared))]
			}
			text.WriteString(code + "\n")
		}
		sources[i] = File{Name: fmt.Sprintf("distinct%d.txt", i), Data: text.String()}
	}
	return sources
}

// repeatedCode returns n files that each hold one code, lines times.
func repeatedCode(n, lines int) []couponsource.Source {
	data := strings.Repeat("REPEATED\n", lines)
	sources := make([]couponsource.Source, n)
	for i := range sources {
		sources[i] = File{Name: fmt.Sprintf("repeated%d.txt", i), Data: data}
	}
	return sources
}
