// Package orders places orders: it validates them, prices them and applies
// coupons.
package orders

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/sam97/oolio-kart/pkg/datasources/orderstore"
	"github.com/sam97/oolio-kart/pkg/datasources/productstore"
	"github.com/sam97/oolio-kart/pkg/models"
	"github.com/sam97/oolio-kart/pkg/services/coupons/validator"
)

type Service struct {
	products productstore.Store
	orders   orderstore.Store
	coupons  validator.Validator
	newID    func() (string, error)
}

func NewService(products productstore.Store, orders orderstore.Store, coupons validator.Validator) *Service {
	return &Service{products: products, orders: orders, coupons: coupons, newID: newUUID}
}

func newUUID() (string, error) {
	id, err := uuid.NewV7()
	return id.String(), err
}

// Place validates, prices and stores an order. It returns a
// *models.ValidationError when the request is invalid, and an error wrapping
// models.ErrCouponUnavailable when a coupon was given but could not be checked.
//
// Checks run from cheapest to most expensive, so the coupons service is only
// asked once everything else is known to be valid.
func (s *Service) Place(ctx context.Context, req models.OrderRequest) (models.Order, error) {
	if problems := checkItems(req.Items); len(problems) > 0 {
		return models.Order{}, &models.ValidationError{Problems: problems}
	}

	var subtotal models.Cents
	var products []models.Product
	var problems []string
	seen := map[string]bool{}
	for index, item := range req.Items {
		found, err := s.products.Get(ctx, item.ProductID)
		if errors.Is(err, models.ErrProductNotFound) {
			problems = append(problems, fmt.Sprintf("items[%d].productId: product %q not found", index, item.ProductID))
			continue
		}
		if err != nil {
			return models.Order{}, err
		}
		subtotal += found.Price * models.Cents(item.Quantity)
		if !seen[found.ID] {
			seen[found.ID] = true
			products = append(products, found)
		}
	}
	if len(problems) > 0 {
		return models.Order{}, &models.ValidationError{Problems: problems}
	}

	var discounts models.Cents
	if req.CouponCode != "" {
		found, err := s.coupons.Validate(ctx, req.CouponCode)
		if errors.Is(err, models.ErrCouponInvalid) {
			return models.Order{}, &models.ValidationError{Problems: []string{"couponCode: is not a valid coupon"}}
		}
		if err != nil {
			return models.Order{}, err
		}
		discounts = subtotal.ApplyPercent(found.DiscountPercent)
	}

	id, err := s.newID()
	if err != nil {
		return models.Order{}, fmt.Errorf("generate order id: %w", err)
	}
	placed := models.Order{
		ID:        id,
		Total:     subtotal - discounts,
		Discounts: discounts,
		Items:     req.Items,
		Products:  products,
	}
	if err := s.orders.Save(ctx, placed); err != nil {
		return models.Order{}, fmt.Errorf("save order: %w", err)
	}
	return placed, nil
}

func checkItems(items []models.OrderItem) []string {
	if len(items) == 0 {
		return []string{"items: must contain at least one item"}
	}
	if len(items) > models.MaxOrderItems {
		return []string{fmt.Sprintf("items: must contain at most %d items", models.MaxOrderItems)}
	}
	var problems []string
	for index, item := range items {
		if item.ProductID == "" {
			problems = append(problems, fmt.Sprintf("items[%d].productId: is required", index))
		}
		if item.Quantity < 1 || item.Quantity > models.MaxItemQuantity {
			problems = append(problems, fmt.Sprintf("items[%d].quantity: must be between 1 and %d", index, models.MaxItemQuantity))
		}
	}
	return problems
}
