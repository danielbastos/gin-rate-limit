// Package contract contains reusable conformance tests for storage adapters.
// Factories must return a new isolated store and register cleanup with t.Cleanup.
package contract

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ratelimit "github.com/danielbastos/gin-rate-limit"
	"github.com/danielbastos/gin-rate-limit/storage"
)

var epoch = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func requireNoError(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func requireCount(t testing.TB, count int64, err error, want int64) {
	t.Helper()
	requireNoError(t, err)
	if count != want {
		t.Fatalf("count = %d, want %d", count, want)
	}
}

func RunEventStore(t *testing.T, factory func(*testing.T) storage.EventStore) {
	t.Helper()
	ctx := context.Background()
	t.Run("clock", func(t *testing.T) {
		s := factory(t)
		before := time.Now().Add(-5 * time.Second)
		now, err := s.Now(ctx)
		requireNoError(t, err)
		if now.Before(before) || now.After(time.Now().Add(5*time.Second)) {
			t.Fatalf("unexpected clock: %s", now)
		}
	})
	t.Run("boundaries_and_duplicate_timestamps", func(t *testing.T) {
		s := factory(t)
		from, to := epoch, epoch.Add(time.Second)
		n, err := s.Count(ctx, "a", from, to)
		requireCount(t, n, err, 0)
		for _, at := range []time.Time{from.Add(-time.Nanosecond), from, from.Add(time.Nanosecond), to, to, to.Add(time.Nanosecond)} {
			requireNoError(t, s.Append(ctx, "a", at))
		}
		requireNoError(t, s.Append(ctx, "b", to))
		n, err = s.Count(ctx, "a", from, to)
		requireCount(t, n, err, 3)
		n, err = s.Count(ctx, "b", from, to)
		requireCount(t, n, err, 1)
	})
	t.Run("cleanup_strict_cutoff_and_key_isolation", func(t *testing.T) {
		s := factory(t)
		for _, key := range []string{"a", "b"} {
			for _, at := range []time.Time{epoch.Add(-time.Nanosecond), epoch, epoch.Add(time.Nanosecond)} {
				requireNoError(t, s.Append(ctx, key, at))
			}
		}
		requireNoError(t, s.PruneKey(ctx, "missing", epoch))
		requireNoError(t, s.PruneKey(ctx, "a", epoch))
		n, err := s.Count(ctx, "a", epoch.Add(-time.Second), epoch.Add(time.Second))
		requireCount(t, n, err, 2)
		n, err = s.Count(ctx, "b", epoch.Add(-time.Second), epoch.Add(time.Second))
		requireCount(t, n, err, 3)
		requireNoError(t, s.Prune(ctx, epoch))
		n, err = s.Count(ctx, "b", epoch.Add(-time.Second), epoch.Add(time.Second))
		requireCount(t, n, err, 2)
		requireNoError(t, s.Prune(ctx, epoch.Add(time.Second)))
		n, err = s.Count(ctx, "a", epoch.Add(-time.Second), epoch.Add(time.Second))
		requireCount(t, n, err, 0)
	})
	t.Run("unordered_timestamps_and_timezone", func(t *testing.T) {
		s := factory(t)
		for _, at := range []time.Time{epoch.Add(2 * time.Second), epoch, epoch.Add(time.Second).In(time.FixedZone("offset", -3*3600))} {
			requireNoError(t, s.Append(ctx, "a", at))
		}
		n, err := s.Count(ctx, "a", epoch, epoch.Add(time.Second))
		requireCount(t, n, err, 1)
	})
	t.Run("dates_outside_unix_nano_range", func(t *testing.T) {
		s := factory(t)
		for _, at := range []time.Time{
			time.Date(1, 1, 1, 0, 0, 0, 1, time.UTC),
			time.Unix(-1, 999999999),
			time.Date(9999, 12, 31, 0, 0, 0, 1, time.UTC),
		} {
			requireNoError(t, s.Append(ctx, "a", at))
			n, err := s.Count(ctx, "a", at.Add(-time.Nanosecond), at)
			requireCount(t, n, err, 1)
		}
	})
	t.Run("cleanup_multiple_batches_and_reinsert", func(t *testing.T) {
		s := factory(t)
		for i := 0; i < 513; i++ {
			requireNoError(t, s.Append(ctx, "a", epoch))
		}
		requireNoError(t, s.Prune(ctx, epoch.Add(time.Second)))
		n, err := s.Count(ctx, "a", epoch.Add(-time.Second), epoch)
		requireCount(t, n, err, 0)
		requireNoError(t, s.Append(ctx, "a", epoch))
		requireNoError(t, s.Prune(ctx, epoch.Add(time.Second)))
		n, err = s.Count(ctx, "a", epoch.Add(-time.Second), epoch)
		requireCount(t, n, err, 0)
	})
	t.Run("concurrent_cleanup_reinsert", func(t *testing.T) {
		s := factory(t)
		parallel(t, 4, func() error {
			for i := 0; i < 10; i++ {
				if err := s.Append(ctx, "a", epoch); err != nil {
					return err
				}
				if err := s.Prune(ctx, epoch.Add(time.Second)); err != nil {
					return err
				}
				if err := s.PruneKey(ctx, "a", epoch.Add(time.Second)); err != nil {
					return err
				}
			}
			return nil
		})
		requireNoError(t, s.Append(ctx, "a", epoch))
		n, err := s.Count(ctx, "a", epoch.Add(-time.Second), epoch)
		requireCount(t, n, err, 1)
		requireNoError(t, s.Prune(ctx, epoch.Add(time.Second)))
		n, err = s.Count(ctx, "a", epoch.Add(-time.Second), epoch)
		requireCount(t, n, err, 0)
	})
	t.Run("concurrent_append", func(t *testing.T) {
		s := factory(t)
		parallel(t, 8, func() error {
			for i := 0; i < 25; i++ {
				if err := s.Append(ctx, "a", epoch); err != nil {
					return err
				}
			}
			return nil
		})
		n, err := s.Count(ctx, "a", epoch.Add(-time.Second), epoch)
		requireCount(t, n, err, 200)
	})
	t.Run("cancellation", func(t *testing.T) {
		s := factory(t)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, e1 := s.Now(canceled)
		_, e2 := s.Count(canceled, "a", epoch, epoch.Add(time.Second))
		for i, err := range []error{e1, e2, s.Append(canceled, "a", epoch), s.Prune(canceled, epoch), s.PruneKey(canceled, "a", epoch)} {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("operation %d: %v", i, err)
			}
		}
		n, err := s.Count(ctx, "a", epoch.Add(-time.Second), epoch)
		requireCount(t, n, err, 0)
	})
}

