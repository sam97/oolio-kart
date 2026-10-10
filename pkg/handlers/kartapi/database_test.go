package kartapi

import (
	"net/http"
	"testing"

	"github.com/sam97/oolio-kart/pkg/datasources"
	"github.com/sam97/oolio-kart/pkg/datasources/datasourcestest"
	"github.com/sam97/oolio-kart/pkg/services/orders"
	"github.com/sam97/oolio-kart/pkg/services/products"
)

// TestWithDatabase serves the catalogue and places an order through the real
// data stores.
func TestWithDatabase(t *testing.T) {
	stores := datasourcestest.Open(t, datasources.CacheConfig{})
	server := newTestServer(t, func(opts *Options) {
		opts.Products = products.NewService(stores.Products)
		opts.Orders = orders.NewService(stores.Products, stores.Orders, opts.Coupons)
	})

	rec := server.do(t, call{method: http.MethodGet, path: "/api/product"})
	if got := decode[[]map[string]any](t, rec); rec.Code != http.StatusOK || len(got) != 9 {
		t.Fatalf("GET /api/product = %d with %d products, want 200 with 9", rec.Code, len(got))
	}
	rec = server.do(t, call{method: http.MethodGet, path: "/api/product/7"})
	if rec.Code != http.StatusOK || decode[map[string]any](t, rec)["name"] != "Red Velvet Cake" {
		t.Errorf("GET /api/product/7 = %d %s", rec.Code, rec.Body)
	}

	rec = server.do(t, orderCall(`{"couponCode":"HAPPYHRS","items":[{"productId":"1","quantity":2},{"productId":"3","quantity":1}]}`))
	placed := decode[map[string]any](t, rec)
	if rec.Code != http.StatusOK || placed["total"] != 18.9 || placed["discounts"] != 2.1 {
		t.Errorf("POST /api/order = %d %s", rec.Code, rec.Body)
	}
	rec = server.do(t, orderCall(`{"items":[{"productId":"99","quantity":1}]}`))
	expectError(t, rec, http.StatusUnprocessableEntity, `items[0].productId: product "99" not found`)
}
