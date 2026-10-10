package kartapi

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"regexp"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/sam97/oolio-kart/pkg/helpers/logging"
)

var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// dropMalformedRequestID removes an incoming X-Request-Id that is too long or
// has unsafe characters, so requestID generates a fresh one instead of
// echoing it into logs and responses.
func dropMalformedRequestID(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		header := c.Request().Header
		if id := header.Get(echo.HeaderXRequestID); id != "" && !safeRequestID.MatchString(id) {
			header.Del(echo.HeaderXRequestID)
		}
		return next(c)
	}
}

// requestID sets X-Request-Id on the response and puts it in the request
// context, so every log line of the request carries it.
func requestID() echo.MiddlewareFunc {
	return middleware.RequestIDWithConfig(middleware.RequestIDConfig{
		Generator: uuid.NewString,
		RequestIDHandler: func(c *echo.Context, id string) {
			c.SetRequest(c.Request().WithContext(logging.WithRequestID(c.Request().Context(), id)))
		},
	})
}

// quietPaths are probed constantly, so they are logged at debug level.
var quietPaths = map[string]bool{"/health": true, "/ready": true}

// accessLog logs one line per request. It renders errors through the error
// handler first, so the logged status is the one the client received.
func accessLog() echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		HandleError:     true,
		LogMethod:       true,
		LogURIPath:      true,
		LogStatus:       true,
		LogLatency:      true,
		LogRemoteIP:     true,
		LogUserAgent:    true,
		LogResponseSize: true,
		LogValuesFunc: func(c *echo.Context, values middleware.RequestLoggerValues) error {
			level := slog.LevelInfo
			switch {
			case quietPaths[values.URIPath]:
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
				"bytes", values.ResponseSize,
				"duration", values.Latency,
				"ip", values.RemoteIP,
				"user_agent", values.UserAgent,
			}
			if values.Error != nil && values.Status >= 500 {
				attrs = append(attrs, "err", values.Error)
			}
			c.Logger().Log(c.Request().Context(), level, "request", attrs...)
			return nil
		},
	})
}

// ipExtractor resolves the client IP used for rate limits and logs. It is the
// TCP peer, unless that peer is a trusted proxy, in which case
// X-Forwarded-For is walked right to left past trusted hops.
func ipExtractor(trusted []netip.Prefix) echo.IPExtractor {
	if len(trusted) == 0 {
		return echo.ExtractIPDirect()
	}
	// Trust only the configured ranges, not echo's default private ranges.
	options := []echo.TrustOption{echo.TrustLoopback(false), echo.TrustLinkLocal(false), echo.TrustPrivateNet(false)}
	for _, prefix := range trusted {
		_, ipRange, _ := net.ParseCIDR(prefix.Masked().String())
		options = append(options, echo.TrustIPRange(ipRange))
	}
	return echo.ExtractIPFromXFFHeader(options...)
}
