package postgres

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sam97/oolio-kart/pkg/models"
)

// Orders keeps placed orders in the orders and order_items tables. It is an
// orderstore.Store.
type Orders struct {
	pool *pgxpool.Pool
}

func NewOrders(pool *pgxpool.Pool) *Orders {
	return &Orders{pool: pool}
}

// Save writes the order and one order_items row per item in one
// transaction. Each item records its product's price from order.Products, so
// the order keeps its prices if the catalogue changes.
func (o *Orders) Save(ctx context.Context, order models.Order) error {
	prices := make(map[string]models.Cents, len(order.Products))
	for _, product := range order.Products {
		prices[product.ID] = product.Price
	}
	items := make([][]any, len(order.Items))
	for line, item := range order.Items {
		price, found := prices[item.ProductID]
		productID, err := strconv.ParseInt(item.ProductID, 10, 64)
		if !found || err != nil {
			return fmt.Errorf("item %d: product %q is not among the order's products", line, item.ProductID)
		}
		items[line] = []any{order.ID, line, productID, item.Quantity, int64(price)}
	}

	tx, err := o.pool.Begin(ctx)
	if err != nil {
		return classify(err)
	}
	defer tx.Rollback(context.Background()) // a no-op once committed

	_, err = tx.Exec(ctx,
		`INSERT INTO orders (id, coupon_code, discounts_cents, total_cents) VALUES ($1, NULLIF($2, ''), $3, $4)`,
		order.ID, order.CouponCode, int64(order.Discounts), int64(order.Total))
	if err != nil {
		return classify(err)
	}
	columns := []string{"order_id", "line", "product_id", "quantity", "unit_price_cents"}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"order_items"}, columns, pgx.CopyFromRows(items)); err != nil {
		return classify(err)
	}
	return classify(tx.Commit(ctx))
}
