// Package product holds the product catalogue.
package product

import (
	"context"
	"errors"

	"github.com/sam97/oolio-kart/api/internal/money"
)

var ErrNotFound = errors.New("product not found")

type Product struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Price    money.Cents `json:"price"`
	Category string      `json:"category"`
	Image    Image       `json:"image"`
}

type Image struct {
	Thumbnail string `json:"thumbnail"`
	Mobile    string `json:"mobile"`
	Tablet    string `json:"tablet"`
	Desktop   string `json:"desktop"`
}

type Store interface {
	List(ctx context.Context) ([]Product, error)

	// Get returns ErrNotFound when no product has the given id.
	Get(ctx context.Context, id string) (Product, error)
}
