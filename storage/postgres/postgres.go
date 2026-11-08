// Package postgres implements storage using explicitly migrated PostgreSQL
// tables. The caller owns the database pool and schema lifecycle.
package postgres

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/danielbastos/gin-rate-limit/internal/instant"
	"github.com/danielbastos/gin-rate-limit/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SchemaSQL is an explicit migration for the library tables and indexes. Apply
// it using your migration tooling and a dedicated schema/search_path.
//
//go:embed schema.sql
var SchemaSQL string

var ErrInvalidConfig = errors.New("ratelimit/postgres: pool and nonempty schema required")

type store struct {
	pool    *pgxpool.Pool
	events  string
	buckets string
}

type EventStore struct{ store }
type BucketStore struct{ store }

var _ storage.EventStore = (*EventStore)(nil)
var _ storage.BucketStore = (*BucketStore)(nil)

func newStore(pool *pgxpool.Pool, schema string) (store, error) {
	if pool == nil || schema == "" {
		return store{}, ErrInvalidConfig
	}
	return store{pool, pgx.Identifier{schema, "ratelimit_events"}.Sanitize(), pgx.Identifier{schema, "ratelimit_buckets"}.Sanitize()}, nil
}

// NewEventStore constructs an adapter without connecting or running migrations.
func NewEventStore(pool *pgxpool.Pool, schema string) (*EventStore, error) {
	s, err := newStore(pool, schema)
	if err != nil {
		return nil, err
	}
	return &EventStore{s}, nil
}

func NewBucketStore(pool *pgxpool.Pool, schema string) (*BucketStore, error) {
	s, err := newStore(pool, schema)
	if err != nil {
		return nil, err
	}
	return &BucketStore{s}, nil
}

func (s store) Now(ctx context.Context) (time.Time, error) {
	var now time.Time
	err := s.pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now)
	return now.UTC(), err
}

func (s *EventStore) Append(ctx context.Context, key string, at time.Time) error {
	_, err := s.pool.Exec(ctx, "INSERT INTO "+s.events+" (key, at_ns) VALUES ($1, $2::numeric)", key, instant.Decimal(at))
	return err
}

func (s *EventStore) Count(ctx context.Context, key string, from, to time.Time) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx, "SELECT count(*) FROM "+s.events+" WHERE key=$1 AND at_ns>$2::numeric AND at_ns<=$3::numeric", key, instant.Decimal(from), instant.Decimal(to)).Scan(&count)
	return count, err
}

// deleteBatches keeps each statement bounded. No transaction spans all keys.
// Successful batches are retained if a later batch fails or is canceled.
func (s store) deleteBatches(ctx context.Context, table, predicate string, args ...any) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE ctid IN (SELECT ctid FROM %s WHERE %s LIMIT 512)", table, table, predicate)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := s.pool.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		if result.RowsAffected() < 512 {
			return nil
		}
	}
}

func (s *EventStore) Prune(ctx context.Context, before time.Time) error {
	return s.deleteBatches(ctx, s.events, "at_ns < $1::numeric", instant.Decimal(before))
}

func (s *EventStore) PruneKey(ctx context.Context, key string, before time.Time) error {
	return s.deleteBatches(ctx, s.events, "key=$1 AND at_ns < $2::numeric", key, instant.Decimal(before))
}

func (s *BucketStore) IncrementBucket(ctx context.Context, key string, start time.Time, resolution time.Duration) error {
	_, err := s.pool.Exec(ctx, "INSERT INTO "+s.buckets+" AS b (key, resolution_ns, start_ns, count) VALUES ($1, $2, $3::numeric, 1) ON CONFLICT (key, resolution_ns, start_ns) DO UPDATE SET count=b.count+1", key, int64(resolution), instant.Decimal(start))
	return err
}

func (s *BucketStore) SumBuckets(ctx context.Context, key string, from, to time.Time, resolution time.Duration) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx, "SELECT COALESCE(sum(count),0)::bigint FROM "+s.buckets+" WHERE key=$1 AND resolution_ns=$2 AND start_ns>$3::numeric AND start_ns<=$4::numeric", key, int64(resolution), instant.Decimal(from.Add(-resolution)), instant.Decimal(to)).Scan(&count)
	return count, err
}

func (s *BucketStore) PruneBuckets(ctx context.Context, before, current time.Time, resolution time.Duration) error {
	return s.deleteBatches(ctx, s.buckets, "resolution_ns=$1 AND start_ns<$2::numeric AND start_ns<$3::numeric", int64(resolution), instant.Decimal(before), instant.Decimal(current))
}

func (s *BucketStore) PruneKeyBuckets(ctx context.Context, key string, before, current time.Time, resolution time.Duration) error {
	return s.deleteBatches(ctx, s.buckets, "key=$1 AND resolution_ns=$2 AND start_ns<$3::numeric AND start_ns<$4::numeric", key, int64(resolution), instant.Decimal(before), instant.Decimal(current))
}
