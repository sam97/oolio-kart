// Package productstore holds the product catalogue.
package productstore

import (
	"context"

	"github.com/sam97/oolio-kart/pkg/models"
)

type Store interface {
	List(ctx context.Context) ([]models.Product, error)

	// Get returns models.ErrProductNotFound when no product has the given id.
	Get(ctx context.Context, id string) (models.Product, error)
}
