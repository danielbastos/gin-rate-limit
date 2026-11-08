package eventlog

import (
	"context"
	"time"

	"github.com/danielbastos/gin-rate-limit/storage"
)

type Strategy struct{ store storage.EventStore }

func New(store storage.EventStore) *Strategy { return &Strategy{store: store} }

func (s *Strategy) Get(ctx context.Context, key string, window time.Duration) (int64, error) {
	now, err := s.store.Now(ctx)
	if err != nil {
		return 0, err
	}
	now = now.UTC().Round(0)
	return s.store.Count(ctx, key, now.Add(-window), now)
}

func (s *Strategy) Hit(ctx context.Context, key string) error {
	now, err := s.store.Now(ctx)
	if err != nil {
		return err
	}
	return s.store.Append(ctx, key, now.UTC().Round(0))
}

func (s *Strategy) Clean(ctx context.Context, window time.Duration) error {
	now, err := s.store.Now(ctx)
	if err != nil {
		return err
	}
	return s.store.Prune(ctx, now.UTC().Round(0).Add(-window))
}

func (s *Strategy) CleanKey(ctx context.Context, key string, window time.Duration) error {
	now, err := s.store.Now(ctx)
	if err != nil {
		return err
	}
	return s.store.PruneKey(ctx, key, now.UTC().Round(0).Add(-window))
}
