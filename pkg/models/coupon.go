package models

import "errors"

const (
	CouponMinLength = 8
	CouponMaxLength = 10
)

var (
	// ErrCouponInvalid means the code is not a valid coupon.
	ErrCouponInvalid = errors.New("invalid coupon code")

	// ErrCouponUnavailable means validity could not be determined right now,
	// e.g. the coupons service is down or still loading.
	ErrCouponUnavailable = errors.New("coupon service unavailable")
)

type Coupon struct {
	Code            string `json:"code"`
	DiscountPercent int    `json:"discountPercent"`
}

// WellFormedCoupon reports whether code could be a coupon at all: 8 to 10
// ASCII letters or digits. Anything else is invalid without asking the
// coupons service.
func WellFormedCoupon(code string) bool {
	if len(code) < CouponMinLength || len(code) > CouponMaxLength {
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
