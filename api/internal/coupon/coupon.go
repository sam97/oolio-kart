// Package coupon validates promo codes against the coupons service.
package coupon

import (
	"context"
	"errors"

	coupons "github.com/sam97/oolio-kart/coupons-service"
)

var (
	// ErrInvalid means the code is not a valid coupon.
	ErrInvalid = errors.New("invalid coupon code")

	// ErrUnavailable means validity could not be determined right now, e.g.
	// the coupons service is down or still loading.
	ErrUnavailable = errors.New("coupon service unavailable")
)

type Coupon struct {
	Code            string `json:"code"`
	DiscountPercent int    `json:"discountPercent"`
}

type Validator interface {
	// Validate returns the coupon for code, ErrInvalid if the code is not a
	// valid coupon, or an error wrapping ErrUnavailable.
	Validate(ctx context.Context, code string) (Coupon, error)
}

// WellFormed reports whether code could be a coupon at all: 8 to 10 ASCII
// letters or digits. Anything else is invalid without asking the service.
func WellFormed(code string) bool {
	if len(code) < coupons.MinLength || len(code) > coupons.MaxLength {
		return false
	}
	for _, char := range []byte(code) {
		isDigit := char >= '0' && char <= '9'
		isLetter := (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z')
		if !isDigit && !isLetter {
			return false
		}
	}
	return true
}
