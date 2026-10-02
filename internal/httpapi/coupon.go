package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/sam97/oolio-kart/internal/couponclient"
)

type couponRequest struct {
	CouponCode string `json:"couponCode"`
}

type couponValidation struct {
	CouponCode      string `json:"couponCode"`
	Valid           bool   `json:"valid"`
	DiscountPercent int    `json:"discountPercent,omitempty"`
}

func (h *handlers) validateCoupon(c *echo.Context) error {
	var req couponRequest
	if err := bindJSON(c, &req); err != nil {
		return err
	}
	if req.CouponCode == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "couponCode: is required")
	}

	found, err := h.coupons.Validate(c.Request().Context(), req.CouponCode)
	switch {
	case err == nil:
		return c.JSON(http.StatusOK, couponValidation{CouponCode: req.CouponCode, Valid: true, DiscountPercent: found.DiscountPercent})
	case errors.Is(err, couponclient.ErrInvalid):
		return c.JSON(http.StatusOK, couponValidation{CouponCode: req.CouponCode, Valid: false})
	case errors.Is(err, couponclient.ErrUnavailable):
		return couponUnavailable(c, err)
	default:
		return fmt.Errorf("validate coupon: %w", err)
	}
}
