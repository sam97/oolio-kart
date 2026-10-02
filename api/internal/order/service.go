package order

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/sam97/oolio-kart/api/internal/coupon"
	"github.com/sam97/oolio-kart/api/internal/money"
	"github.com/sam97/oolio-kart/api/internal/product"
)

type Service struct {
	products product.Store
	orders   Store
	coupons  coupon.Validator
	newID    func() (string, error)
}

func NewService(products product.Store, orders Store, coupons coupon.Validator) *Service {
	return &Service{products: products, orders: orders, coupons: coupons, newID: newUUID}
}

func newUUID() (string, error) {
	id, err := uuid.NewV7()
	return id.String(), err
}

// Place validates, prices and stores an order. It returns a *ValidationError
// when the request is invalid, and an error wrapping coupon.ErrUnavailable
// when a coupon was given but could not be checked.
//
// Checks run from cheapest to most expensive, so the coupons service is only
// asked once everything else is known to be valid.
func (s *Service) Place(ctx context.Context, req Request) (Order, error) {
	if problems := checkItems(req.Items); len(problems) > 0 {
		return Order{}, &ValidationError{Problems: problems}
	}

	var subtotal money.Cents
	var products []product.Product
	var problems []string
	seen := map[string]bool{}
	for index, item := range req.Items {
		found, err := s.products.Get(ctx, item.ProductID)
		if errors.Is(err, product.ErrNotFound) {
			problems = append(problems, fmt.Sprintf("items[%d].productId: product %q not found", index, item.ProductID))
			continue
		}
		if err != nil {
			return Order{}, err
		}
		subtotal += found.Price * money.Cents(item.Quantity)
		if !seen[found.ID] {
			seen[found.ID] = true
			products = append(products, found)
		}
	}
	if len(problems) > 0 {
		return Order{}, &ValidationError{Problems: problems}
	}

	var discounts money.Cents
	if req.CouponCode != "" {
		found, err := s.coupons.Validate(ctx, req.CouponCode)
		if errors.Is(err, coupon.ErrInvalid) {
			return Order{}, &ValidationError{Problems: []string{"couponCode: is not a valid coupon"}}
		}
		if err != nil {
			return Order{}, err
		}
		discounts = subtotal.ApplyPercent(found.DiscountPercent)
	}

	id, err := s.newID()
	if err != nil {
		return Order{}, fmt.Errorf("generate order id: %w", err)
	}
	placed := Order{
		ID:        id,
		Total:     subtotal - discounts,
		Discounts: discounts,
		Items:     req.Items,
		Products:  products,
	}
	if err := s.orders.Save(ctx, placed); err != nil {
		return Order{}, fmt.Errorf("save order: %w", err)
	}
	return placed, nil
}

func checkItems(items []Item) []string {
	if len(items) == 0 {
		return []string{"items: must contain at least one item"}
	}
	if len(items) > MaxItems {
		return []string{fmt.Sprintf("items: must contain at most %d items", MaxItems)}
	}
	var problems []string
	for index, item := range items {
		if item.ProductID == "" {
			problems = append(problems, fmt.Sprintf("items[%d].productId: is required", index))
		}
		if item.Quantity < 1 || item.Quantity > MaxQuantity {
			problems = append(problems, fmt.Sprintf("items[%d].quantity: must be between 1 and %d", index, MaxQuantity))
		}
	}
	return problems
}
