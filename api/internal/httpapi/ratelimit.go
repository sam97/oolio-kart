package httpapi

import (
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

type Limit struct {
	Requests int
	Window   time.Duration
}

// rateLimit allows bursts of limit.Requests per client IP, refilling at
// limit.Requests per limit.Window. Counters live in this process; to share
// them across replicas, replace the store with one implementing
// middleware.RateLimiterStore on Redis.
func rateLimit(limit Limit) echo.MiddlewareFunc {
	store := middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
		Rate:      float64(limit.Requests) / limit.Window.Seconds(),
		Burst:     limit.Requests,
		ExpiresIn: limit.Window,
	})
	return middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store: store,
		IdentifierExtractor: func(c *echo.Context) (string, error) {
			return clientKey(c.RealIP()), nil
		},
		DenyHandler: func(c *echo.Context, identifier string, err error) error {
			if err != nil {
				return fmt.Errorf("rate limiter: %w", err)
			}
			return echo.NewHTTPError(http.StatusTooManyRequests, "too many requests, retry later")
		},
	})
}

// clientKey buckets IPv6 clients by their /64, since a single client usually
// controls the whole /64 and could otherwise rotate addresses to get a fresh
// limit on every request.
func clientKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is6() {
		return netip.PrefixFrom(addr, 64).Masked().String()
	}
	return addr.String()
}
