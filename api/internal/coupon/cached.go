package coupon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/sam97/oolio-kart/api/internal/cache"
)

// Cached remembers answers from another Validator. Valid and invalid answers
// are kept for separate TTLs; a TTL of 0 disables caching of that answer.
// Unavailable answers are never cached. Cache failures are logged and the
// lookup falls through to the wrapped Validator.
type Cached struct {
	next        Validator
	cache       cache.Cache
	ttl         time.Duration
	negativeTTL time.Duration
	logger      *slog.Logger
}

func NewCached(next Validator, store cache.Cache, ttl, negativeTTL time.Duration, logger *slog.Logger) *Cached {
	return &Cached{next: next, cache: store, ttl: ttl, negativeTTL: negativeTTL, logger: logger}
}

type cachedAnswer struct {
	Valid           bool `json:"valid"`
	DiscountPercent int  `json:"discountPercent,omitempty"`
}

func (c *Cached) Validate(ctx context.Context, code string) (Coupon, error) {
	if !WellFormed(code) {
		return Coupon{}, ErrInvalid
	}

	key := "coupon:" + code
	answer, found, err := cache.GetJSON[cachedAnswer](ctx, c.cache, key)
	if err != nil {
		c.logger.WarnContext(ctx, "read coupon cache", "err", err)
	}
	if found {
		if !answer.Valid {
			return Coupon{}, ErrInvalid
		}
		return Coupon{Code: code, DiscountPercent: answer.DiscountPercent}, nil
	}

	coupon, err := c.next.Validate(ctx, code)
	switch {
	case err == nil:
		c.store(ctx, key, cachedAnswer{Valid: true, DiscountPercent: coupon.DiscountPercent}, c.ttl)
	case errors.Is(err, ErrInvalid):
		c.store(ctx, key, cachedAnswer{Valid: false}, c.negativeTTL)
	}
	return coupon, err
}

func (c *Cached) store(ctx context.Context, key string, answer cachedAnswer, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	if err := cache.SetJSON(ctx, c.cache, key, answer, ttl); err != nil {
		c.logger.WarnContext(ctx, "write coupon cache", "err", err)
	}
}
