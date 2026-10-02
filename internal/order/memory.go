package order

import (
	"context"
	"sync"
)

// Memory is an in-process Store. It stands in for a real database and keeps
// every order until the process exits.
type Memory struct {
	mu     sync.RWMutex
	orders map[string]Order
}

func NewMemory() *Memory {
	return &Memory{orders: map[string]Order{}}
}

func (m *Memory) Save(_ context.Context, order Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orders[order.ID] = order
	return nil
}

func (m *Memory) Get(_ context.Context, id string) (Order, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	order, found := m.orders[id]
	return order, found
}
