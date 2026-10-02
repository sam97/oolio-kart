package coupons

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// SavedName is the file, inside the watched folder, that keeps the result of
// the last successful load. It starts with a dot so scan ignores it.
const SavedName = ".valid-codes.json"

// savedCodes is the result of a load together with what it was built from. It
// is only reused when the folder and the validity rules are unchanged, so a
// restart can skip a full build.
type savedCodes struct {
	MinLength int                  `json:"minLength"`
	MaxLength int                  `json:"maxLength"`
	MinFiles  int                  `json:"minFiles"`
	Files     map[string]savedFile `json:"files"`
	Codes     []string             `json:"codes"`
}

type savedFile struct {
	Size    int64 `json:"size"`
	ModTime int64 `json:"modTime"`
}

func newSavedCodes(snap snapshot, codes []string) savedCodes {
	files := make(map[string]savedFile, len(snap))
	for path, state := range snap {
		// Base names keep the file valid if the folder is mounted elsewhere.
		files[filepath.Base(path)] = savedFile{state.size, state.modTime}
	}
	return savedCodes{
		MinLength: MinLength,
		MaxLength: MaxLength,
		MinFiles:  MinFiles,
		Files:     files,
		Codes:     codes,
	}
}

// matches reports whether saved was built from snap under the current rules.
func (saved savedCodes) matches(snap snapshot) bool {
	if saved.MinLength != MinLength || saved.MaxLength != MaxLength || saved.MinFiles != MinFiles {
		return false
	}
	if len(saved.Files) != len(snap) {
		return false
	}
	for path, state := range snap {
		file, found := saved.Files[filepath.Base(path)]
		if !found || file != (savedFile{state.size, state.modTime}) {
			return false
		}
	}
	return true
}

// writeSaved replaces dir's saved file. It writes to a temporary file first so
// a crash never leaves a half-written one behind.
func writeSaved(dir string, saved savedCodes) error {
	temp, err := os.CreateTemp(dir, SavedName+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name()) // no-op once renamed

	if err := json.NewEncoder(temp).Encode(saved); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), filepath.Join(dir, SavedName))
}

// readSaved returns dir's saved file. A missing file is reported with an error
// satisfying errors.Is(err, fs.ErrNotExist).
func readSaved(dir string) (savedCodes, error) {
	data, err := os.ReadFile(filepath.Join(dir, SavedName))
	if err != nil {
		return savedCodes{}, err
	}
	var saved savedCodes
	if err := json.Unmarshal(data, &saved); err != nil {
		return savedCodes{}, err
	}
	// IsValid binary searches the codes, so they must be sorted and unique.
	for i, code := range saved.Codes {
		if _, ok := encode([]byte(code)); !ok {
			return savedCodes{}, fmt.Errorf("invalid code %q", code)
		}
		if i > 0 && saved.Codes[i-1] >= code {
			return savedCodes{}, errors.New("codes are not sorted and unique")
		}
	}
	return saved, nil
}

// restore loads the saved codes if they were built from snap. It reports
// whether it did; on false the caller should build the codes as usual.
func (s *Service) restore(snap snapshot) bool {
	start := time.Now()
	saved, err := readSaved(s.cfg.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		s.cfg.Logger.Warn("ignore saved coupons", "file", SavedName, "err", err)
		return false
	}
	if !saved.matches(snap) {
		s.cfg.Logger.Info("saved coupons are out of date, rebuilding")
		return false
	}

	s.mu.Lock()
	s.codes = saved.Codes
	s.mu.Unlock()
	s.cfg.Logger.Info("restored saved coupons", "files", len(snap), "codes", len(saved.Codes))

	if s.cfg.OnLoad != nil {
		s.cfg.OnLoad(LoadResult{
			Files:    slices.Sorted(maps.Keys(snap)),
			Codes:    len(saved.Codes),
			Duration: time.Since(start),
			Restored: true,
		})
	}
	return true
}
