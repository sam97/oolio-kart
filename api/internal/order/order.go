// Package order places orders: it validates them, prices them and applies
// coupons.
package order

import (
	"context"
	"strings"

	"github.com/sam97/oolio-kart/api/internal/money"
	"github.com/sam97/oolio-kart/api/internal/product"
)

const (
	MaxItems    = 100
	MaxQuantity = 100
)

type Item struct {
	ProductID string `json:"productId"`
	Quantity  int    `json:"quantity"`
}

type Request struct {
	CouponCode string `json:"couponCode"`
	Items      []Item `json:"items"`
}

type Order struct {
	ID        string            `json:"id"`
	Total     money.Cents       `json:"total"`
	Discounts money.Cents       `json:"discounts"`
	Items     []Item            `json:"items"`
	Products  []product.Product `json:"products"`
}

type Store interface {
	Save(ctx context.Context, order Order) error
}

// ValidationError lists everything wrong with a Request.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return strings.Join(e.Problems, "; ")
}
