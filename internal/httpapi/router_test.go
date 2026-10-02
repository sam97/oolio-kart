package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sam97/oolio-kart/internal/couponclient"
	"github.com/sam97/oolio-kart/internal/order"
	"github.com/sam97/oolio-kart/internal/product"
)

// fakeCoupons knows HAPPYHRS, and fails every lookup while down is set.
type fakeCoupons struct {
	down atomic.Bool
}

func (f *fakeCoupons) Validate(_ context.Context, code string) (couponclient.Coupon, error) {
	if f.down.Load() {
		return couponclient.Coupon{}, couponclient.ErrUnavailable
	}
	if code != "HAPPYHRS" {
		return couponclient.Coupon{}, couponclient.ErrInvalid
	}
	return couponclient.Coupon{Code: code, DiscountPercent: 10}, nil
}

type testServer struct {
	handler http.Handler
	coupons *fakeCoupons
	ready   *atomic.Bool
}

func newTestServer(t *testing.T, configure ...func(*Options)) *testServer {
	t.Helper()
	coupons := &fakeCoupons{}
	products := product.NewMemory(product.Demo())
	ready := &atomic.Bool{}
	ready.Store(true)

	opts := Options{
		Logger:             slog.New(slog.DiscardHandler),
		Products:           products,
		Orders:             order.NewService(products, order.NewMemory(), coupons),
		Coupons:            coupons,
		APIKeys:            map[string][]string{"apitest": {"create_order"}, "readonly": {}},
		CouponLimit:        Limit{Requests: 1000, Window: time.Minute},
		OrderLimit:         Limit{Requests: 1000, Window: time.Minute},
		CORSAllowedOrigins: []string{"*"},
		Ready:              ready.Load,
		Spec:               []byte("openapi: 3.1.0\n"),
	}
	for _, apply := range configure {
		apply(&opts)
	}
	return &testServer{handler: NewRouter(opts), coupons: coupons, ready: ready}
}

type call struct {
	method  string
	path    string
	body    string
	headers map[string]string
}

func (s *testServer) do(t *testing.T, c call) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if c.body != "" {
		body = strings.NewReader(c.body)
	}
	req := httptest.NewRequest(c.method, c.path, body)
	req.RemoteAddr = "203.0.113.7:51234"
	if c.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range c.headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec
}

func orderCall(body string) call {
	return call{http.MethodPost, "/api/order", body, map[string]string{"api_key": "apitest"}}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return value
}

func expectError(t *testing.T, rec *httptest.ResponseRecorder, status int, messagePart string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	resp := decode[apiResponse](t, rec)
	if resp.Code != status || resp.Type == "" || !strings.Contains(resp.Message, messagePart) {
		t.Errorf("body = %+v, want code %d and message containing %q", resp, status, messagePart)
	}
}

