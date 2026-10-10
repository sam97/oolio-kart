package couponstore

import (
	"context"
	"log/slog"
	"time"

	"github.com/sam97/oolio-kart/pkg/datasources/cache"
	"github.com/sam97/oolio-kart/pkg/models"
)

// CachedLookup remembers whether codes are valid. Valid and invalid answers
// are kept for separate TTLs; a TTL of 0 disables caching of that answer.
// Answers from before the first build, and failures, are never cached. Cache
// failures are logged and the lookup falls through.
type CachedLookup struct {
	next        Lookup
	cache       cache.Cache
	ttl         time.Duration
	negativeTTL time.Duration
	logger      *slog.Logger
}

func NewCachedLookup(next Lookup, store cache.Cache, ttl, negativeTTL time.Duration, logger *slog.Logger) *CachedLookup {
	return &CachedLookup{next: next, cache: store, ttl: ttl, negativeTTL: negativeTTL, logger: logger}
}

func (c *CachedLookup) Contains(ctx context.Context, code string) (valid, published bool, err error) {
	key := "coupon:" + code
	valid, found, err := cache.GetJSON[bool](ctx, c.cache, key)
	if err != nil {
		c.logger.WarnContext(ctx, "read coupon cache", "err", err)
	}
	if found {
		return valid, true, nil
	}

	valid, published, err = c.next.Contains(ctx, code)
	if err != nil || !published {
		return valid, published, err
	}
	ttl := c.ttl
	if !valid {
		ttl = c.negativeTTL
	}
	if ttl > 0 {
		if err := cache.SetJSON(ctx, c.cache, key, valid, ttl); err != nil {
			c.logger.WarnContext(ctx, "write coupon cache", "err", err)
		}
	}
	return valid, true, nil
}

// Bust empties the whole cache, so the next lookups read the published codes
// again. A cache shared with CachedSettings loses the settings too.
func (c *CachedLookup) Bust(ctx context.Context) error {
	return c.cache.Purge(ctx)
}

// CachedSettings remembers the coupon settings for ttl; a ttl of 0 disables
// caching. Cache failures are logged and the settings are read again.
type CachedSettings struct {
	next   Settings
	cache  cache.Cache
	ttl    time.Duration
	logger *slog.Logger
}

func NewCachedSettings(next Settings, store cache.Cache, ttl time.Duration, logger *slog.Logger) *CachedSettings {
	return &CachedSettings{next: next, cache: store, ttl: ttl, logger: logger}
}

const settingsKey = "coupon:settings"

func (c *CachedSettings) Settings(ctx context.Context) (models.CouponSettings, error) {
	settings, found, err := cache.GetJSON[models.CouponSettings](ctx, c.cache, settingsKey)
	if err != nil {
		c.logger.WarnContext(ctx, "read settings cache", "err", err)
	}
	if found {
		return settings, nil
	}
	settings, err = c.next.Settings(ctx)
	if err != nil || c.ttl <= 0 {
		return settings, err
	}
	if err := cache.SetJSON(ctx, c.cache, settingsKey, settings, c.ttl); err != nil {
		c.logger.WarnContext(ctx, "write settings cache", "err", err)
	}
	return settings, nil
}
