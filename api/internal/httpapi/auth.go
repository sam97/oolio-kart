package httpapi

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"slices"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

const apiKeyHeader = "api_key"

// keyRing holds the SHA-256 of each api key with its scopes, so looking a key
// up does not leak timing about the raw key.
type keyRing map[[sha256.Size]byte][]string

func newKeyRing(keys map[string][]string) keyRing {
	ring := keyRing{}
	for key, scopes := range keys {
		ring[sha256.Sum256([]byte(key))] = scopes
	}
	return ring
}

var errMissingScope = errors.New("api key lacks scope")

// requireScope answers 401 when the api_key header is missing or unknown, and
// 403 when the key is known but lacks scope.
func requireScope(ring keyRing, scope string) echo.MiddlewareFunc {
	return middleware.KeyAuthWithConfig(middleware.KeyAuthConfig{
		KeyLookup: "header:" + apiKeyHeader,
		Validator: func(c *echo.Context, key string, source middleware.ExtractorSource) (bool, error) {
			scopes, found := ring[sha256.Sum256([]byte(key))]
			if !found {
				return false, nil
			}
			if !slices.Contains(scopes, scope) {
				return false, errMissingScope
			}
			return true, nil
		},
		ErrorHandler: func(c *echo.Context, err error) error {
			switch {
			case errors.Is(err, errMissingScope):
				return echo.NewHTTPError(http.StatusForbidden, "api key lacks the "+scope+" scope")
			case errors.Is(err, middleware.ErrInvalidKey):
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid api key")
			default:
				return echo.NewHTTPError(http.StatusUnauthorized, "missing api_key header")
			}
		},
	})
}
