package datasources_test

import (
	"errors"
	"testing"
	"time"

	"github.com/sam97/oolio-kart/pkg/datasources"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/datasources/datasourcestest"
	"github.com/sam97/oolio-kart/pkg/models"
)

func TestOpenRejectsUnknownStores(t *testing.T) {
	for _, url := range []string{"", "mysql://localhost/kart", "://bad"} {
		if _, err := datasources.Open(t.Context(), datasources.Config{DatabaseURL: url}); err == nil {
			t.Errorf("Open(%q) succeeded", url)
		}
	}
}

func TestOpenIsLazy(t *testing.T) {
	stores, err := datasources.Open(t.Context(), datasources.Config{DatabaseURL: "postgres://kart:kart@127.0.0.1:1/kart?connect_timeout=1"})
	if err != nil {
		t.Fatalf("Open with the database down: %v", err)
	}
	defer stores.Close()
	if _, err := stores.Products.List(t.Context()); !errors.Is(err, models.ErrUnavailable) {
		t.Errorf("List err = %v, want ErrUnavailable", err)
	}
}

// TestCacheUntilBust checks that a cache configured in Open keeps answering
// coupon lookups from before a new build, until BustCache.
func TestCacheUntilBust(t *testing.T) {
	ctx := t.Context()
	stores := datasourcestest.Open(t, datasources.CacheConfig{MaxEntries: 10, CouponTTL: time.Hour, SettingsTTL: time.Hour})
	publish := func(codes ...string) {
		t.Helper()
		batch, err := stores.Coupons.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer batch.Abort()
		for _, code := range codes {
			batch.Add(code)
		}
		if err := batch.Commit(ctx, couponstore.Manifest{}); err != nil {
			t.Fatal(err)
		}
	}
	valid := func() bool {
		t.Helper()
		valid, _, err := stores.CouponLookup.Contains(ctx, "HAPPYHRS")
		if err != nil {
			t.Fatal(err)
		}
		return valid
	}

	publish("HAPPYHRS")
	if !valid() {
		t.Fatal("HAPPYHRS is not valid after publishing it")
	}
	publish("FIFTYOFF")
	if !valid() {
		t.Error("the cached answer was not used")
	}
	if err := stores.BustCache(ctx); err != nil {
		t.Fatal(err)
	}
	if valid() {
		t.Error("HAPPYHRS is still valid after BustCache")
	}
}
