package memory

import (
	"context"
	"sync"
	"time"

	"github.com/danielbastos/gin-rate-limit/storage"
)

type series struct {
	key        string
	resolution time.Duration
}

type BucketStore struct {
	mu      sync.RWMutex
	buckets map[series]map[time.Time]int64
}

var _ storage.BucketStore = (*BucketStore)(nil)

func NewBucketStore() *BucketStore { return &BucketStore{} }

func (*BucketStore) Now(ctx context.Context) (time.Time, error) {
	return time.Now().UTC(), ctx.Err()
}

func (s *BucketStore) IncrementBucket(ctx context.Context, key string, start time.Time, resolution time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.buckets == nil {
		s.buckets = make(map[series]map[time.Time]int64)
	}
	id := series{key, resolution}
	if s.buckets[id] == nil {
		s.buckets[id] = make(map[time.Time]int64)
	}
	s.buckets[id][start.UTC().Round(0)]++
	return nil
}

func (s *BucketStore) SumBuckets(ctx context.Context, key string, from, to time.Time, resolution time.Duration) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int64
	for start, count := range s.buckets[series{key, resolution}] {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if !start.After(to) && start.Add(resolution).After(from) {
			total += count
		}
	}
	return total, ctx.Err()
}

func (s *BucketStore) PruneBuckets(ctx context.Context, before, currentBucketStart time.Time, resolution time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.buckets {
		if err := ctx.Err(); err != nil {
			return err
		}
		if id.resolution == resolution {
			if err := s.pruneKey(ctx, id, before, currentBucketStart); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (s *BucketStore) PruneKeyBuckets(ctx context.Context, key string, before, currentBucketStart time.Time, resolution time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneKey(ctx, series{key, resolution}, before, currentBucketStart)
}

func (s *BucketStore) pruneKey(ctx context.Context, id series, before, currentBucketStart time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for start := range s.buckets[id] {
		if err := ctx.Err(); err != nil {
			return err
		}
		if start.Before(before) && start.Before(currentBucketStart) {
			delete(s.buckets[id], start)
		}
	}
	if len(s.buckets[id]) == 0 {
		delete(s.buckets, id)
	}
	return nil
}
