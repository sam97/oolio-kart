package products

import (
	"context"
	"errors"
	"testing"

	"github.com/sam97/oolio-kart/pkg/models"
)

// fakeStore is a productstore.Store over a slice.
type fakeStore []models.Product

func (f fakeStore) List(context.Context) ([]models.Product, error) { return f, nil }

func (f fakeStore) Get(_ context.Context, id string) (models.Product, error) {
	for _, product := range f {
		if product.ID == id {
			return product, nil
		}
	}
	return models.Product{}, models.ErrProductNotFound
}

func (f fakeStore) GetMany(context.Context, []string) (map[string]models.Product, error) {
	return nil, errors.New("not used")
}

func TestService(t *testing.T) {
	ctx := t.Context()
	if got, err := NewService(fakeStore(nil)).List(ctx); err != nil || got == nil || len(got) != 0 {
		t.Errorf("List of an empty catalogue = %#v, %v; want an empty slice", got, err)
	}

	service := NewService(fakeStore{{ID: "1", Name: "Waffle"}})
	if got, err := service.Get(ctx, "1"); err != nil || got.Name != "Waffle" {
		t.Errorf("Get(1) = %+v, %v", got, err)
	}
	if _, err := service.Get(ctx, "2"); !errors.Is(err, models.ErrProductNotFound) {
		t.Errorf("Get(2) err = %v, want ErrProductNotFound", err)
	}
}
