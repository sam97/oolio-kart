package validator

import (
	"context"
	"errors"
	"testing"
	"time"

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
