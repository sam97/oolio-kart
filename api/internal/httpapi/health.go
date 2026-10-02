package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

type healthStatus struct {
	Status string `json:"status"`
}

// liveness is the liveness probe: the process is up and serving.
func (h *handlers) liveness(c *echo.Context) error {
	return c.JSON(http.StatusOK, healthStatus{"ok"})
}

// readiness is the readiness probe. It fails once shutdown starts so load
// balancers stop sending traffic while in-flight requests drain. It does not
// depend on the coupons service: orders without coupons keep working when it
// is down.
func (h *handlers) readiness(c *echo.Context) error {
	if h.ready != nil && !h.ready() {
		return c.JSON(http.StatusServiceUnavailable, healthStatus{"shutting down"})
	}
	return c.JSON(http.StatusOK, healthStatus{"ready"})
}

func (h *handlers) openAPI(c *echo.Context) error {
	return c.Blob(http.StatusOK, "application/yaml", h.spec)
}
