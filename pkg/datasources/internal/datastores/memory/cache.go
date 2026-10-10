// Package memory keeps cache entries in process memory.
package memory

import (
	"context"
	"slices"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// Cache is a cache.Cache bounded to a maximum number of entries. When full,
// the least recently used entry is evicted. Expired entries are dropped when
// read, or evicted like any other entry.
type Cache struct {
	entries *lru.Cache[string, entry]
	now     func() time.Time
}

type entry struct {
	value     []byte
	expiresAt time.Time // zero means no expiry
}

func NewCache(maxEntries int) (*Cache, error) {
	entries, err := lru.New[string, entry](maxEntries)
	if err != nil {
		return nil, err
	}
	return &Cache{entries: entries, now: time.Now}, nil
}

func (m *Cache) Get(_ context.Context, key string) ([]byte, bool, error) {
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

func (m *Cache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	item := entry{value: slices.Clone(value)}
	if ttl > 0 {
		item.expiresAt = m.now().Add(ttl)
	}
	m.entries.Add(key, item)
	return nil
}

func (m *Cache) Delete(_ context.Context, key string) error {
	m.entries.Remove(key)
	return nil
}

func (m *Cache) Purge(_ context.Context) error {
	m.entries.Purge()
	return nil
}
