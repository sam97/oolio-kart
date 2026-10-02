// Package httpapi serves the food ordering API over HTTP.
package httpapi

import (
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/sam97/oolio-kart/api/internal/coupon"
	"github.com/sam97/oolio-kart/api/internal/order"
	"github.com/sam97/oolio-kart/api/internal/product"
)

type Options struct {
	Logger   *slog.Logger
	Products product.Store
	Orders   *order.Service
	Coupons  coupon.Validator

	// APIKeys maps each accepted api_key to its scopes.
	APIKeys map[string][]string

	CouponLimit Limit
	OrderLimit  Limit

	TrustedProxies     []netip.Prefix
	CORSAllowedOrigins []string

	// Ready reports whether the server should receive traffic.
	Ready func() bool

	// Spec is the OpenAPI document served at /api/openapi.yaml.
	Spec []byte
}

func NewRouter(opts Options) *echo.Echo {
	handlers := &handlers{
		products: opts.Products,
		orders:   opts.Orders,
		coupons:  opts.Coupons,
		ready:    opts.Ready,
		spec:     opts.Spec,
	}

	router := echo.New()
	router.Logger = opts.Logger
	router.IPExtractor = ipExtractor(opts.TrustedProxies)
	router.HTTPErrorHandler = handleError

	router.Pre(dropMalformedRequestID)
	router.Use(
		requestID(),
		accessLog(),
		middleware.Recover(),
		middleware.SecureWithConfig(middleware.SecureConfig{
			ContentTypeNosniff: "nosniff",
			XFrameOptions:      "DENY",
		}),
		middleware.CORSWithConfig(middleware.CORSConfig{
			AllowOrigins:  opts.CORSAllowedOrigins,
			AllowMethods:  []string{http.MethodGet, http.MethodPost},
			AllowHeaders:  []string{echo.HeaderContentType, apiKeyHeader, echo.HeaderXRequestID},
			ExposeHeaders: []string{echo.HeaderXRequestID, echo.HeaderRetryAfter, middleware.HeaderXRateLimitLimit, middleware.HeaderXRateLimitRemaining},
			MaxAge:        300,
		}),
	)

	router.GET("/health", handlers.liveness)
	router.GET("/ready", handlers.readiness)

	api := router.Group("/api")
	api.GET("/openapi.yaml", handlers.openAPI)
	api.GET("/product", handlers.listProducts)
	api.GET("/product/:productId", handlers.getProduct)
	api.POST("/coupon/validate", handlers.validateCoupon, rateLimit(opts.CouponLimit))
	api.POST("/order", handlers.placeOrder,
		rateLimit(opts.OrderLimit),
		requireScope(newKeyRing(opts.APIKeys), "create_order"),
	)
	return router
}

type handlers struct {
	products product.Store
	orders   *order.Service
	coupons  coupon.Validator
	ready    func() bool
	spec     []byte
}
