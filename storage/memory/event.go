// Package memory implements process-local, concurrency-safe storage without TTL
// or automatic retention. Zero-value stores are ready to use.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/danielbastos/gin-rate-limit/storage"
)

type EventStore struct {
	mu     sync.RWMutex
	events map[string][]time.Time
}

var _ storage.EventStore = (*EventStore)(nil)

func NewEventStore() *EventStore { return &EventStore{} }

func (*EventStore) Now(ctx context.Context) (time.Time, error) {
	return time.Now().UTC(), ctx.Err()
}

func (s *EventStore) Append(ctx context.Context, key string, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.events == nil {
		s.events = make(map[string][]time.Time)
	}
	s.events[key] = append(s.events[key], at.UTC().Round(0))
	return nil
}

func (s *EventStore) Count(ctx context.Context, key string, from, to time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var count int64
	for i, at := range s.events[key] {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		if at.After(from) && !at.After(to) {
			count++
		}
	}
	return count, ctx.Err()
}

func (s *EventStore) Prune(ctx context.Context, before time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.events {
		if err := s.pruneKey(ctx, key, before); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *EventStore) PruneKey(ctx context.Context, key string, before time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneKey(ctx, key, before)
}

// Called with the write lock held. Build a new slice so cancellation cannot
// leave a partially compacted history with duplicated occurrences.
func (s *EventStore) pruneKey(ctx context.Context, key string, before time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var kept []time.Time
	for i, at := range s.events[key] {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if !at.Before(before) {
			kept = append(kept, at)
		}
	}
	if len(kept) == 0 {
		delete(s.events, key)
	} else {
		s.events[key] = kept
	}
	return nil
}
