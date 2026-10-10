package postgres_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/sam97/oolio-kart/pkg/datasources/internal/datastores/postgres"
	"github.com/sam97/oolio-kart/pkg/datasources/internal/datastores/postgres/postgrestest"
	"github.com/sam97/oolio-kart/pkg/models"
)

func TestProducts(t *testing.T) {
	ctx := t.Context()
	products := postgres.NewProducts(postgrestest.New(t))

	// The migration seeds the demo catalogue, listed by id.
	all, err := products.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, product := range all {
		ids = append(ids, product.ID)
	}
	if want := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}; !slices.Equal(ids, want) {
		t.Fatalf("List ids = %v, want %v", ids, want)
	}
	waffle := models.Product{
		ID: "1", Name: "Waffle with Berries", Price: 650, Category: "Waffle",
		Image: models.Image{
			Thumbnail: "https://orderfoodonline.deno.dev/public/images/image-waffle-thumbnail.jpg",
			Mobile:    "https://orderfoodonline.deno.dev/public/images/image-waffle-mobile.jpg",
			Tablet:    "https://orderfoodonline.deno.dev/public/images/image-waffle-tablet.jpg",
			Desktop:   "https://orderfoodonline.deno.dev/public/images/image-waffle-desktop.jpg",
		},
	}
	if all[0] != waffle {
		t.Errorf("List[0] = %+v, want %+v", all[0], waffle)
	}

	if got, err := products.Get(ctx, "1"); err != nil || got != waffle {
		t.Errorf("Get(1) = %+v, %v", got, err)
	}
	for _, id := range []string{"10", "abc", "", "1.5"} {
		if _, err := products.Get(ctx, id); !errors.Is(err, models.ErrProductNotFound) {
			t.Errorf("Get(%q) err = %v, want ErrProductNotFound", id, err)
		}
	}

	found, err := products.GetMany(ctx, []string{"1", "3", "3", "10", "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(found)); !slices.Equal(got, []string{"1", "3"}) {
		t.Errorf("GetMany found %v, want [1 3]", got)
	}
	if found, err := products.GetMany(ctx, nil); err != nil || len(found) != 0 {
		t.Errorf("GetMany(nil) = %v, %v", found, err)
	}
}

func TestOrdersSave(t *testing.T) {
	ctx := t.Context()
	pool := postgrestest.New(t)
	orders := postgres.NewOrders(pool)
	waffle := models.Product{ID: "1", Price: 650}
	brownie := models.Product{ID: "8", Price: 550}

	order := models.Order{
		ID:         "01a125fa-6b84-70da-b03b-49981e57c71b",
		Total:      1665,
		Discounts:  185,
		Items:      []models.OrderItem{{ProductID: "1", Quantity: 2}, {ProductID: "8", Quantity: 1}},
		Products:   []models.Product{waffle, brownie},
		CouponCode: "HAPPYHRS",
	}
	if err := orders.Save(ctx, order); err != nil {
		t.Fatal(err)
	}
	var coupon string
	var total, discounts int64
	err := pool.QueryRow(ctx, "SELECT coupon_code, total_cents, discounts_cents FROM orders WHERE id = $1", order.ID).
		Scan(&coupon, &total, &discounts)
	if err != nil || coupon != "HAPPYHRS" || total != 1665 || discounts != 185 {
		t.Errorf("order row = %q, %d, %d, %v", coupon, total, discounts, err)
	}
	type line struct {
		product, quantity, price int64
	}
	rows, err := pool.Query(ctx, "SELECT product_id, quantity, unit_price_cents FROM order_items WHERE order_id = $1 ORDER BY line", order.ID)
	if err != nil {
		t.Fatal(err)
	}
	var lines []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.product, &l.quantity, &l.price); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, l)
	}
	if want := []line{{1, 2, 650}, {8, 1, 550}}; !slices.Equal(lines, want) {
		t.Errorf("order items = %v, want %v", lines, want)
	}

	// An order without a coupon stores null.
	plain := models.Order{ID: "01a125fa-6b84-70da-b03b-49981e57c71c", Total: 650, Items: order.Items[:1], Products: order.Products[:1]}
	if err := orders.Save(ctx, plain); err != nil {
		t.Fatal(err)
	}
	var isNull bool
	if err := pool.QueryRow(ctx, "SELECT coupon_code IS NULL FROM orders WHERE id = $1", plain.ID).Scan(&isNull); err != nil || !isNull {
		t.Errorf("coupon_code is null = %v, %v", isNull, err)
	}
}

// TestOrdersSaveIsAllOrNothing saves an order whose second item names a
// product missing from the catalogue: the foreign key fails it, and no part
// of the order is kept.
func TestOrdersSaveIsAllOrNothing(t *testing.T) {
	ctx := t.Context()
	pool := postgrestest.New(t)
	order := models.Order{
		ID:       "01a125fa-6b84-70da-b03b-49981e57c71b",
		Total:    1300,
		Items:    []models.OrderItem{{ProductID: "1", Quantity: 1}, {ProductID: "99", Quantity: 1}},
		Products: []models.Product{{ID: "1", Price: 650}, {ID: "99", Price: 650}},
	}
	if err := postgres.NewOrders(pool).Save(ctx, order); err == nil {
		t.Fatal("Save succeeded with an unknown product")
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM orders").Scan(&count); err != nil || count != 0 {
		t.Errorf("orders = %d, %v; want none", count, err)
	}
}

func TestErrorsWhenDown(t *testing.T) {
	pool, err := postgres.Open(t.Context(), "postgres://kart:kart@127.0.0.1:1/kart?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx := context.Background()
	if _, err := postgres.NewProducts(pool).List(ctx); !errors.Is(err, models.ErrUnavailable) {
		t.Errorf("List err = %v, want ErrUnavailable", err)
	}
	order := models.Order{ID: "01a125fa-6b84-70da-b03b-49981e57c71b", Items: []models.OrderItem{{ProductID: "1", Quantity: 1}}, Products: []models.Product{{ID: "1"}}}
	if err := postgres.NewOrders(pool).Save(ctx, order); !errors.Is(err, models.ErrUnavailable) {
		t.Errorf("Save err = %v, want ErrUnavailable", err)
	}
	if _, _, err := postgres.NewCoupons(pool).Contains(ctx, "HAPPYHRS"); !errors.Is(err, models.ErrUnavailable) {
		t.Errorf("Contains err = %v, want ErrUnavailable", err)
	}
}
