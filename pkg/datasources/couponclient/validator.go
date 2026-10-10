// Package couponclient validates promo codes against the coupons service.
package couponclient

import (
	"context"

	"github.com/sam97/oolio-kart/pkg/models"
)

type Validator interface {
	// Validate returns the coupon for code, models.ErrCouponInvalid if the
	// code is not a valid coupon, or an error wrapping
	// models.ErrCouponUnavailable.
	Validate(ctx context.Context, code string) (models.Coupon, error)
}