func TestListProducts(t *testing.T) {
	server := newTestServer(t)
	rec := server.do(t, call{method: http.MethodGet, path: "/api/product"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	products := decode[[]map[string]any](t, rec)
	if len(products) != 9 {
		t.Fatalf("got %d products, want 9", len(products))
	}
	first := products[0]
	if first["id"] != "1" || first["price"] != 6.5 || first["name"] != "Waffle with Berries" {
		t.Errorf("first product = %v", first)
	}
	image, _ := first["image"].(map[string]any)
	for _, size := range []string{"thumbnail", "mobile", "tablet", "desktop"} {
		if url, _ := image[size].(string); !strings.HasSuffix(url, "image-waffle-"+size+".jpg") {
			t.Errorf("image.%s = %v", size, image[size])
		}
	}
}

func TestListProductsEmpty(t *testing.T) {
	server := newTestServer(t, func(opts *Options) { opts.Products = product.NewMemory(nil) })
	rec := server.do(t, call{method: http.MethodGet, path: "/api/product"})
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("got %d %s, want 200 []", rec.Code, rec.Body)
	}
}

func TestGetProduct(t *testing.T) {
	server := newTestServer(t)

	rec := server.do(t, call{method: http.MethodGet, path: "/api/product/7"})
	if rec.Code != http.StatusOK || decode[map[string]any](t, rec)["name"] != "Red Velvet Cake" {
		t.Errorf("GET /api/product/7 = %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/api/product/abc", "/api/product/1.5", "/api/product/99999999999999999999"} {
		expectError(t, server.do(t, call{method: http.MethodGet, path: path}), http.StatusBadRequest, "productId must be an integer")
	}
	for _, path := range []string{"/api/product/999", "/api/product/0", "/api/product/-1"} {
		expectError(t, server.do(t, call{method: http.MethodGet, path: path}), http.StatusNotFound, "product not found")
	}
}

func TestPlaceOrder(t *testing.T) {
	server := newTestServer(t)

	rec := server.do(t, orderCall(`{"items":[{"productId":"1","quantity":2},{"productId":"3","quantity":1}]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body)
	}
	placed := decode[map[string]any](t, rec)
	if placed["total"] != 21.0 || placed["discounts"] != 0.0 {
		t.Errorf("total = %v, discounts = %v; want 21, 0", placed["total"], placed["discounts"])
	}
	if id, _ := placed["id"].(string); len(id) != 36 {
		t.Errorf("id = %v, want a UUID", placed["id"])
	}
	if items, _ := placed["items"].([]any); len(items) != 2 {
		t.Errorf("items = %v", placed["items"])
	}
	if products, _ := placed["products"].([]any); len(products) != 2 {
		t.Errorf("products = %v", placed["products"])
	}

	rec = server.do(t, orderCall(`{"couponCode":"HAPPYHRS","items":[{"productId":"1","quantity":2},{"productId":"3","quantity":1}]}`))
	placed = decode[map[string]any](t, rec)
	if rec.Code != http.StatusOK || placed["total"] != 18.9 || placed["discounts"] != 2.1 {
		t.Errorf("with coupon: %d total = %v, discounts = %v; want 18.9, 2.1", rec.Code, placed["total"], placed["discounts"])
	}
}

func TestPlaceOrderAuth(t *testing.T) {
	server := newTestServer(t)
	body := `{"items":[{"productId":"1","quantity":1}]}`

	missing := orderCall(body)
	missing.headers = nil
	expectError(t, server.do(t, missing), http.StatusUnauthorized, "missing api_key")

	unknown := orderCall(body)
	unknown.headers = map[string]string{"api_key": "wrong"}
	expectError(t, server.do(t, unknown), http.StatusUnauthorized, "invalid api key")

	readonly := orderCall(body)
	readonly.headers = map[string]string{"api_key": "readonly"}
	expectError(t, server.do(t, readonly), http.StatusForbidden, "create_order")
}

func TestPlaceOrderBadInput(t *testing.T) {
	server := newTestServer(t)
	tests := map[string]string{
		"malformed json":      `{"items":[`,
		"not an object":       `[1,2]`,
		"numeric product id":  `{"items":[{"productId":1,"quantity":1}]}`,
		"fractional quantity": `{"items":[{"productId":"1","quantity":1.5}]}`,
		"string quantity":     `{"items":[{"productId":"1","quantity":"2"}]}`,
		"trailing data":       `{"items":[{"productId":"1","quantity":1}]} {}`,
		"too large":           `{"items":[` + strings.Repeat(`{"productId":"1","quantity":1},`, 3000) + `{"productId":"1","quantity":1}]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			expectError(t, server.do(t, orderCall(body)), http.StatusBadRequest, "")
		})
	}

	t.Run("empty body", func(t *testing.T) {
		req := orderCall("")
		req.headers["Content-Type"] = "application/json"
		expectError(t, server.do(t, req), http.StatusBadRequest, "must not be empty")
	})
	t.Run("wrong content type", func(t *testing.T) {
		req := orderCall(`{"items":[{"productId":"1","quantity":1}]}`)
		req.headers["Content-Type"] = "text/plain"
		expectError(t, server.do(t, req), http.StatusBadRequest, "Content-Type")
	})
}

func TestPlaceOrderValidation(t *testing.T) {
	server := newTestServer(t)
	tests := map[string]struct{ body, message string }{
		"missing items":   {`{}`, "items: must contain at least one item"},
		"null items":      {`{"items":null}`, "items: must contain at least one item"},
		"empty items":     {`{"items":[]}`, "items: must contain at least one item"},
		"missing fields":  {`{"items":[{}]}`, "items[0].productId: is required; items[0].quantity"},
		"zero quantity":   {`{"items":[{"productId":"1","quantity":0}]}`, "items[0].quantity: must be between 1 and 100"},
		"unknown product": {`{"items":[{"productId":"42","quantity":1}]}`, `product "42" not found`},
		"invalid coupon":  {`{"couponCode":"SUPER100","items":[{"productId":"1","quantity":1}]}`, "couponCode: is not a valid coupon"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			expectError(t, server.do(t, orderCall(tt.body)), http.StatusUnprocessableEntity, tt.message)
		})
	}
}

func TestPlaceOrderCouponsDown(t *testing.T) {
	server := newTestServer(t)
	server.coupons.down.Store(true)

	rec := server.do(t, orderCall(`{"couponCode":"HAPPYHRS","items":[{"productId":"1","quantity":1}]}`))
	expectError(t, rec, http.StatusServiceUnavailable, "retry later")
	if rec.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After")
	}

	rec = server.do(t, orderCall(`{"items":[{"productId":"1","quantity":1}]}`))
	if rec.Code != http.StatusOK {
		t.Errorf("order without coupon = %d, want 200", rec.Code)
	}
}

func TestValidateCoupon(t *testing.T) {
	server := newTestServer(t)
	validate := func(body string) *httptest.ResponseRecorder {
		return server.do(t, call{method: http.MethodPost, path: "/api/coupon/validate", body: body})
	}

	rec := validate(`{"couponCode":"HAPPYHRS"}`)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"couponCode":"HAPPYHRS","valid":true,"discountPercent":10}` {
		t.Errorf("valid coupon = %d %s", rec.Code, rec.Body)
	}
	rec = validate(`{"couponCode":"SUPER100"}`)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"couponCode":"SUPER100","valid":false}` {
		t.Errorf("invalid coupon = %d %s", rec.Code, rec.Body)
	}
	expectError(t, validate(`{}`), http.StatusUnprocessableEntity, "couponCode: is required")
	expectError(t, validate(`{"couponCode":5}`), http.StatusBadRequest, "couponCode must be of type string")

	server.coupons.down.Store(true)
	expectError(t, validate(`{"couponCode":"HAPPYHRS"}`), http.StatusServiceUnavailable, "retry later")
}

func TestRateLimits(t *testing.T) {
	server := newTestServer(t, func(opts *Options) {
		opts.CouponLimit = Limit{Requests: 3, Window: time.Minute}
		opts.OrderLimit = Limit{Requests: 2, Window: time.Minute}
	})
	coupon := call{method: http.MethodPost, path: "/api/coupon/validate", body: `{"couponCode":"HAPPYHRS"}`}

	for attempt := range 3 {
		if rec := server.do(t, coupon); rec.Code != http.StatusOK {
			t.Fatalf("coupon attempt %d = %d", attempt, rec.Code)
		}
	}
	rec := server.do(t, coupon)
	expectError(t, rec, http.StatusTooManyRequests, "too many requests")
	if rec.Header().Get("Retry-After") == "" || rec.Header().Get("X-RateLimit-Limit") != "3" {
		t.Errorf("rate limit headers = %v", rec.Header())
	}

	// Orders have their own budget, and rejected auth still counts.
	unauthenticated := orderCall(`{}`)
	unauthenticated.headers = nil
	server.do(t, unauthenticated)
	server.do(t, orderCall(`{"items":[{"productId":"1","quantity":1}]}`))
	expectError(t, server.do(t, orderCall(`{"items":[{"productId":"1","quantity":1}]}`)), http.StatusTooManyRequests, "")

	// Products are not rate limited.
	if rec := server.do(t, call{method: http.MethodGet, path: "/api/product"}); rec.Code != http.StatusOK {
		t.Errorf("products = %d", rec.Code)
	}
}

func TestRateLimitTrustsOnlyConfiguredProxies(t *testing.T) {
	server := newTestServer(t, func(opts *Options) {
		opts.CouponLimit = Limit{Requests: 1, Window: time.Minute}
		opts.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	})
	request := func(remote, forwarded string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/coupon/validate", strings.NewReader(`{"couponCode":"HAPPYHRS"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		req.Header.Set("X-Forwarded-For", forwarded)
		rec := httptest.NewRecorder()
		server.handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// An untrusted peer cannot dodge its limit by rotating X-Forwarded-For.
	if code := request("198.51.100.1:1000", "1.1.1.1"); code != http.StatusOK {
		t.Fatalf("first direct request = %d", code)
	}
	if code := request("198.51.100.1:1000", "2.2.2.2"); code != http.StatusTooManyRequests {
		t.Errorf("spoofed request = %d, want 429", code)
	}

	// Behind a trusted proxy, each forwarded client has its own budget.
	if code := request("10.0.0.5:1000", "3.3.3.3"); code != http.StatusOK {
		t.Errorf("proxied client A = %d", code)
	}
	if code := request("10.0.0.5:1000", "4.4.4.4"); code != http.StatusOK {
		t.Errorf("proxied client B = %d", code)
	}
	if code := request("10.0.0.5:1000", "3.3.3.3"); code != http.StatusTooManyRequests {
		t.Errorf("proxied client A again = %d, want 429", code)
	}
}

func TestHealth(t *testing.T) {
	server := newTestServer(t)
	for _, path := range []string{"/health", "/ready"} {
		if rec := server.do(t, call{method: http.MethodGet, path: path}); rec.Code != http.StatusOK {
			t.Errorf("%s = %d", path, rec.Code)
		}
	}

	server.ready.Store(false)
	if rec := server.do(t, call{method: http.MethodGet, path: "/ready"}); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/ready while shutting down = %d", rec.Code)
	}
	if rec := server.do(t, call{method: http.MethodGet, path: "/health"}); rec.Code != http.StatusOK {
		t.Errorf("/health while shutting down = %d", rec.Code)
	}
}

func TestRoutingErrors(t *testing.T) {
	server := newTestServer(t)
	expectError(t, server.do(t, call{method: http.MethodGet, path: "/api/nope"}), http.StatusNotFound, "not found")
	expectError(t, server.do(t, call{method: http.MethodGet, path: "/product"}), http.StatusNotFound, "not found")
	expectError(t, server.do(t, call{method: http.MethodDelete, path: "/api/product"}), http.StatusMethodNotAllowed, "")
	expectError(t, server.do(t, call{method: http.MethodGet, path: "/api/order"}), http.StatusMethodNotAllowed, "")
}

func TestRequestID(t *testing.T) {
	server := newTestServer(t)

	rec := server.do(t, call{method: http.MethodGet, path: "/health", headers: map[string]string{"X-Request-Id": "abc-123"}})
	if got := rec.Header().Get("X-Request-Id"); got != "abc-123" {
		t.Errorf("echoed request id = %q", got)
	}
	rec = server.do(t, call{method: http.MethodGet, path: "/health", headers: map[string]string{"X-Request-Id": "bad id\n"}})
	if got := rec.Header().Get("X-Request-Id"); len(got) != 36 {
		t.Errorf("generated request id = %q, want a UUID", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	server := newTestServer(t)
	rec := server.do(t, call{method: http.MethodOptions, path: "/api/order", headers: map[string]string{
		"Origin":                         "https://shop.example",
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "content-type, api_key",
	}})
	if rec.Code >= 300 || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("preflight = %d %v", rec.Code, rec.Header())
	}
	if allowed := strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers")); !strings.Contains(allowed, "api_key") {
		t.Errorf("Access-Control-Allow-Headers = %q", allowed)
	}
}

func TestPanicIsRecovered(t *testing.T) {
	server := newTestServer(t, func(opts *Options) { opts.Products = panickyStore{} })
	expectError(t, server.do(t, call{method: http.MethodGet, path: "/api/product"}), http.StatusInternalServerError, "internal server error")
}

type panickyStore struct{ product.Store }

func (panickyStore) List(context.Context) ([]product.Product, error) { panic("boom") }

func TestOpenAPI(t *testing.T) {
	server := newTestServer(t)
	rec := server.do(t, call{method: http.MethodGet, path: "/api/openapi.yaml"})
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "openapi:") {
		t.Errorf("GET /api/openapi.yaml = %d %q", rec.Code, rec.Body)
	}
}

func TestClientKey(t *testing.T) {
	tests := map[string]string{
		"203.0.113.7":          "203.0.113.7",
		"::ffff:203.0.113.7":   "203.0.113.7",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:db8:1:2:ff::1":   "2001:db8:1:2::/64",
		"not-an-ip":            "not-an-ip",
	}
	for ip, want := range tests {
		if got := clientKey(ip); got != want {
			t.Errorf("clientKey(%q) = %q, want %q", ip, got, want)
		}
	}
}
