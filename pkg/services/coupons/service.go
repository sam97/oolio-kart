package coupons

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultPollInterval is how often the watched folder is checked for changes.
const DefaultPollInterval = 2 * time.Second

type Config struct {
	// Dir is the watched folder. Every regular, non-hidden file in it is a
	// coupon base file.
	Dir string

	// PollInterval defaults to DefaultPollInterval. A change is only loaded
	// once the folder looks the same on two polls in a row, so files that are
	// still being copied in are not read half-written.
	PollInterval time.Duration

	// OnLoad, if set, is called after every load attempt. It must return
	// quickly; a slow OnLoad delays the next reload.
	OnLoad func(LoadResult)

	Logger *slog.Logger
}

type LoadResult struct {
	Files    []string
	Codes    int
	Duration time.Duration
	Err      error

	// Restored is true when the codes were read from the saved result of an
	// earlier run instead of being built from the files.
	Restored bool
}

// Service keeps the list of valid coupons in sync with a watched folder.
type Service struct {
	cfg Config

	mu    sync.RWMutex
	codes []string
}

func New(cfg Config) *Service {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{cfg: cfg}
}

// ValidCoupons returns the valid coupons from the last successful load, in
// sorted order.
func (s *Service) ValidCoupons() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.codes)
}

func (s *Service) IsValid(code string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, found := slices.BinarySearch(s.codes, code)
	return found
}

// Run watches the folder and reloads the coupons whenever its contents change,
// until ctx is cancelled. A failed load keeps the previous coupons and is
// retried on the next change.
//
// On start, if the folder is unchanged since the last successful load, the
// saved result of that load is used right away instead of rebuilding.
func (s *Service) Run(ctx context.Context) error {
	t := time.NewTicker(s.cfg.PollInterval)
	defer t.Stop()

	var prev, loaded snapshot
	// Files that match the saved result were complete when it was written, so
	// they need not be seen twice before they are trusted.
	if cur, err := scan(s.cfg.Dir); err == nil && s.restore(cur) {
		prev, loaded = cur, cur
	}
	for {
		cur, err := scan(s.cfg.Dir)
		if err != nil {
			s.cfg.Logger.Error("scan coupon folder", "dir", s.cfg.Dir, "err", err)
		} else if maps.Equal(cur, prev) && (loaded == nil || !maps.Equal(cur, loaded)) {
			s.load(cur)
			loaded = cur
		}
		prev = cur

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (s *Service) load(snap snapshot) {
	files := slices.Sorted(maps.Keys(snap))
	start := time.Now()
	codes, err := Build(files)
	res := LoadResult{Files: files, Codes: len(codes), Duration: time.Since(start), Err: err}

	if err != nil {
		s.cfg.Logger.Error("load coupons", "files", files, "err", err)
	} else {
		s.mu.Lock()
		s.codes = codes
		s.mu.Unlock()
		s.cfg.Logger.Info("loaded coupons", "files", len(files), "codes", len(codes), "took", res.Duration)
		if err := writeSaved(s.cfg.Dir, newSavedCodes(snap, codes)); err != nil {
			s.cfg.Logger.Warn("save coupons", "file", SavedName, "err", err)
		}
	}
	// A load allocates several GB that is garbage once it returns; give it
	// back to the OS instead of holding it until the next load.
	debug.FreeOSMemory()

	if s.cfg.OnLoad != nil {
		s.cfg.OnLoad(res)
	}
}

type fileState struct {
	size    int64
	modTime int64
}

// snapshot maps each file path in the folder to its size and mtime.
type snapshot map[string]fileState

func scan(dir string) (snapshot, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	snap := snapshot{}
	for _, e := range entries {
		if !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		snap[filepath.Join(dir, e.Name())] = fileState{info.Size(), info.ModTime().UnixNano()}
	}
	return snap, nil
}
