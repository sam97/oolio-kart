package product

import (
	"context"
	"slices"

	"github.com/sam97/oolio-kart/api/internal/money"
)

// Memory is a read-only, in-process Store. It stands in for a real database.
type Memory struct {
	products []Product
	byID     map[string]Product
}

func NewMemory(products []Product) *Memory {
	byID := make(map[string]Product, len(products))
	for _, item := range products {
		byID[item.ID] = item
	}
	return &Memory{products: slices.Clone(products), byID: byID}
}

func (m *Memory) List(context.Context) ([]Product, error) {
	return slices.Clone(m.products), nil
}

func (m *Memory) Get(_ context.Context, id string) (Product, error) {
	item, found := m.byID[id]
	if !found {
		return Product{}, ErrNotFound
	}
	return item, nil
}

const imageBaseURL = "https://orderfoodonline.deno.dev/public/images/"

// Demo is the dummy catalogue served until a real store is wired in.
func Demo() []Product {
	return []Product{
		demo("1", "Waffle with Berries", "Waffle", 650, "waffle"),
		demo("2", "Vanilla Bean Crème Brûlée", "Crème Brûlée", 700, "creme-brulee"),
		demo("3", "Macaron Mix of Five", "Macaron", 800, "macaron"),
		demo("4", "Classic Tiramisu", "Tiramisu", 550, "tiramisu"),
		demo("5", "Pistachio Baklava", "Baklava", 400, "baklava"),
		demo("6", "Lemon Meringue Pie", "Pie", 500, "meringue"),
		demo("7", "Red Velvet Cake", "Cake", 450, "cake"),
		demo("8", "Salted Caramel Brownie", "Brownie", 550, "brownie"),
		demo("9", "Vanilla Panna Cotta", "Panna Cotta", 650, "panna-cotta"),
	}
}

func demo(id, name, category string, cents int64, image string) Product {
	prefix := imageBaseURL + "image-" + image
	return Product{
		ID:       id,
		Name:     name,
		Price:    money.Cents(cents),
		Category: category,
		Image: Image{
			Thumbnail: prefix + "-thumbnail.jpg",
			Mobile:    prefix + "-mobile.jpg",
			Tablet:    prefix + "-tablet.jpg",
			Desktop:   prefix + "-desktop.jpg",
		},
	}
}
