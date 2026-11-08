package postgres_test

import (
	"context"
	"errors"
	"testing"

	store "github.com/danielbastos/gin-rate-limit/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInvalidConfiguration(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://localhost:1/unused")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, tc := range []struct {
		pool   *pgxpool.Pool
		schema string
	}{{nil, "test"}, {pool, ""}} {
		if _, err := store.NewEventStore(tc.pool, tc.schema); !errors.Is(err, store.ErrInvalidConfig) {
			t.Fatalf("event store error=%v", err)
		}
		if _, err := store.NewBucketStore(tc.pool, tc.schema); !errors.Is(err, store.ErrInvalidConfig) {
			t.Fatalf("bucket store error=%v", err)
		}
	}
}
