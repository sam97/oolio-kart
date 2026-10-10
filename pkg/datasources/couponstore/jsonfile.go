package couponstore

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
)

// FileName is the file JSONFile keeps in its folder. It starts with a dot so
// the coupon folder's reader ignores it.
const FileName = ".valid-codes.json"

// JSONFile stores the result as one JSON file, replaced atomically.
type JSONFile struct {
	dir string
}

func NewJSONFile(dir string) *JSONFile {
	return &JSONFile{dir: dir}
}

type savedCodes struct {
	Rules string               `json:"rules"`
	Files map[string]savedFile `json:"files"`
	Codes []string             `json:"codes"`
}

type savedFile struct {
	Size    int64 `json:"size"`
	ModTime int64 `json:"modTime"` // Unix nanoseconds
}

func (store *JSONFile) Begin(ctx context.Context, fp Fingerprint) (Batch, error) {
	return &jsonBatch{store: store, fp: fp}, nil
}

// Load returns found = false for a missing, unreadable or out of date file.
// Only an unreadable file is also reported as an error.
func (store *JSONFile) Load(ctx context.Context, fp Fingerprint) ([]string, bool, error) {
	data, err := os.ReadFile(store.path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var saved savedCodes
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, false, err
	}
	// Lookups binary search the codes, so they must be sorted and unique.
	for i := 1; i < len(saved.Codes); i++ {
		if saved.Codes[i-1] >= saved.Codes[i] {
			return nil, false, errors.New("codes are not sorted and unique")
		}
	}
	if !saved.fingerprint().Equal(fp) {
		return nil, false, nil
	}
	return saved.Codes, true, nil
}

func (store *JSONFile) path() string {
	return filepath.Join(store.dir, FileName)
}

// fingerprint rebuilds the Fingerprint the codes were saved with. Sources
// are listed by name, as couponsource.Reader lists them.
func (saved savedCodes) fingerprint() Fingerprint {
	fp := Fingerprint{Rules: saved.Rules}
	for name, file := range saved.Files {
		fp.Sources = append(fp.Sources, couponsource.Info{Name: name, Size: file.Size, ModTime: time.Unix(0, file.ModTime)})
	}
	slices.SortFunc(fp.Sources, func(a, b couponsource.Info) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return fp
}

// jsonBatch holds the codes in memory until Commit writes them. Valid codes
// are a tiny fraction of the input, so this stays small.
type jsonBatch struct {
	store *JSONFile
	fp    Fingerprint
	codes []string
}

func (batch *jsonBatch) Add(code string) error {
	batch.codes = append(batch.codes, code)
	return nil
}

func (batch *jsonBatch) Commit() error {
	slices.Sort(batch.codes)
	saved := savedCodes{
		Rules: batch.fp.Rules,
		Files: make(map[string]savedFile, len(batch.fp.Sources)),
		Codes: slices.Compact(batch.codes),
	}
	for _, info := range batch.fp.Sources {
		saved.Files[info.Name] = savedFile{Size: info.Size, ModTime: info.ModTime.UnixNano()}
	}
	return writeAtomic(batch.store.path(), saved)
}

func (batch *jsonBatch) Abort() {
	batch.codes = nil
}

// writeAtomic writes to a temporary file first, so a crash never leaves a
// half-written one behind.
func writeAtomic(path string, value any) error {
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name()) // no-op once renamed

	if err := json.NewEncoder(temp).Encode(value); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
