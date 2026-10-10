// Package cache defines a byte-oriented key/value cache with per-entry expiry.
//
// The interface mirrors what Redis offers (GET, SET with EX, DEL) so that the
// in-memory implementation can be swapped for a Redis or Valkey one without
// touching callers.
package cache

import (
	"context"
	"encoding/json"
	"time"
)

type Cache interface {
	// Get returns the value stored under key. found is false when the key is
	// missing or expired.
	Get(ctx context.Context, key string) (value []byte, found bool, err error)

	// Set stores value under key for ttl. A ttl <= 0 means the entry does not
	// expire on its own.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	Delete(ctx context.Context, key string) error

	// Purge removes every entry.
	Purge(ctx context.Context) error
}

// GetJSON reads key from cache and decodes it into a T.
func GetJSON[T any](ctx context.Context, cache Cache, key string) (T, bool, error) {
	var value T
	raw, found, err := cache.Get(ctx, key)
	if err != nil || !found {
		return value, false, err
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, false, err
	}
	return value, true, nil
}

// SetJSON encodes value as JSON and stores it under key for ttl.
func SetJSON[T any](ctx context.Context, cache Cache, key string, value T, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return cache.Set(ctx, key, raw, ttl)
}
