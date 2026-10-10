package postgres

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sam97/oolio-kart/pkg/models"
)

// Products is the catalogue in the products table. It is a
// productstore.Store. Ids are integers in the table and strings in
// models.Product; an id that is not an integer matches no product.
type Products struct {
	pool *pgxpool.Pool
}

func NewProducts(pool *pgxpool.Pool) *Products {
	return &Products{pool: pool}
}

const selectProducts = "SELECT id, name, price_cents, category, image FROM products"

func (p *Products) List(ctx context.Context) ([]models.Product, error) {
	rows, err := p.pool.Query(ctx, selectProducts+" ORDER BY id")
	if err != nil {
		return nil, classify(err)
	}
	products, err := pgx.CollectRows(rows, scanProduct)
	return products, classify(err)
}

func (p *Products) Get(ctx context.Context, id string) (models.Product, error) {
	key, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return models.Product{}, models.ErrProductNotFound
	}
	rows, err := p.pool.Query(ctx, selectProducts+" WHERE id = $1", key)
	if err != nil {
		return models.Product{}, classify(err)
	}
	product, err := pgx.CollectExactlyOneRow(rows, scanProduct)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Product{}, models.ErrProductNotFound
	}
	return product, classify(err)
}

func (p *Products) GetMany(ctx context.Context, ids []string) (map[string]models.Product, error) {
	keys := make([]int64, 0, len(ids))
	for _, id := range ids {
		if key, err := strconv.ParseInt(id, 10, 64); err == nil {
			keys = append(keys, key)
		}
	}
	found := make(map[string]models.Product, len(keys))
	if len(keys) == 0 {
		return found, nil
	}
	rows, err := p.pool.Query(ctx, selectProducts+" WHERE id = ANY($1)", keys)
	if err != nil {
		return nil, classify(err)
	}
	products, err := pgx.CollectRows(rows, scanProduct)
	if err != nil {
		return nil, classify(err)
	}
	for _, product := range products {
		found[product.ID] = product
	}
	return found, nil
}

func scanProduct(row pgx.CollectableRow) (models.Product, error) {
	var product models.Product
	var id int64
	err := row.Scan(&id, &product.Name, &product.Price, &product.Category, &product.Image)
	product.ID = strconv.FormatInt(id, 10)
	return product, err
}
