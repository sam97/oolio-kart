package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"
	"github.com/sam97/oolio-kart/api/internal/product"
)

func (h *handlers) listProducts(c *echo.Context) error {
	products, err := h.products.List(c.Request().Context())
	if err != nil {
		return fmt.Errorf("list products: %w", err)
	}
	if products == nil {
		products = []product.Product{}
	}
	return c.JSON(http.StatusOK, products)
}

func (h *handlers) getProduct(c *echo.Context) error {
	// The spec types productId as an int64 path parameter.
	id, err := echo.PathParam[int64](c, "productId")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "productId must be an integer")
	}

	found, err := h.products.Get(c.Request().Context(), strconv.FormatInt(id, 10))
	if errors.Is(err, product.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "product not found")
	}
	if err != nil {
		return fmt.Errorf("get product: %w", err)
	}
	return c.JSON(http.StatusOK, found)
}
