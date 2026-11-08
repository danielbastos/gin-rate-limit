// Package storage defines persistence contracts independent of public key types.
// Implementations must support concurrent calls, preserve error identity and
// honor context cancellation. Cleanup may be partial when interrupted.
package storage

import (
	"context"
	"time"
)

// EventStore persists individual occurrences, including identical timestamps.
// Each successful Append must retain one occurrence, even under concurrency.
type EventStore interface {
	Now(ctx context.Context) (time.Time, error)
	Append(ctx context.Context, key string, at time.Time) error
	// Count counts events in (from, to].
	Count(ctx context.Context, key string, from, to time.Time) (int64, error)
	// Prune deletes events strictly older than before across this store.
	Prune(ctx context.Context, before time.Time) error
	PruneKey(ctx context.Context, key string, before time.Time) error
}

// BucketStore keeps atomic counters identified by key, resolution and start.
// Strategies provide aligned starts; adapters must not realign them.
type BucketStore interface {
	Now(ctx context.Context) (time.Time, error)
	IncrementBucket(ctx context.Context, key string, start time.Time, resolution time.Duration) error
	// SumBuckets includes [start, start+resolution) intersecting (from, to]:
	// start <= to && start+resolution > from.
	SumBuckets(ctx context.Context, key string, from, to time.Time, resolution time.Duration) (int64, error)
	// PruneBuckets removes only the requested resolution, when
	// start < before && start < currentBucketStart, preserving open/future buckets.
	PruneBuckets(ctx context.Context, before, currentBucketStart time.Time, resolution time.Duration) error
	PruneKeyBuckets(ctx context.Context, key string, before, currentBucketStart time.Time, resolution time.Duration) error
}
