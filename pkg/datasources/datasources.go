// Package datasources opens the app's data stores. It is the only place that
// knows what is behind each store interface: the implementations live in
// internal/datastores, which nothing outside this tree can import.
//
//	services ──▶ productstore.Store, orderstore.Store, couponstore.*, couponsource.Reader
//	                 ▲ built by Open and CouponFiles
//	internal/datastores: postgres (tables), memory (cache), files (coupon base files)
package datasources

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sam97/oolio-kart/pkg/datasources/cache"
	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/datasources/internal/datastores/files"
	"github.com/sam97/oolio-kart/pkg/datasources/internal/datastores/memory"
	"github.com/sam97/oolio-kart/pkg/datasources/internal/datastores/postgres"
	"github.com/sam97/oolio-kart/pkg/datasources/orderstore"
	"github.com/sam97/oolio-kart/pkg/datasources/productstore"
)

type Config struct {
	// DatabaseURL locates the database; its scheme picks the store.
	// postgres:// and postgresql:// are supported.
	DatabaseURL string

	// Cache caches coupon lookups and settings. The zero value caches
	// nothing.
	Cache CacheConfig

	Logger *slog.Logger
}

// CacheConfig sizes the cache in front of the coupon store. A TTL of 0
// disables caching of that kind of answer.
type CacheConfig struct {
	MaxEntries        int
	CouponTTL         time.Duration
	CouponNegativeTTL time.Duration
	SettingsTTL       time.Duration
}

// Stores are the app's data stores. Callers use only the interfaces.
type Stores struct {
	Products productstore.Store
	Orders   orderstore.Store

	// Coupons publishes coupon builds; CouponLookup and CouponSettings
	// answer from the published ones, through the cache if configured.
	Coupons        couponstore.Store
	CouponLookup   couponstore.Lookup
	CouponSettings couponstore.Settings
	BuildLock      couponstore.Locker

	pool  *pgxpool.Pool
	cache cache.Cache // nil without a cache
}

// Open returns the stores for cfg. It connects lazily: a database that is
// down makes calls fail with models.ErrUnavailable, not Open.
func Open(ctx context.Context, cfg Config) (*Stores, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	parsed, err := url.Parse(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return nil, fmt.Errorf("database URL scheme %q is not supported, want postgres", parsed.Scheme)
	}

	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	coupons := postgres.NewCoupons(pool)
	stores := &Stores{
		Products:       postgres.NewProducts(pool),
		Orders:         postgres.NewOrders(pool),
		Coupons:        coupons,
		CouponLookup:   coupons,
		CouponSettings: coupons,
		BuildLock:      postgres.NewBuildLock(pool),
		pool:           pool,
	}

	if cfg.Cache != (CacheConfig{}) {
		entries, err := memory.NewCache(cfg.Cache.MaxEntries)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("create cache: %w", err)
		}
		stores.cache = entries
		stores.CouponLookup = couponstore.NewCachedLookup(coupons, entries, cfg.Cache.CouponTTL, cfg.Cache.CouponNegativeTTL, cfg.Logger)
		stores.CouponSettings = couponstore.NewCachedSettings(coupons, entries, cfg.Cache.SettingsTTL, cfg.Logger)
	}
	return stores, nil
}

// Migrate brings the database schema up to date.
func (s *Stores) Migrate(ctx context.Context) error {
	return postgres.Migrate(ctx, s.pool)
}

// BustCache empties the cache, so the next coupon lookups and settings are
// read from the database. Without a cache it does nothing.
func (s *Stores) BustCache(ctx context.Context) error {
	if s.cache == nil {
		return nil
	}
	return s.cache.Purge(ctx)
}

func (s *Stores) Close() {
	s.pool.Close()
}

// CouponFiles reads the coupon base files in dir: every regular file whose
// name does not start with a dot, as plain text or gzip.
func CouponFiles(dir string) couponsource.Opener {
	return func(memory int64) couponsource.Reader {
		return files.NewFS(dir, memory, files.NewGzip())
	}
}
