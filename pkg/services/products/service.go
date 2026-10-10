// Package products serves the product catalogue.
package products

import (
	"context"

	"github.com/sam97/oolio-kart/pkg/datasources/productstore"
	"github.com/sam97/oolio-kart/pkg/models"
)

type Service struct {
	store productstore.Store
}

func NewService(store productstore.Store) *Service {
	return &Service{store: store}
}

// List returns every product; an empty catalogue is an empty slice, not nil.
func (s *Service) List(ctx context.Context) ([]models.Product, error) {
	products, err := s.store.List(ctx)
	if products == nil && err == nil {
		products = []models.Product{}
	}
	return products, err
}

// Get returns models.ErrProductNotFound when no product has the given id.
func (s *Service) Get(ctx context.Context, id string) (models.Product, error) {
	return s.store.Get(ctx, id)
}
