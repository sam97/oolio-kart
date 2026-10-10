package validator

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/sam97/oolio-kart/pkg/datasources/cache"
	"github.com/sam97/oolio-kart/pkg/models"
)

var defaultSettings = models.CouponSettings{MinLength: 8, MaxLength: 10, MinFiles: 2, DiscountPercent: 10}

// fakeStore is a couponstore.Lookup and couponstore.Settings in memory.
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

func TestStore(t *testing.T) {
	ctx := t.Context()
	store := &fakeStore{codes: map[string]bool{"HAPPYHRS": true, "SHORT77": true}, published: true, settings: defaultSettings}
	validator := NewStore(store, store, time.Second)

	got, err := validator.Validate(ctx, "HAPPYHRS")
	if err != nil || got != (models.Coupon{Code: "HAPPYHRS", DiscountPercent: 10}) {
		t.Errorf("Validate(HAPPYHRS) = %+v, %v", got, err)
	}
	// SHORT77 is published but shorter than the settings allow.
	for _, code := range []string{"SUPER100", "SHORT77"} {
		if _, err := validator.Validate(ctx, code); !errors.Is(err, models.ErrCouponInvalid) {
			t.Errorf("Validate(%s) err = %v, want ErrCouponInvalid", code, err)
		}
	}

	store.settings.DiscountPercent = 25
	if got, _ := validator.Validate(ctx, "HAPPYHRS"); got.DiscountPercent != 25 {
		t.Errorf("discount = %d, want the settings' 25", got.DiscountPercent)
	}

	store.published = false
	if _, err := validator.Validate(ctx, "HAPPYHRS"); !errors.Is(err, models.ErrCouponUnavailable) {
		t.Errorf("before the first build err = %v, want ErrCouponUnavailable", err)
	}

	store.published, store.err = true, errors.New("connection refused")
	if _, err := validator.Validate(ctx, "HAPPYHRS"); !errors.Is(err, models.ErrCouponUnavailable) {
		t.Errorf("with the database down err = %v, want ErrCouponUnavailable", err)
	}
}

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

func newMemory(t *testing.T) *cache.Memory {
	t.Helper()
	mem, err := cache.NewMemory(10)
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

// TestDiscountFollowsSettings checks that a cached valid code still takes the
// discount from the current settings.
func TestDiscountFollowsSettings(t *testing.T) {
	ctx := t.Context()
	store := &fakeStore{codes: map[string]bool{"HAPPYHRS": true}, published: true, settings: defaultSettings}
	lookup := NewCachedLookup(store, newMemory(t), time.Hour, time.Hour, slog.New(slog.DiscardHandler))
	validator := NewStore(lookup, store, time.Second)

	validator.Validate(ctx, "HAPPYHRS")
	store.settings.DiscountPercent = 15
	if got, err := validator.Validate(ctx, "HAPPYHRS"); err != nil || got.DiscountPercent != 15 {
		t.Errorf("Validate = %+v, %v; want the new 15%% discount", got, err)
	}
}

func TestStoreSkipsJunk(t *testing.T) {
	store := &fakeStore{err: errors.New("never called")}
	validator := NewStore(store, store, time.Second)
	for _, code := range []string{"", "BAD/../CODE", "ELEVENCHARS"} {
		if _, err := validator.Validate(t.Context(), code); !errors.Is(err, models.ErrCouponInvalid) {
			t.Errorf("Validate(%q) err = %v, want ErrCouponInvalid", code, err)
		}
	}
	if store.settingsCalls != 0 {
		t.Errorf("junk codes read the settings %d times", store.settingsCalls)
	}
}
