package orders

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/sam97/oolio-kart/pkg/datasources/orderstore"
	"github.com/sam97/oolio-kart/pkg/datasources/productstore"
	"github.com/sam97/oolio-kart/pkg/models"
	"github.com/sam97/oolio-kart/pkg/services/coupons/validator"
)

type fakeCoupons struct {
	err   error
	calls int
}

func (f *fakeCoupons) Validate(_ context.Context, code string) (models.Coupon, error) {
	f.calls++
	if f.err != nil {
		return models.Coupon{}, f.err
	}
	return models.Coupon{Code: code, DiscountPercent: 10}, nil
}

func newTestService(coupons validator.Validator) (*Service, *orderstore.Memory) {
	products := productstore.NewMemory([]models.Product{
		{ID: "1", Name: "Waffle", Price: 650},
		{ID: "2", Name: "Brownie", Price: 455},
	})
	orders := orderstore.NewMemory()
	service := NewService(products, orders, coupons)
	service.newID = func() (string, error) { return "order-1", nil }
	return service, orders
}

func TestPlacePricesOrder(t *testing.T) {
	service, orders := newTestService(&fakeCoupons{})

	req := models.OrderRequest{Items: []models.OrderItem{item("1", 2), item("2", 1), item("1", 1)}}
	placed, err := service.Place(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if want := models.Cents(3*650 + 455); placed.Total != want || placed.Discounts != 0 {
		t.Errorf("total = %d, discounts = %d; want %d, 0", placed.Total, placed.Discounts, want)
	}
	if !slices.Equal(placed.Items, req.Items) {
		t.Errorf("items = %v, want %v", placed.Items, req.Items)
	}
	var ids []string
	for _, item := range placed.Products {
		ids = append(ids, item.ID)
	}
	if !slices.Equal(ids, []string{"1", "2"}) {
		t.Errorf("product ids = %v, want unique ids in first-seen order", ids)
	}
	if _, saved := orders.Get(t.Context(), "order-1"); !saved {
		t.Error("order was not saved")
	}
}

func TestPlaceAppliesCoupon(t *testing.T) {
	service, _ := newTestService(&fakeCoupons{})

	// subtotal 2405: 10% is 240.5, rounded half up to 241.
	placed, err := service.Place(t.Context(), models.OrderRequest{CouponCode: "HAPPYHRS", Items: []models.OrderItem{item("1", 3), item("2", 1)}})
	if err != nil {
		t.Fatal(err)
	}
	if placed.Discounts != 241 || placed.Total != 2405-241 {
		t.Errorf("discounts = %d, total = %d; want 241, %d", placed.Discounts, placed.Total, 2405-241)
	}
}

func TestPlaceValidation(t *testing.T) {
	many := make([]models.OrderItem, models.MaxOrderItems+1)
	for index := range many {
		many[index] = item("1", 1)
	}

	tests := []struct {
		name    string
		req     models.OrderRequest
		problem string
	}{
		{"no items", models.OrderRequest{}, "items: must contain at least one item"},
		{"empty items", models.OrderRequest{Items: []models.OrderItem{}}, "items: must contain at least one item"},
		{"too many items", models.OrderRequest{Items: many}, "items: must contain at most 100 items"},
		{"missing product id", models.OrderRequest{Items: []models.OrderItem{item("", 1)}}, "items[0].productId: is required"},
		{"zero quantity", models.OrderRequest{Items: []models.OrderItem{item("1", 0)}}, "items[0].quantity: must be between 1 and 100"},
		{"negative quantity", models.OrderRequest{Items: []models.OrderItem{item("1", -2)}}, "items[0].quantity: must be between 1 and 100"},
		{"huge quantity", models.OrderRequest{Items: []models.OrderItem{item("1", models.MaxItemQuantity+1)}}, "items[0].quantity: must be between 1 and 100"},
		{"unknown product", models.OrderRequest{Items: []models.OrderItem{item("1", 1), item("99", 1)}}, `items[1].productId: product "99" not found`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coupons := &fakeCoupons{}
			service, _ := newTestService(coupons)
			tt.req.CouponCode = "HAPPYHRS"

			_, err := service.Place(t.Context(), tt.req)
			var invalid *models.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("err = %v, want *models.ValidationError", err)
			}
			if !slices.Contains(invalid.Problems, tt.problem) {
				t.Errorf("problems = %q, want %q", invalid.Problems, tt.problem)
			}
			if coupons.calls != 0 {
				t.Error("coupon checked although the order was already invalid")
			}
		})
	}
}

func TestPlaceReportsAllItemProblems(t *testing.T) {
	service, _ := newTestService(&fakeCoupons{})
	_, err := service.Place(t.Context(), models.OrderRequest{Items: []models.OrderItem{item("", 0), item("1", 1), item("2", 200)}})
	if err == nil || strings.Count(err.Error(), ";") != 2 {
		t.Errorf("err = %v, want three problems", err)
	}
}

func TestPlaceCouponErrors(t *testing.T) {
	items := []models.OrderItem{item("1", 1)}

	service, _ := newTestService(&fakeCoupons{err: models.ErrCouponInvalid})
	_, err := service.Place(t.Context(), models.OrderRequest{CouponCode: "SUPER100", Items: items})
	var invalid *models.ValidationError
	if !errors.As(err, &invalid) || invalid.Problems[0] != "couponCode: is not a valid coupon" {
		t.Errorf("invalid coupon err = %v", err)
	}

	service, orders := newTestService(&fakeCoupons{err: models.ErrCouponUnavailable})
	if _, err := service.Place(t.Context(), models.OrderRequest{CouponCode: "HAPPYHRS", Items: items}); !errors.Is(err, models.ErrCouponUnavailable) {
		t.Errorf("unavailable coupon err = %v", err)
	}
	if _, saved := orders.Get(t.Context(), "order-1"); saved {
		t.Error("order saved although the coupon could not be checked")
	}

	// No coupon means the coupons service is not needed at all.
	if _, err := service.Place(t.Context(), models.OrderRequest{Items: items}); err != nil {
		t.Errorf("order without coupon failed: %v", err)
	}
}

func item(productID string, quantity int) models.OrderItem {
	return models.OrderItem{ProductID: productID, Quantity: quantity}
}
