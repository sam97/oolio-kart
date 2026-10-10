package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/datasources/internal/datastores/postgres"
	"github.com/sam97/oolio-kart/pkg/datasources/internal/datastores/postgres/postgrestest"
	"github.com/sam97/oolio-kart/pkg/models"
)

var modTime = time.Date(2026, 10, 10, 9, 30, 0, 123456789, time.UTC)

func manifestFor(rules string, names ...string) couponstore.Manifest {
	m := couponstore.Manifest{Fingerprint: couponstore.Fingerprint{Rules: rules}, Layout: couponstore.Layout{Hash: "splitmix64", Buckets: 1}}
	for _, name := range names {
		m.Sources = append(m.Sources, couponsource.Info{Name: name, Size: 100, ModTime: modTime})
	}
	return m
}

func publish(t *testing.T, store *postgres.Coupons, manifest couponstore.Manifest, codes ...string) {
	t.Helper()
	batch, err := store.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer batch.Abort()
	for _, code := range codes {
		if err := batch.Add(code); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Commit(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
}

func contains(t *testing.T, store *postgres.Coupons, code string) (valid, published bool) {
	t.Helper()
	valid, published, err := store.Contains(t.Context(), code)
	if err != nil {
		t.Fatal(err)
	}
	return valid, published
}

func TestPostgresPublish(t *testing.T) {
	pool := postgrestest.New(t)
	store := postgres.NewCoupons(pool)
	ctx := t.Context()

	if _, found, err := store.Published(ctx); err != nil || found {
		t.Fatalf("Published before any build = %v, %v", found, err)
	}
	if valid, published := contains(t, store, "HAPPYHRS"); valid || published {
		t.Fatalf("Contains before any build = %v, %v", valid, published)
	}

	first := manifestFor("rules-1", "a.gz", "b.gz")
	publish(t, store, first, "HAPPYHRS", "FIFTYOFF", "HAPPYHRS")
	fp, found, err := store.Published(ctx)
	if err != nil || !found || !fp.Equal(first.Fingerprint) {
		t.Fatalf("Published = %+v, %v, %v; want %+v", fp, found, err, first.Fingerprint)
	}
	if valid, published := contains(t, store, "HAPPYHRS"); !valid || !published {
		t.Errorf("HAPPYHRS = %v, %v after publishing", valid, published)
	}

	// A second build replaces the codes and the manifest.
	second := manifestFor("rules-1", "a.gz")
	publish(t, store, second, "SIXTYOFF")
	if valid, _ := contains(t, store, "HAPPYHRS"); valid {
		t.Error("HAPPYHRS is still valid after it was replaced")
	}
	if valid, _ := contains(t, store, "SIXTYOFF"); !valid {
		t.Error("SIXTYOFF is not valid")
	}
	if fp, _, _ := store.Published(ctx); !fp.Equal(second.Fingerprint) {
		t.Errorf("Published = %+v, want %+v", fp, second.Fingerprint)
	}

	var layout couponstore.Layout
	var raw []byte
	if err := pool.QueryRow(ctx, "SELECT layout FROM coupon_manifest").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &layout); err != nil || layout.Hash != "splitmix64" {
		t.Errorf("layout = %s, %v", raw, err)
	}
}

func TestPostgresAbort(t *testing.T) {
	pool := postgrestest.New(t)
	store := postgres.NewCoupons(pool)
	publish(t, store, manifestFor("rules", "a.gz"), "HAPPYHRS")

	batch, err := store.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// More than one flush, so some codes reach the database before Abort.
	for i := range 4096 + 10 {
		if err := batch.Add(fmt.Sprintf("CODE%04d", i)); err != nil {
			t.Fatal(err)
		}
	}
	batch.Abort()

	if valid, _ := contains(t, store, "HAPPYHRS"); !valid {
		t.Error("Abort lost the published codes")
	}
	if valid, _ := contains(t, store, "CODE0001"); valid {
		t.Error("an aborted code is visible")
	}
}

// TestPostgresReadersNeverSeeAMix publishes over and over while readers look
// codes up: each reader sees one whole set, never none or both.
func TestPostgresReadersNeverSeeAMix(t *testing.T) {
	pool := postgrestest.New(t)
	store := postgres.NewCoupons(pool)
	publish(t, store, manifestFor("rules", "a.gz"), "SETAAAA1", "SETAAAA2")

	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	var reads atomic.Int64
	for range 4 {
		wg.Go(func() {
			for ctx.Err() == nil {
				var inA, inB int
				err := pool.QueryRow(ctx, `SELECT
					count(*) FILTER (WHERE code LIKE 'SETA%'),
					count(*) FILTER (WHERE code LIKE 'SETB%')
					FROM coupon_codes`).Scan(&inA, &inB)
				if err != nil {
					if ctx.Err() == nil {
						t.Error(err)
					}
					return
				}
				if !(inA == 2 && inB == 0 || inA == 0 && inB == 2) {
					t.Errorf("a reader saw %d codes of set A and %d of set B", inA, inB)
				}
				reads.Add(1)
			}
		})
	}
	for i := range 20 {
		if i%2 == 0 {
			publish(t, store, manifestFor("rules", "b.gz"), "SETBBBB1", "SETBBBB2")
		} else {
			publish(t, store, manifestFor("rules", "a.gz"), "SETAAAA1", "SETAAAA2")
		}
	}
	cancel()
	wg.Wait()
	if reads.Load() == 0 {
		t.Error("no reads ran")
	}
}

func TestPostgresSettings(t *testing.T) {
	pool := postgrestest.New(t)
	store := postgres.NewCoupons(pool)
	got, err := store.Settings(t.Context())
	want := models.CouponSettings{MinLength: 8, MaxLength: 10, MinFiles: 2, DiscountPercent: 10}
	if err != nil || got != want {
		t.Errorf("Settings = %+v, %v; want %+v", got, err, want)
	}

	// The table rejects settings the build cannot work with.
	for _, update := range []string{
		"UPDATE coupon_settings SET max_length = 11",
		"UPDATE coupon_settings SET min_length = 11",
		"UPDATE coupon_settings SET min_files = 0",
		"UPDATE coupon_settings SET discount_percent = 101",
	} {
		if _, err := pool.Exec(t.Context(), update); err == nil {
			t.Errorf("%s succeeded", update)
		}
	}
}
