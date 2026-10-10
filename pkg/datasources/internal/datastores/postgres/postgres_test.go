package postgres_test

import (
	"testing"

	"github.com/sam97/oolio-kart/pkg/datasources/internal/datastores/postgres"
	"github.com/sam97/oolio-kart/pkg/datasources/internal/datastores/postgres/postgrestest"
)

func TestMigrateTwice(t *testing.T) {
	pool := postgrestest.New(t) // migrated once already
	if err := postgres.Migrate(t.Context(), pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestLock(t *testing.T) {
	pool := postgrestest.New(t)
	first, second := postgres.NewLock(pool, 42), postgres.NewLock(pool, 42)

	unlock, ok, err := first.TryLock(t.Context())
	if err != nil || !ok {
		t.Fatalf("first TryLock = %v, %v", ok, err)
	}
	if _, ok, err := second.TryLock(t.Context()); err != nil || ok {
		t.Fatalf("second TryLock while held = %v, %v", ok, err)
	}
	unlock()
	unlock, ok, err = second.TryLock(t.Context())
	if err != nil || !ok {
		t.Fatalf("second TryLock after unlock = %v, %v", ok, err)
	}
	unlock()
}