func RunBucketStore(t *testing.T, factory func(*testing.T) storage.BucketStore) {
	t.Helper()
	ctx := context.Background()
	t.Run("clock", func(t *testing.T) {
		now, err := factory(t).Now(ctx)
		requireNoError(t, err)
		if delta := time.Since(now); delta < -5*time.Second || delta > 5*time.Second {
			t.Fatalf("unexpected clock: %s", now)
		}
	})
	t.Run("intersection_boundaries_and_resolution", func(t *testing.T) {
		s := factory(t)
		second := time.Second
		n, err := s.SumBuckets(ctx, "a", epoch, epoch.Add(second), second)
		requireCount(t, n, err, 0)
		for _, start := range []time.Time{epoch.Add(-second), epoch, epoch.Add(second), epoch.Add(2 * second)} {
			requireNoError(t, s.IncrementBucket(ctx, "a", start, second))
		}
		requireNoError(t, s.IncrementBucket(ctx, "a", epoch, time.Minute))
		requireNoError(t, s.IncrementBucket(ctx, "b", epoch, second))
		n, err = s.SumBuckets(ctx, "a", epoch, epoch.Add(second), second)
		requireCount(t, n, err, 2)
		n, err = s.SumBuckets(ctx, "a", epoch.Add(-time.Nanosecond), epoch, second)
		requireCount(t, n, err, 2)
		n, err = s.SumBuckets(ctx, "a", epoch.Add(time.Nanosecond), epoch.Add(2*time.Nanosecond), second)
		requireCount(t, n, err, 1)
		n, err = s.SumBuckets(ctx, "a", epoch, epoch.Add(second), time.Minute)
		requireCount(t, n, err, 1)
	})
	t.Run("cleanup_preserves_open_and_other_resolutions", func(t *testing.T) {
		s := factory(t)
		minute := time.Minute
		for _, key := range []string{"a", "b"} {
			for _, start := range []time.Time{epoch.Add(-minute), epoch, epoch.Add(minute)} {
				requireNoError(t, s.IncrementBucket(ctx, key, start, minute))
			}
			requireNoError(t, s.IncrementBucket(ctx, key, epoch.Add(-minute), time.Hour))
		}
		cutoff := epoch.Add(30 * time.Second)
		requireNoError(t, s.PruneKeyBuckets(ctx, "missing", cutoff, epoch, minute))
		requireNoError(t, s.PruneKeyBuckets(ctx, "a", cutoff, epoch, minute))
		n, err := s.SumBuckets(ctx, "a", epoch.Add(-2*minute), epoch.Add(2*minute), minute)
		requireCount(t, n, err, 2)
		n, err = s.SumBuckets(ctx, "b", epoch.Add(-2*minute), epoch.Add(2*minute), minute)
		requireCount(t, n, err, 3)
		requireNoError(t, s.PruneBuckets(ctx, cutoff, epoch, minute))
		n, err = s.SumBuckets(ctx, "b", epoch.Add(-2*minute), epoch.Add(2*minute), minute)
		requireCount(t, n, err, 2)
		n, err = s.SumBuckets(ctx, "a", epoch.Add(-2*minute), epoch, time.Hour)
		requireCount(t, n, err, 1)
		// A bucket exactly at the cutoff must also survive.
		requireNoError(t, s.PruneBuckets(ctx, epoch, epoch.Add(minute), minute))
		n, err = s.SumBuckets(ctx, "a", epoch.Add(-minute), epoch, minute)
		requireCount(t, n, err, 1)
	})
	t.Run("concurrent_increment", func(t *testing.T) {
		s := factory(t)
		parallel(t, 8, func() error {
			for i := 0; i < 25; i++ {
				if err := s.IncrementBucket(ctx, "a", epoch, time.Second); err != nil {
					return err
				}
			}
			return nil
		})
		n, err := s.SumBuckets(ctx, "a", epoch.Add(-time.Second), epoch, time.Second)
		requireCount(t, n, err, 200)
	})
	t.Run("cleanup_multiple_batches_and_reinsert", func(t *testing.T) {
		s := factory(t)
		for i := 0; i < 513; i++ {
			requireNoError(t, s.IncrementBucket(ctx, "a", epoch.Add(-time.Duration(i)*time.Second), time.Second))
		}
		requireNoError(t, s.PruneBuckets(ctx, epoch.Add(time.Second), epoch.Add(time.Second), time.Second))
		n, err := s.SumBuckets(ctx, "a", epoch.Add(-time.Hour), epoch, time.Second)
		requireCount(t, n, err, 0)
		requireNoError(t, s.IncrementBucket(ctx, "a", epoch, time.Second))
		requireNoError(t, s.PruneBuckets(ctx, epoch.Add(time.Second), epoch.Add(time.Second), time.Second))
		n, err = s.SumBuckets(ctx, "a", epoch.Add(-time.Hour), epoch, time.Second)
		requireCount(t, n, err, 0)
	})
	t.Run("concurrent_cleanup_reinsert", func(t *testing.T) {
		s := factory(t)
		parallel(t, 4, func() error {
			for i := 0; i < 10; i++ {
				if err := s.IncrementBucket(ctx, "a", epoch, time.Second); err != nil {
					return err
				}
				if err := s.PruneBuckets(ctx, epoch.Add(time.Second), epoch.Add(time.Second), time.Second); err != nil {
					return err
				}
				if err := s.PruneKeyBuckets(ctx, "a", epoch.Add(time.Second), epoch.Add(time.Second), time.Second); err != nil {
					return err
				}
			}
			return nil
		})
		requireNoError(t, s.IncrementBucket(ctx, "a", epoch, time.Second))
		n, err := s.SumBuckets(ctx, "a", epoch.Add(-time.Second), epoch, time.Second)
		requireCount(t, n, err, 1)
		requireNoError(t, s.PruneBuckets(ctx, epoch.Add(time.Second), epoch.Add(time.Second), time.Second))
		n, err = s.SumBuckets(ctx, "a", epoch.Add(-time.Second), epoch, time.Second)
		requireCount(t, n, err, 0)
	})
	t.Run("cancellation", func(t *testing.T) {
		s := factory(t)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, e1 := s.Now(canceled)
		_, e2 := s.SumBuckets(canceled, "a", epoch, epoch.Add(time.Second), time.Second)
		for i, err := range []error{e1, e2, s.IncrementBucket(canceled, "a", epoch, time.Second), s.PruneBuckets(canceled, epoch, epoch, time.Second), s.PruneKeyBuckets(canceled, "a", epoch, epoch, time.Second)} {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("operation %d: %v", i, err)
			}
		}
		n, err := s.SumBuckets(ctx, "a", epoch.Add(-time.Second), epoch, time.Second)
		requireCount(t, n, err, 0)
	})
}

