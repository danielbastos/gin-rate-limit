package tests

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/danielbastos/gin-rate-limit/storage"
	postgresstore "github.com/danielbastos/gin-rate-limit/storage/postgres"
	redisstore "github.com/danielbastos/gin-rate-limit/storage/redis"
	"github.com/danielbastos/gin-rate-limit/tests/contract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	redisclient "github.com/redis/go-redis/v9"
)

func requireIntegration(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		if os.Getenv("RATELIMIT_REQUIRE_INTEGRATION") == "1" {
			t.Fatalf("%s is required", name)
		}
		t.Skipf("set %s to run real-service integration tests", name)
	}
	return value
}

func uniqueName() string {
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return "rltest_" + hex.EncodeToString(id[:])
}

func newRedis(t *testing.T, addr string) (*redisclient.Client, string) {
	t.Helper()
	client := redisclient.NewClient(&redisclient.Options{Addr: addr, MaxRetries: -1, ContextTimeoutEnabled: true})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	prefix := uniqueName()
	t.Cleanup(func() {
		defer client.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Collect before deleting so the cleanup does not mutate a SCAN iteration.
		var keys []string
		var cursor uint64
		for {
			batch, next, err := client.Scan(ctx, cursor, prefix+":*", 128).Result()
			if err != nil {
				t.Error(err)
				return
			}
			keys = append(keys, batch...)
			if next == 0 {
				break
			}
			cursor = next
		}
		if len(keys) > 0 {
			if err := client.Del(ctx, keys...).Err(); err != nil {
				t.Error(err)
			}
		}
	})
	return client, prefix
}

func TestRedis(t *testing.T) {
	addr := requireIntegration(t, "RATELIMIT_REDIS_ADDR")
	events := func(t *testing.T) storage.EventStore {
		client, prefix := newRedis(t, addr)
		s, err := redisstore.NewEventStore(client, prefix)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	buckets := func(t *testing.T) storage.BucketStore {
		client, prefix := newRedis(t, addr)
		s, err := redisstore.NewBucketStore(client, prefix)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	t.Run("events", func(t *testing.T) { contract.RunEventStore(t, events) })
	t.Run("buckets", func(t *testing.T) { contract.RunBucketStore(t, buckets) })
	t.Run("limiters", func(t *testing.T) { contract.RunLimiters(t, events, buckets) })
	t.Run("gin", func(t *testing.T) { contract.RunGinMiddleware(t, events, buckets) })
	t.Run("shared_clients_and_private_prefix", func(t *testing.T) {
		client, prefix := newRedis(t, addr)
		second := redisclient.NewClient(&redisclient.Options{Addr: addr})
		t.Cleanup(func() { _ = second.Close() })
		a, _ := redisstore.NewEventStore(client, prefix)
		b, _ := redisstore.NewEventStore(second, prefix)
		isolated, _ := redisstore.NewEventStore(client, prefix+":other")
		ctx := context.Background()
		now := time.Now()
		if err := a.Append(ctx, "a", now); err != nil {
			t.Fatal(err)
		}
		if err := isolated.Append(ctx, "a", now); err != nil {
			t.Fatal(err)
		}
		if n, err := b.Count(ctx, "a", now.Add(-time.Second), now); err != nil || n != 1 {
			t.Fatalf("count=%d err=%v", n, err)
		}
		if err := b.Prune(ctx, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if n, err := isolated.Count(ctx, "a", now.Add(-time.Second), now); err != nil || n != 1 {
			t.Fatalf("isolated count=%d err=%v", n, err)
		}
	})
	t.Run("closed_connection", func(t *testing.T) {
		client := redisclient.NewClient(&redisclient.Options{Addr: addr})
		s, _ := redisstore.NewEventStore(client, uniqueName())
		_ = client.Close()
		if _, err := s.Now(context.Background()); err == nil {
			t.Fatal("expected closed client error")
		}
	})
}

func newPostgres(t *testing.T, dsn string) (*pgxpool.Pool, string) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := uniqueName()
	identifier := pgx.Identifier{schema}.Sanitize()
	t.Cleanup(func() {
		defer pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+identifier); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, postgresstore.SchemaSQL); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return pool, schema
}

func TestPostgres(t *testing.T) {
	dsn := requireIntegration(t, "RATELIMIT_POSTGRES_DSN")
	events := func(t *testing.T) storage.EventStore {
		pool, schema := newPostgres(t, dsn)
		s, err := postgresstore.NewEventStore(pool, schema)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	buckets := func(t *testing.T) storage.BucketStore {
		pool, schema := newPostgres(t, dsn)
		s, err := postgresstore.NewBucketStore(pool, schema)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	t.Run("events", func(t *testing.T) { contract.RunEventStore(t, events) })
	t.Run("buckets", func(t *testing.T) { contract.RunBucketStore(t, buckets) })
	t.Run("limiters", func(t *testing.T) { contract.RunLimiters(t, events, buckets) })
	t.Run("gin", func(t *testing.T) { contract.RunGinMiddleware(t, events, buckets) })
	t.Run("schema_isolation_and_shared_pools", func(t *testing.T) {
		pool, schema := newPostgres(t, dsn)
		otherPool, otherSchema := newPostgres(t, dsn)
		a, _ := postgresstore.NewEventStore(pool, schema)
		b, _ := postgresstore.NewEventStore(otherPool, schema)
		isolated, _ := postgresstore.NewEventStore(otherPool, otherSchema)
		ctx, now := context.Background(), time.Now()
		for _, s := range []*postgresstore.EventStore{a, isolated} {
			if err := s.Append(ctx, "a", now); err != nil {
				t.Fatal(err)
			}
		}
		if n, err := b.Count(ctx, "a", now.Add(-time.Second), now); err != nil || n != 1 {
			t.Fatalf("count=%d err=%v", n, err)
		}
		if err := b.Prune(ctx, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if n, err := isolated.Count(ctx, "a", now.Add(-time.Second), now); err != nil || n != 1 {
			t.Fatalf("count=%d err=%v", n, err)
		}
	})
	t.Run("migration_is_explicit", func(t *testing.T) {
		pool, schema := newPostgres(t, dsn)
		missing, err := postgresstore.NewEventStore(pool, schema+"_missing")
		if err != nil {
			t.Fatal(err)
		}
		if err := missing.Append(context.Background(), "a", time.Now()); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("expected missing relation, got %v", err)
		}
	})
}
