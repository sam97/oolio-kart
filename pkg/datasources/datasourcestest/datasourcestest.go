// Package datasourcestest opens data stores for tests, each on its own
// migrated database schema.
package datasourcestest

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/sam97/oolio-kart/pkg/datasources"
	"github.com/sam97/oolio-kart/pkg/datasources/internal/datastores/postgres/postgrestest"
)

// Open returns stores on a fresh schema in the database at
// TEST_DATABASE_URL, migrated and dropped when the test ends. It skips the
// test if TEST_DATABASE_URL is unset.
func Open(t *testing.T, cache datasources.CacheConfig) *datasources.Stores {
	t.Helper()
	schemaURL := schemaURL(t)
	stores, err := datasources.Open(context.Background(), datasources.Config{DatabaseURL: schemaURL, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stores.Close)
	if err := stores.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return stores
}

// schemaURL creates a schema dropped when the test ends, and returns
// TEST_DATABASE_URL with its search_path set to that schema.
func schemaURL(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	schema := postgrestest.Schema(t, base)
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
