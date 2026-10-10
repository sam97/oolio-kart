package couponstore

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/sam97/oolio-kart/pkg/datasources/internal/datastores/memory"
	"github.com/sam97/oolio-kart/pkg/models"
)

// fakeStore is a Lookup and Settings in memory.
type fakeStore struct {
	codes         map[string]bool
	published     bool
	settings      models.CouponSettings
	err           error // returned by every call when set
	settingsCalls int
}

func (f *fakeStore) Contains(_ context.Context, code string) (bool, bool, error) {
	return f.codes[code], f.published, f.err
}

func (f *fakeStore) Settings(context.Context) (models.CouponSettings, error) {
	f.settingsCalls++
	return f.settings, f.err
}

var defaultSettings = models.CouponSettings{MinLength: 8, MaxLength: 10, MinFiles: 2, DiscountPercent: 10}

func TestCachedSettings(t *testing.T) {
	ctx := t.Context()
	mem := newMemory(t)
	store := &fakeStore{settings: defaultSettings}
	settings := NewCachedSettings(store, mem, time.Minute, slog.New(slog.DiscardHandler))

	for range 3 {
		if got, err := settings.Settings(ctx); err != nil || got != defaultSettings {
			t.Fatalf("Settings = %+v, %v", got, err)
		}
	}
	if store.settingsCalls != 1 {
		t.Errorf("settings read %d times, want 1", store.settingsCalls)
	}

	store.err = errors.New("down")
	uncached := NewCachedSettings(store, newMemory(t), time.Minute, slog.New(slog.DiscardHandler))
	if _, err := uncached.Settings(ctx); err == nil {
		t.Error("a failed read was not reported")
	}
}

func newMemory(t *testing.T) *memory.Cache {
	t.Helper()
	mem, err := memory.NewCache(10)
	if err != nil {
		t.Fatal(err)
	}
	return mem
}

// countingLookup counts the lookups that reach it.
type countingLookup struct {
	*fakeStore
	calls int
}

func (c *countingLookup) Contains(ctx context.Context, code string) (bool, bool, error) {
	c.calls++
	return c.fakeStore.Contains(ctx, code)
}

func TestCachedLookup(t *testing.T) {
	ctx := t.Context()
	next := &countingLookup{fakeStore: &fakeStore{codes: map[string]bool{"HAPPYHRS": true}, published: true}}
	cached := NewCachedLookup(next, newMemory(t), time.Minute, time.Minute, slog.New(slog.DiscardHandler))
	for range 3 {
		if valid, published, err := cached.Contains(ctx, "HAPPYHRS"); !valid || !published || err != nil {
			t.Fatalf("Contains(HAPPYHRS) = %v, %v, %v", valid, published, err)
		}
		if valid, _, _ := cached.Contains(ctx, "SUPER100"); valid {
			t.Fatal("Contains(SUPER100) = valid")
		}
	}
	if next.calls != 2 {
		t.Errorf("lookups reached the store %d times, want 2", next.calls)
	}

	// Bust forgets every answer.
	if err := cached.Bust(ctx); err != nil {
		t.Fatal(err)
	}
	cached.Contains(ctx, "HAPPYHRS")
	if next.calls != 3 {
		t.Errorf("after Bust, lookups reached the store %d times, want 3", next.calls)
	}
}

func TestCachedLookupSkipsUnpublishedAndFailures(t *testing.T) {
	ctx := t.Context()
	next := &countingLookup{fakeStore: &fakeStore{codes: map[string]bool{"HAPPYHRS": true}}}
	cached := NewCachedLookup(next, newMemory(t), time.Minute, time.Minute, slog.New(slog.DiscardHandler))

	cached.Contains(ctx, "HAPPYHRS") // not published yet
	next.published, next.err = true, errors.New("down")
	cached.Contains(ctx, "HAPPYHRS")
	next.err = nil
	if valid, published, err := cached.Contains(ctx, "HAPPYHRS"); !valid || !published || err != nil {
		t.Errorf("Contains = %v, %v, %v once published", valid, published, err)
	}
	if next.calls != 3 {
		t.Errorf("lookups reached the store %d times, want 3", next.calls)
	}
}

func TestCachedLookupZeroTTLDisablesCaching(t *testing.T) {
	next := &countingLookup{fakeStore: &fakeStore{codes: map[string]bool{"HAPPYHRS": true}, published: true}}
	cached := NewCachedLookup(next, newMemory(t), 0, 0, slog.New(slog.DiscardHandler))
	for range 2 {
		cached.Contains(t.Context(), "HAPPYHRS")
		cached.Contains(t.Context(), "SUPER100")
	}
	if next.calls != 4 {
		t.Errorf("lookups reached the store %d times, want 4", next.calls)
	}
}
