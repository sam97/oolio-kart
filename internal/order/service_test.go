package order

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/sam97/oolio-kart/internal/couponclient"
	"github.com/sam97/oolio-kart/internal/money"
	"github.com/sam97/oolio-kart/internal/product"
)

type fakeCoupons struct {
	err   error
	calls int
}

func (f *fakeCoupons) Validate(_ context.Context, code string) (couponclient.Coupon, error) {
	f.calls++
	if f.err != nil {
		return couponclient.Coupon{}, f.err
	}
	return couponclient.Coupon{Code: code, DiscountPercent: 10}, nil
}

func newTestService(coupons couponclient.Validator) (*Service, *Memory) {
	products := product.NewMemory([]product.Product{
		{ID: "1", Name: "Waffle", Price: 650},
		{ID: "2", Name: "Brownie", Price: 455},
	})
	orders := NewMemory()
	service := NewService(products, orders, coupons)
	service.newID = func() (string, error) { return "order-1", nil }
	return service, orders
}

func TestPlacePricesOrder(t *testing.T) {
	service, orders := newTestService(&fakeCoupons{})

	req := Request{Items: []Item{{"1", 2}, {"2", 1}, {"1", 1}}}
	placed, err := service.Place(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if want := money.Cents(3*650 + 455); placed.Total != want || placed.Discounts != 0 {
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
	placed, err := service.Place(t.Context(), Request{CouponCode: "HAPPYHRS", Items: []Item{{"1", 3}, {"2", 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if placed.Discounts != 241 || placed.Total != 2405-241 {
		t.Errorf("discounts = %d, total = %d; want 241, %d", placed.Discounts, placed.Total, 2405-241)
	}
}

func TestPlaceValidation(t *testing.T) {
	many := make([]Item, MaxItems+1)
	for index := range many {
		many[index] = Item{"1", 1}
	}

	tests := []struct {
		name    string
		req     Request
		problem string
	}{
		{"no items", Request{}, "items: must contain at least one item"},
		{"empty items", Request{Items: []Item{}}, "items: must contain at least one item"},
		{"too many items", Request{Items: many}, "items: must contain at most 100 items"},
		{"missing product id", Request{Items: []Item{{"", 1}}}, "items[0].productId: is required"},
		{"zero quantity", Request{Items: []Item{{"1", 0}}}, "items[0].quantity: must be between 1 and 100"},
		{"negative quantity", Request{Items: []Item{{"1", -2}}}, "items[0].quantity: must be between 1 and 100"},
		{"huge quantity", Request{Items: []Item{{"1", MaxQuantity + 1}}}, "items[0].quantity: must be between 1 and 100"},
		{"unknown product", Request{Items: []Item{{"1", 1}, {"99", 1}}}, `items[1].productId: product "99" not found`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coupons := &fakeCoupons{}
			service, _ := newTestService(coupons)
			tt.req.CouponCode = "HAPPYHRS"

			_, err := service.Place(t.Context(), tt.req)
			var invalid *ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("err = %v, want *ValidationError", err)
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
	_, err := service.Place(t.Context(), Request{Items: []Item{{"", 0}, {"1", 1}, {"2", 200}}})
	if err == nil || strings.Count(err.Error(), ";") != 2 {
		t.Errorf("err = %v, want three problems", err)
	}
}

func TestPlaceCouponErrors(t *testing.T) {
	items := []Item{{"1", 1}}

	service, _ := newTestService(&fakeCoupons{err: couponclient.ErrInvalid})
	_, err := service.Place(t.Context(), Request{CouponCode: "SUPER100", Items: items})
	var invalid *ValidationError
	if !errors.As(err, &invalid) || invalid.Problems[0] != "couponCode: is not a valid coupon" {
		t.Errorf("invalid coupon err = %v", err)
	}

	service, orders := newTestService(&fakeCoupons{err: couponclient.ErrUnavailable})
	if _, err := service.Place(t.Context(), Request{CouponCode: "HAPPYHRS", Items: items}); !errors.Is(err, couponclient.ErrUnavailable) {
		t.Errorf("unavailable coupon err = %v", err)
	}
	if _, saved := orders.Get(t.Context(), "order-1"); saved {
		t.Error("order saved although the coupon could not be checked")
	}

	// No coupon means the coupons service is not needed at all.
	if _, err := service.Place(t.Context(), Request{Items: items}); err != nil {
		t.Errorf("order without coupon failed: %v", err)
	}
}
