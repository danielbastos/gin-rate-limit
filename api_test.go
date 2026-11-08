package ratelimit_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	ratelimit "github.com/danielbastos/gin-rate-limit"
	"github.com/danielbastos/gin-rate-limit/storage/memory"
)

func TestConstructorsRejectInvalidStoresAndResolution(t *testing.T) {
	var events *memory.EventStore
	var buckets *memory.BucketStore
	for _, build := range []func() (ratelimit.RateLimit, error){
		func() (ratelimit.RateLimit, error) { return ratelimit.NewEventLog(nil) },
		func() (ratelimit.RateLimit, error) { return ratelimit.NewEventLog(events) },
		func() (ratelimit.RateLimit, error) { return ratelimit.NewBuckets(nil, time.Second) },
		func() (ratelimit.RateLimit, error) { return ratelimit.NewBuckets(buckets, time.Second) },
	} {
		l, err := build()
		if l != nil || !errors.Is(err, ratelimit.ErrInvalidStore) {
			t.Fatalf("limiter=%v err=%v", l, err)
		}
	}
	for _, resolution := range []time.Duration{0, -time.Second, time.Millisecond, 1500 * time.Millisecond} {
		if _, err := ratelimit.NewBuckets(memory.NewBucketStore(), resolution); !errors.Is(err, ratelimit.ErrInvalidResolution) {
			t.Errorf("resolution=%s: %v", resolution, err)
		}
	}
}

type spyStore struct {
	now        time.Time
	fail       string
	err        error
	calls      []string
	ctx        context.Context
	from, to   time.Time
	resolution time.Duration
}

func (s *spyStore) record(ctx context.Context, operation string) error {
	s.calls = append(s.calls, operation)
	s.ctx = ctx
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if s.fail == operation {
		return s.err
	}
	return nil
}

func (s *spyStore) Now(ctx context.Context) (time.Time, error) { return s.now, s.record(ctx, "now") }
func (s *spyStore) Append(ctx context.Context, _ string, at time.Time) error {
	s.to = at
	return s.record(ctx, "append")
}
func (s *spyStore) Count(ctx context.Context, _ string, from, to time.Time) (int64, error) {
	s.from, s.to = from, to
	return 41, s.record(ctx, "count")
}
func (s *spyStore) Prune(ctx context.Context, before time.Time) error {
	s.from = before
	return s.record(ctx, "prune")
}
func (s *spyStore) PruneKey(ctx context.Context, _ string, before time.Time) error {
	s.from = before
	return s.record(ctx, "prune_key")
}
func (s *spyStore) IncrementBucket(ctx context.Context, _ string, start time.Time, resolution time.Duration) error {
	s.to, s.resolution = start, resolution
	return s.record(ctx, "increment")
}
func (s *spyStore) SumBuckets(ctx context.Context, _ string, from, to time.Time, resolution time.Duration) (int64, error) {
	s.from, s.to, s.resolution = from, to, resolution
	return 41, s.record(ctx, "sum")
}
func (s *spyStore) PruneBuckets(ctx context.Context, before, current time.Time, resolution time.Duration) error {
	s.from, s.to, s.resolution = before, current, resolution
	return s.record(ctx, "prune_buckets")
}
func (s *spyStore) PruneKeyBuckets(ctx context.Context, _ string, before, current time.Time, resolution time.Duration) error {
	s.from, s.to, s.resolution = before, current, resolution
	return s.record(ctx, "prune_key_buckets")
}

func TestValidationDoesNotAccessStorage(t *testing.T) {
	key, _ := ratelimit.NewKey("test")
	s := &spyStore{}
	for _, construct := range []func() (ratelimit.RateLimit, error){
		func() (ratelimit.RateLimit, error) { return ratelimit.NewEventLog(s) },
		func() (ratelimit.RateLimit, error) { return ratelimit.NewBuckets(s, time.Second) },
	} {
		l, err := construct()
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		_, err = l.Get(ctx, ratelimit.Key{}, time.Second)
		for _, err := range []error{err, l.Hit(ctx, ratelimit.Key{}), l.CleanKey(ctx, ratelimit.Key{}, time.Second)} {
			if !errors.Is(err, ratelimit.ErrInvalidKey) {
				t.Errorf("error=%v", err)
			}
		}
		for _, window := range []time.Duration{0, -1} {
			_, err = l.Get(ctx, key, window)
			for _, err := range []error{err, l.Clean(ctx, window), l.CleanKey(ctx, key, window)} {
				if !errors.Is(err, ratelimit.ErrInvalidWindow) {
					t.Errorf("error=%v", err)
				}
			}
		}
	}
	if len(s.calls) != 0 {
		t.Fatalf("validation accessed storage: %v", s.calls)
	}
}

func TestOperationErrorsAndClock(t *testing.T) {
	key, _ := ratelimit.NewKey("test")
	now := time.Date(2026, 11, 8, 12, 0, 3, 123, time.FixedZone("offset", -10800))
	sentinel := errors.New("backend failure")
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "forwarded")
	for _, strategy := range []string{"events", "buckets"} {
		for _, operation := range []string{"get", "hit", "clean", "clean_key"} {
			t.Run(strategy+"/"+operation, func(t *testing.T) {
				for _, fail := range []string{"", "now", "store"} {
					s := &spyStore{now: now, err: sentinel}
					var l ratelimit.RateLimit
					var err error
					var backend string
					if strategy == "events" {
						l, err = ratelimit.NewEventLog(s)
						backend = map[string]string{"get": "count", "hit": "append", "clean": "prune", "clean_key": "prune_key"}[operation]
					} else {
						l, err = ratelimit.NewBuckets(s, 7*time.Second)
						backend = map[string]string{"get": "sum", "hit": "increment", "clean": "prune_buckets", "clean_key": "prune_key_buckets"}[operation]
					}
					if err != nil {
						t.Fatal(err)
					}
					if fail == "store" {
						s.fail = backend
					} else {
						s.fail = fail
					}
					switch operation {
					case "get":
						var n int64
						n, err = l.Get(ctx, key, time.Minute)
						if fail == "" && n != 41 {
							t.Fatalf("count=%d", n)
						}
					case "hit":
						err = l.Hit(ctx, key)
					case "clean":
						err = l.Clean(ctx, time.Minute)
					case "clean_key":
						err = l.CleanKey(ctx, key, time.Minute)
					}
					wantCalls := []string{"now", backend}
					if fail == "now" {
						wantCalls = []string{"now"}
					}
					if !reflect.DeepEqual(s.calls, wantCalls) || s.ctx != ctx {
						t.Fatalf("calls=%v ctx=%v", s.calls, s.ctx)
					}
					if fail != "" {
						if !errors.Is(err, sentinel) {
							t.Fatalf("lost error identity: %v", err)
						}
						continue
					}
					if err != nil {
						t.Fatal(err)
					}
					if operation != "hit" && !s.from.Equal(now.Add(-time.Minute)) {
						t.Fatalf("cutoff=%s", s.from)
					}
					if operation == "get" || operation == "hit" || strategy == "buckets" {
						want := now.UTC()
						if strategy == "buckets" && operation != "get" {
							want = want.Truncate(7 * time.Second)
						}
						if s.to != want {
							t.Fatalf("time=%s want=%s", s.to, want)
						}
					}
					if strategy == "buckets" && s.resolution != 7*time.Second {
						t.Fatal("lost resolution")
					}
				}
			})
		}
	}
}
