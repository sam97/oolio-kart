package kartapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/sam97/oolio-kart/pkg/models"
)

// couponRetryAfter is the Retry-After, in seconds, sent when coupons cannot
// be checked.
const couponRetryAfter = "5"

func (h *handlers) placeOrder(c *echo.Context) error {
	var req models.OrderRequest
	if err := bindJSON(c, &req); err != nil {
		return err
	}

	placed, err := h.orders.Place(c.Request().Context(), req)
	var invalid *models.ValidationError
	switch {
	case err == nil:
		return c.JSON(http.StatusOK, placed)
	case errors.As(err, &invalid):
		return echo.NewHTTPError(http.StatusUnprocessableEntity, invalid.Error())
	case errors.Is(err, models.ErrCouponUnavailable):
		return couponUnavailable(c, err)
	default:
		return fmt.Errorf("place order: %w", err)
	}
}

func couponUnavailable(c *echo.Context, err error) error {
	c.Logger().WarnContext(c.Request().Context(), "coupons unavailable", "err", err)
	c.Response().Header().Set(echo.HeaderRetryAfter, couponRetryAfter)
	return echo.NewHTTPError(http.StatusServiceUnavailable, "coupons cannot be checked right now, retry later")
}
