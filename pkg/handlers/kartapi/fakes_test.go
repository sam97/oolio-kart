package kartapi

import (
	"context"
	"sync"

	"github.com/sam97/oolio-kart/pkg/models"
)

// fakeProducts is a productstore.Store over a slice. While err is set, every
// call fails with it.
type fakeProducts struct {
	products []models.Product
	err      error
}

func (f *fakeProducts) List(context.Context) ([]models.Product, error) {
	return f.products, f.err
}

func (f *fakeProducts) Get(_ context.Context, id string) (models.Product, error) {
	if f.err != nil {
		return models.Product{}, f.err
	}
	for _, product := range f.products {
		if product.ID == id {
			return product, nil
		}
	}
	return models.Product{}, models.ErrProductNotFound
}

func (f *fakeProducts) GetMany(_ context.Context, ids []string) (map[string]models.Product, error) {
	if f.err != nil {
		return nil, f.err
	}
	found := map[string]models.Product{}
	for _, id := range ids {
		for _, product := range f.products {
			if product.ID == id {
				found[id] = product
			}
		}
	}
	return found, nil
}

// fakeOrders is an orderstore.Store that keeps orders in memory.
type fakeOrders struct {
	mu    sync.Mutex
	saved []models.Order
}

func (f *fakeOrders) Save(_ context.Context, order models.Order) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, order)
	return nil
}

// demoProducts is the catalogue the database migration seeds.
func demoProducts() []models.Product {
	const imageBaseURL = "https://orderfoodonline.deno.dev/public/images/image-"
	product := func(id, name, category string, cents int64, image string) models.Product {
		prefix := imageBaseURL + image
		return models.Product{
			ID: id, Name: name, Price: models.Cents(cents), Category: category,
			Image: models.Image{
				Thumbnail: prefix + "-thumbnail.jpg",
				Mobile:    prefix + "-mobile.jpg",
				Tablet:    prefix + "-tablet.jpg",
				Desktop:   prefix + "-desktop.jpg",
			},
		}
	}
	return []models.Product{
		product("1", "Waffle with Berries", "Waffle", 650, "waffle"),
		product("2", "Vanilla Bean Crème Brûlée", "Crème Brûlée", 700, "creme-brulee"),
		product("3", "Macaron Mix of Five", "Macaron", 800, "macaron"),
		product("4", "Classic Tiramisu", "Tiramisu", 550, "tiramisu"),
		product("5", "Pistachio Baklava", "Baklava", 400, "baklava"),
		product("6", "Lemon Meringue Pie", "Pie", 500, "meringue"),
		product("7", "Red Velvet Cake", "Cake", 450, "cake"),
		product("8", "Salted Caramel Brownie", "Brownie", 550, "brownie"),
		product("9", "Vanilla Panna Cotta", "Panna Cotta", 650, "panna-cotta"),
	}
}
