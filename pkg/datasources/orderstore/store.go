// Package orderstore keeps placed orders.
package orderstore

import (
	"context"

	"github.com/sam97/oolio-kart/pkg/models"
)

type Store interface {
	// Save stores the order and its items, all or nothing.
	Save(ctx context.Context, order models.Order) error
}
