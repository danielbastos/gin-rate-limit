package buckets

import (
	"context"
	"time"

	"github.com/danielbastos/gin-rate-limit/storage"
)

type Strategy struct {
	store      storage.BucketStore
	resolution time.Duration
}

func New(store storage.BucketStore, resolution time.Duration) *Strategy {
	return &Strategy{store: store, resolution: resolution}
}

func (s *Strategy) Get(ctx context.Context, key string, window time.Duration) (int64, error) {
	now, err := s.store.Now(ctx)
	if err != nil {
		return 0, err
	}
	now = now.UTC().Round(0)
	return s.store.SumBuckets(ctx, key, now.Add(-window), now, s.resolution)
}

func (s *Strategy) Hit(ctx context.Context, key string) error {
	now, err := s.store.Now(ctx)
	if err != nil {
		return err
	}
	return s.store.IncrementBucket(ctx, key, now.UTC().Truncate(s.resolution), s.resolution)
}

func (s *Strategy) Clean(ctx context.Context, window time.Duration) error {
	now, err := s.store.Now(ctx)
	if err != nil {
		return err
	}
	now = now.UTC().Round(0)
	return s.store.PruneBuckets(ctx, now.Add(-window), now.Truncate(s.resolution), s.resolution)
}

func (s *Strategy) CleanKey(ctx context.Context, key string, window time.Duration) error {
	now, err := s.store.Now(ctx)
	if err != nil {
		return err
	}
	now = now.UTC().Round(0)
	return s.store.PruneKeyBuckets(ctx, key, now.Add(-window), now.Truncate(s.resolution), s.resolution)
}
