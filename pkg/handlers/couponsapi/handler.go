// Package couponsapi serves coupon lookups over HTTP for the coupons server.
package couponsapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// Checker is the part of coupons.Service the handler needs.
type Checker interface {
	IsValid(code string) bool
}

type couponResponse struct {
	Code            string `json:"code"`
	DiscountPercent int    `json:"discountPercent"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// NewRouter serves coupon lookups. Until ready reports true, lookups answer
// 503 rather than claiming every code is invalid.
func NewRouter(coupons Checker, ready func() bool, discountPercent int, logger *slog.Logger) *echo.Echo {
	router := echo.New()
	router.Logger = logger
	router.HTTPErrorHandler = handleError
	router.Use(accessLog(), middleware.Recover())

	router.GET("/v1/coupons/:code", func(c *echo.Context) error {
		if !ready() {
			c.Response().Header().Set(echo.HeaderRetryAfter, "5")
			return echo.NewHTTPError(http.StatusServiceUnavailable, "coupons are still loading")
		}
		code := c.Param("code")
		if !coupons.IsValid(code) {
			return echo.NewHTTPError(http.StatusNotFound, "coupon not found")
		}
		return c.JSON(http.StatusOK, couponResponse{Code: code, DiscountPercent: discountPercent})
	})

	router.GET("/health", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	router.GET("/ready", func(c *echo.Context) error {
		if !ready() {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "loading"})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ready"})
	})

	return router
}

// handleError renders errors as {"error": message}, hiding the details of
// unexpected errors.
func handleError(c *echo.Context, err error) {
	if resp, _ := echo.UnwrapResponse(c.Response()); resp != nil && resp.Committed {
		return
	}
	status := echo.StatusCode(err)
	message := ""
	if httpErr, ok := errors.AsType[*echo.HTTPError](err); ok {
		message = httpErr.Message
	}
	if status == 0 || status == http.StatusInternalServerError {
		status, message = http.StatusInternalServerError, "internal server error"
	}
	if message == "" {
		message = strings.ToLower(http.StatusText(status))
	}
	if err := c.JSON(status, errorResponse{message}); err != nil {
		c.Logger().Error("write error response", "err", err)
	}
}

func accessLog() echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		HandleError:  true,
		LogMethod:    true,
		LogURIPath:   true,
		LogStatus:    true,
		LogLatency:   true,
		LogRequestID: true,
		LogValuesFunc: func(c *echo.Context, values middleware.RequestLoggerValues) error {
			level := slog.LevelInfo
			switch {
			case values.URIPath == "/health" || values.URIPath == "/ready":
				level = slog.LevelDebug
			case values.Status == http.StatusServiceUnavailable:
				level = slog.LevelWarn
			case values.Status >= 500:
				level = slog.LevelError
			}
			attrs := []any{
				"method", values.Method,
				"path", values.URIPath,
				"status", values.Status,
				"duration", values.Latency,
			}
			if values.RequestID != "" {
				attrs = append(attrs, "request_id", values.RequestID)
			}
			if values.Error != nil && level == slog.LevelError {
				attrs = append(attrs, "err", values.Error)
			}
			c.Logger().Log(c.Request().Context(), level, "request", attrs...)
			return nil
		},
	})
}