func parallel(t *testing.T, workers int, work func() error) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- work() }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		requireNoError(t, err)
	}
}

type fixedEvents struct {
	storage.EventStore
	now time.Time
}

func (s fixedEvents) Now(ctx context.Context) (time.Time, error) { return s.now, ctx.Err() }

type fixedBuckets struct {
	storage.BucketStore
	now time.Time
}

func (s fixedBuckets) Now(ctx context.Context) (time.Time, error) { return s.now, ctx.Err() }

// RunLimiters checks the public strategies over stores using controlled time.
func RunLimiters(t *testing.T, events func(*testing.T) storage.EventStore, buckets func(*testing.T) storage.BucketStore) {
	t.Helper()
	ctx := context.Background()
	key, err := ratelimit.NewKey("contract", "id", "a")
	requireNoError(t, err)
	other, err := ratelimit.NewKey("contract", "id", "b")
	requireNoError(t, err)
	for _, strategy := range []string{"events", "buckets"} {
		t.Run(strategy, func(t *testing.T) {
			var l ratelimit.RateLimit
			var err error
			if strategy == "events" {
				l, err = ratelimit.NewEventLog(fixedEvents{events(t), epoch})
			} else {
				l, err = ratelimit.NewBuckets(fixedBuckets{buckets(t), epoch}, time.Second)
			}
			requireNoError(t, err)
			n, err := l.Get(ctx, key, time.Nanosecond)
			requireCount(t, n, err, 0)
			requireNoError(t, l.Hit(ctx, key))
			requireNoError(t, l.Hit(ctx, key))
			n, err = l.Get(ctx, key, time.Nanosecond)
			requireCount(t, n, err, 2)
			n, err = l.Get(ctx, other, time.Hour)
			requireCount(t, n, err, 0)
			requireNoError(t, l.Clean(ctx, time.Nanosecond))
			requireNoError(t, l.CleanKey(ctx, key, time.Nanosecond))
			n, err = l.Get(ctx, key, time.Nanosecond)
			requireCount(t, n, err, 2)
		})
	}
	t.Run("seven_second_alignment", func(t *testing.T) {
		s := buckets(t)
		now := epoch.Add(3*time.Second + 123*time.Nanosecond).In(time.FixedZone("offset", -10800))
		l, err := ratelimit.NewBuckets(fixedBuckets{s, now}, 7*time.Second)
		requireNoError(t, err)
		requireNoError(t, l.Hit(ctx, key))
		start := now.UTC().Truncate(7 * time.Second)
		n, err := s.SumBuckets(ctx, key.String(), start.Add(-time.Nanosecond), start, 7*time.Second)
		requireCount(t, n, err, 1)
	})
	t.Run("partial_history_after_cleanup", func(t *testing.T) {
		now := epoch.Add(2500 * time.Millisecond)
		e := events(t)
		for _, at := range []time.Time{epoch, epoch.Add(1600 * time.Millisecond), now} {
			requireNoError(t, e.Append(ctx, key.String(), at))
		}
		l, err := ratelimit.NewEventLog(fixedEvents{e, now})
		requireNoError(t, err)
		requireNoError(t, l.CleanKey(ctx, key, time.Second))
		n, err := l.Get(ctx, key, time.Hour)
		requireCount(t, n, err, 2)
		b := buckets(t)
		for _, start := range []time.Time{epoch.Add(time.Second), epoch.Add(2 * time.Second)} {
			requireNoError(t, b.IncrementBucket(ctx, key.String(), start, time.Second))
		}
		l, err = ratelimit.NewBuckets(fixedBuckets{b, now}, time.Second)
		requireNoError(t, err)
		n, err = l.Get(ctx, key, time.Second)
		requireCount(t, n, err, 2)
		requireNoError(t, l.Clean(ctx, time.Second))
		n, err = l.Get(ctx, key, time.Hour)
		requireCount(t, n, err, 1)
	})
	t.Run("mixed_concurrency", func(t *testing.T) {
		for _, strategy := range []string{"events", "buckets"} {
			t.Run(strategy, func(t *testing.T) {
				var l ratelimit.RateLimit
				var err error
				if strategy == "events" {
					l, err = ratelimit.NewEventLog(fixedEvents{events(t), epoch})
				} else {
					l, err = ratelimit.NewBuckets(fixedBuckets{buckets(t), epoch}, time.Second)
				}
				requireNoError(t, err)
				parallel(t, 4, func() error {
					for i := 0; i < 20; i++ {
						if err := l.Hit(ctx, key); err != nil {
							return err
						}
						if _, err := l.Get(ctx, key, time.Second); err != nil {
							return err
						}
						if err := l.CleanKey(ctx, key, time.Second); err != nil {
							return err
						}
						if err := l.Clean(ctx, time.Second); err != nil {
							return err
						}
					}
					return nil
				})
				n, err := l.Get(ctx, key, time.Second)
				requireCount(t, n, err, 80)
			})
		}
	})
}
