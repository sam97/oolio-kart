package models

import "testing"

func TestWellFormedCoupon(t *testing.T) {
	for _, code := range []string{"HAPPYHRS", "HAPPYHOURS", "happyhrs", "12345678", "ABCDEFGHI"} {
		if !WellFormedCoupon(code) {
			t.Errorf("WellFormedCoupon(%q) = false", code)
		}
	}
	for _, code := range []string{"", "SHORT77", "ELEVENCHARS", "HAPPY HR", "HAPPY-HR", "HAPPYHR/", "HAPPYHRÉ"} {
		if WellFormedCoupon(code) {
			t.Errorf("WellFormedCoupon(%q) = true", code)
		}
	}
}
