package models

import "errors"

// CouponMaxLength is the longest code any CouponSettings may allow: codes are
// packed into 64 bits while they are counted.
const CouponMaxLength = 10

var (
	// ErrCouponInvalid means the code is not a valid coupon.
	ErrCouponInvalid = errors.New("invalid coupon code")

	// ErrCouponUnavailable means validity could not be determined right now,
	// e.g. the database is down or no coupons have been built yet.
	ErrCouponUnavailable = errors.New("coupon service unavailable")
)

type Coupon struct {
	Code            string `json:"code"`
	DiscountPercent int    `json:"discountPercent"`
}

// CouponSettings are the rules for valid coupons, kept in the database. A
// code is valid when it is MinLength to MaxLength letters or digits and
// appears in at least MinFiles coupon base files.
type CouponSettings struct {
	MinLength       int `json:"minLength"`
	MaxLength       int `json:"maxLength"`
	MinFiles        int `json:"minFiles"`
	DiscountPercent int `json:"discountPercent"`
}

// WellFormed reports whether code has the length and characters the settings
// allow. Anything else is invalid without looking it up.
func (s CouponSettings) WellFormed(code string) bool {
	return len(code) >= s.MinLength && len(code) <= s.MaxLength && alphanumeric(code)
}

// WellFormedCoupon reports whether code could be a coupon under any settings:
// 1 to CouponMaxLength ASCII letters or digits.
func WellFormedCoupon(code string) bool {
	return len(code) >= 1 && len(code) <= CouponMaxLength && alphanumeric(code)
}

func alphanumeric(code string) bool {
	for _, char := range []byte(code) {
		isDigit := char >= '0' && char <= '9'
		isLetter := (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z')
		if !isDigit && !isLetter {
			return false
		}
	}
	return true
}
