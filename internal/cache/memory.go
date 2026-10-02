package cache

import (
	"context"
	"slices"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// Memory is an in-process Cache bounded to a maximum number of entries. When
// full, the least recently used entry is evicted. Expired entries are dropped
// when read, or evicted like any other entry.
type Memory struct {
	entries *lru.Cache[string, entry]
	now     func() time.Time
}

type entry struct {
	value     []byte
	expiresAt time.Time // zero means no expiry
}

func NewMemory(maxEntries int) (*Memory, error) {
	entries, err := lru.New[string, entry](maxEntries)
	if err != nil {
		return nil, err
	}
	return &Memory{entries: entries, now: time.Now}, nil
}

func (m *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	item, found := m.entries.Get(key)
	if !found {
		return nil, false, nil
	}
	if !item.expiresAt.IsZero() && !m.now().Before(item.expiresAt) {
		m.entries.Remove(key)
		return nil, false, nil
	}
	return slices.Clone(item.value), true, nil
}

func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	item := entry{value: slices.Clone(value)}
	if ttl > 0 {
		item.expiresAt = m.now().Add(ttl)
	}
	m.entries.Add(key, item)
	return nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	m.entries.Remove(key)
	return nil
}
