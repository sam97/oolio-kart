// Package validator answers whether a promo code is a valid coupon, from the
// codes and settings coupons-job publishes.
package validator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/models"
)

type Validator interface {
	// Validate returns the coupon for code, models.ErrCouponInvalid if the
	// code is not a valid coupon, or an error wrapping
	// models.ErrCouponUnavailable.
	Validate(ctx context.Context, code string) (models.Coupon, error)
}

// errNotBuilt means coupons-job has not published any codes yet.
var errNotBuilt = errors.New("no coupons have been built yet")

// Store validates codes against the published codes, with the discount from
// the settings. datasources.Open caches lookups and settings when configured;
// the discount then follows the settings cache, not the codes cache.
type Store struct {
	lookup   couponstore.Lookup
	settings couponstore.Settings
	timeout  time.Duration
}

// NewStore bounds each lookup, settings included, by timeout.
func NewStore(lookup couponstore.Lookup, settings couponstore.Settings, timeout time.Duration) *Store {
	return &Store{lookup: lookup, settings: settings, timeout: timeout}
}

func (s *Store) Validate(ctx context.Context, code string) (models.Coupon, error) {
	// Codes no settings could allow are invalid without a lookup, and never
	// become cache keys.
	if !models.WellFormedCoupon(code) {
		return models.Coupon{}, models.ErrCouponInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	settings, err := s.settings.Settings(ctx)
	if err != nil {
		return models.Coupon{}, fmt.Errorf("%w: read settings: %w", models.ErrCouponUnavailable, err)
	}
	if !settings.WellFormed(code) {
		return models.Coupon{}, models.ErrCouponInvalid
	}
	valid, published, err := s.lookup.Contains(ctx, code)
	switch {
	case err != nil:
		return models.Coupon{}, fmt.Errorf("%w: %w", models.ErrCouponUnavailable, err)
	case !published:
		return models.Coupon{}, fmt.Errorf("%w: %w", models.ErrCouponUnavailable, errNotBuilt)
	case !valid:
		return models.Coupon{}, models.ErrCouponInvalid
	}
	return models.Coupon{Code: code, DiscountPercent: settings.DiscountPercent}, nil
}
