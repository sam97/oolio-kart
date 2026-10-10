package models

import "testing"

func TestWellFormedCoupon(t *testing.T) {
	for _, code := range []string{"A", "SHORT77", "HAPPYHRS", "HAPPYHOURS", "happyhrs", "12345678"} {
		if !WellFormedCoupon(code) {
			t.Errorf("WellFormedCoupon(%q) = false", code)
		}
	}
	for _, code := range []string{"", "ELEVENCHARS", "HAPPY HR", "HAPPY-HR", "HAPPYHR/", "HAPPYHRÉ"} {
		if WellFormedCoupon(code) {
			t.Errorf("WellFormedCoupon(%q) = true", code)
		}
	}
}

func TestCouponSettingsWellFormed(t *testing.T) {
	settings := CouponSettings{MinLength: 8, MaxLength: 10}
	for _, code := range []string{"HAPPYHRS", "HAPPYHOURS", "happyhrs", "12345678", "ABCDEFGHI"} {
		if !settings.WellFormed(code) {
			t.Errorf("WellFormed(%q) = false", code)
		}
	}
	for _, code := range []string{"", "SHORT77", "ELEVENCHARS", "HAPPY HR", "HAPPYHR/"} {
		if settings.WellFormed(code) {
			t.Errorf("WellFormed(%q) = true", code)
		}
	}
}
