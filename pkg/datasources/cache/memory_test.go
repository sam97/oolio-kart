package cache

import (
	"testing"
	"time"
)

func newTestMemory(t *testing.T, maxEntries int) (*Memory, *time.Time) {
	t.Helper()
	mem, err := NewMemory(maxEntries)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mem.now = func() time.Time { return now }
	return mem, &now
}

func TestMemoryExpiry(t *testing.T) {
	ctx := t.Context()
	mem, now := newTestMemory(t, 10)

	mem.Set(ctx, "short", []byte("a"), time.Minute)
	mem.Set(ctx, "forever", []byte("b"), 0)

	if value, found, _ := mem.Get(ctx, "short"); !found || string(value) != "a" {
		t.Fatalf("Get(short) = %q, %v; want a, true", value, found)
	}

	*now = now.Add(time.Minute)
	if _, found, _ := mem.Get(ctx, "short"); found {
		t.Error("Get(short) found an expired entry")
	}
	if _, found, _ := mem.Get(ctx, "forever"); !found {
		t.Error("Get(forever) lost an entry without expiry")
	}

	mem.Delete(ctx, "forever")
	if _, found, _ := mem.Get(ctx, "forever"); found {
		t.Error("Get(forever) found a deleted entry")
	}
}

func TestMemoryEvictsLeastRecentlyUsed(t *testing.T) {
	ctx := t.Context()
	mem, _ := newTestMemory(t, 2)

	mem.Set(ctx, "a", []byte("1"), 0)
	mem.Set(ctx, "b", []byte("2"), 0)
	mem.Get(ctx, "a") // b is now least recently used
	mem.Set(ctx, "c", []byte("3"), 0)

	if _, found, _ := mem.Get(ctx, "b"); found {
		t.Error("b should have been evicted")
	}
	for _, key := range []string{"a", "c"} {
		if _, found, _ := mem.Get(ctx, key); !found {
			t.Errorf("%s should still be cached", key)
		}
	}
}

func TestMemoryCopiesValues(t *testing.T) {
	ctx := t.Context()
	mem, _ := newTestMemory(t, 2)

	value := []byte("abc")
	mem.Set(ctx, "key", value, 0)
	value[0] = 'x'

	got, _, _ := mem.Get(ctx, "key")
	got[1] = 'y'

	if again, _, _ := mem.Get(ctx, "key"); string(again) != "abc" {
		t.Errorf("cached value changed to %q", again)
	}
}

func TestJSONHelpers(t *testing.T) {
	ctx := t.Context()
	mem, _ := newTestMemory(t, 2)

	type payload struct {
		Name  string
		Count int
	}
	if err := SetJSON(ctx, mem, "key", payload{"x", 3}, 0); err != nil {
		t.Fatal(err)
	}
	got, found, err := GetJSON[payload](ctx, mem, "key")
	if err != nil || !found || got != (payload{"x", 3}) {
		t.Errorf("GetJSON = %+v, %v, %v", got, found, err)
	}
	if _, found, err := GetJSON[payload](ctx, mem, "missing"); found || err != nil {
		t.Errorf("GetJSON(missing) = %v, %v", found, err)
	}
}

func TestMemoryPurge(t *testing.T) {
	ctx := t.Context()
	mem, _ := newTestMemory(t, 4)
	mem.Set(ctx, "a", []byte("1"), 0)
	mem.Set(ctx, "b", []byte("2"), time.Minute)
	if err := mem.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a", "b"} {
		if _, found, _ := mem.Get(ctx, key); found {
			t.Errorf("%s survived Purge", key)
		}
	}
}
