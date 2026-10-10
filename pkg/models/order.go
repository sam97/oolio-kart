package models

import "strings"

const (
	MaxOrderItems   = 100
	MaxItemQuantity = 100
)

type OrderItem struct {
	ProductID string `json:"productId"`
	Quantity  int    `json:"quantity"`
}

type OrderRequest struct {
	CouponCode string      `json:"couponCode"`
	Items      []OrderItem `json:"items"`
}

type Order struct {
	ID        string      `json:"id"`
	Total     Cents       `json:"total"`
	Discounts Cents       `json:"discounts"`
	Items     []OrderItem `json:"items"`
	Products  []Product   `json:"products"`

	// CouponCode is the coupon the order used, if any. It is stored with the
	// order but not part of the API response.
	CouponCode string `json:"-"`
}

// ValidationError lists everything wrong with an OrderRequest.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return strings.Join(e.Problems, "; ")
}
