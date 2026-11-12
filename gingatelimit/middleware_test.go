package gingatelimit_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	ratelimit "github.com/danielbastos/gin-rate-limit"
	"github.com/danielbastos/gin-rate-limit/gingatelimit"
	"github.com/danielbastos/gin-rate-limit/storage/memory"
	"github.com/gin-gonic/gin"
)

type counter struct {
	ratelimit.RateLimit
	get func(context.Context, ratelimit.Key, time.Duration) (int64, error)
	hit func(context.Context, ratelimit.Key) error
}

func (s *counter) Get(ctx context.Context, key ratelimit.Key, window time.Duration) (int64, error) {
	return s.get(ctx, key, window)
}

func (s *counter) Hit(ctx context.Context, key ratelimit.Key) error { return s.hit(ctx, key) }

func fixedKey(*gin.Context) (ratelimit.Key, error) { return ratelimit.NewKey("test") }

func config() gingatelimit.Config {
	return gingatelimit.Config{Limit: 2, Window: time.Minute, KeyFunc: fixedKey}
}

func middleware(t *testing.T, l ratelimit.RateLimit, cfg gingatelimit.Config) gin.HandlerFunc {
	t.Helper()
	m, err := gingatelimit.New(l, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func request(r http.Handler, path, user string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Test-User", user)
	r.ServeHTTP(w, req)
	return w
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	var typedNil *counter
	for _, tc := range []struct {
		name string
		l    ratelimit.RateLimit
		edit func(*gingatelimit.Config)
	}{
		{name: "nil"},
		{name: "typed nil", l: typedNil},
		{name: "zero limit", l: &counter{}, edit: func(c *gingatelimit.Config) { c.Limit = 0 }},
		{name: "negative limit", l: &counter{}, edit: func(c *gingatelimit.Config) { c.Limit = -1 }},
		{name: "zero window", l: &counter{}, edit: func(c *gingatelimit.Config) { c.Window = 0 }},
		{name: "negative window", l: &counter{}, edit: func(c *gingatelimit.Config) { c.Window = -1 }},
		{name: "missing key function", l: &counter{}, edit: func(c *gingatelimit.Config) { c.KeyFunc = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config()
			if tc.edit != nil {
				tc.edit(&cfg)
			}
			m, err := gingatelimit.New(tc.l, cfg)
			if m != nil || !errors.Is(err, gingatelimit.ErrInvalidConfig) {
				t.Fatalf("middleware=%v error=%v", m, err)
			}
		})
	}
}

type eventClock struct {
	*memory.EventStore
	now time.Time
}

func (s *eventClock) Now(ctx context.Context) (time.Time, error) { return s.now, ctx.Err() }

type bucketClock struct {
	*memory.BucketStore
	now time.Time
}

func (s *bucketClock) Now(ctx context.Context) (time.Time, error) { return s.now, ctx.Err() }

func TestCountsAdmissionsAndExpiresWindow(t *testing.T) {
	for _, strategy := range []string{"events", "buckets"} {
		t.Run(strategy, func(t *testing.T) {
			now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
			events := &eventClock{memory.NewEventStore(), now}
			buckets := &bucketClock{memory.NewBucketStore(), now}
			var l ratelimit.RateLimit
			var err error
			if strategy == "events" {
				l, err = ratelimit.NewEventLog(events)
			} else {
				l, err = ratelimit.NewBuckets(buckets, time.Second)
			}
			if err != nil {
				t.Fatal(err)
			}
			cfg := config()
			cfg.KeyFunc = func(c *gin.Context) (ratelimit.Key, error) {
				return ratelimit.NewKey("resource", "user", c.GetString("user"))
			}
			r := gin.New()
			group := r.Group("/api", func(c *gin.Context) {
				c.Set("user", c.GetHeader("X-Test-User"))
				c.Next()
			})
			group.Use(middleware(t, l, cfg))
			var executions int
			group.GET("/resource", func(c *gin.Context) {
				executions++
				c.Status(http.StatusInternalServerError)
			})
			for i, want := range []int{500, 500, 429, 429} {
				w := request(r, "/api/resource", "alice")
				if w.Code != want {
					t.Fatalf("request %d: status=%d want=%d", i, w.Code, want)
				}
				if want == 429 && (w.Body.String() != `{"error":"rate limit exceeded"}` || w.Header().Get("Content-Type") != "application/json; charset=utf-8") {
					t.Fatalf("response=%s headers=%v", w.Body, w.Header())
				}
			}
			key, _ := ratelimit.NewKey("resource", "user", "alice")
			if n, err := l.Get(context.Background(), key, cfg.Window); err != nil || n != 2 || executions != 2 {
				t.Fatalf("count=%d error=%v executions=%d", n, err, executions)
			}
			if w := request(r, "/api/resource", "bob"); w.Code != 500 {
				t.Fatalf("other user status=%d", w.Code)
			}
			// Buckets retain the whole intersecting bucket, unlike exact event logs.
			events.now = now.Add(cfg.Window)
			buckets.now = now.Add(cfg.Window + time.Second)
			if w := request(r, "/api/resource", "alice"); w.Code != 500 || executions != 4 {
				t.Fatalf("expired status=%d executions=%d", w.Code, executions)
			}
		})
	}
}

func TestSkipBypassesKeyAndStorage(t *testing.T) {
	cfg := config()
	cfg.Skip = func(c *gin.Context) bool { return c.FullPath() == "/health" }
	cfg.KeyFunc = func(*gin.Context) (ratelimit.Key, error) {
		t.Fatal("skipped request generated a key")
		return ratelimit.Key{}, nil
	}
	r := gin.New()
	r.Use(middleware(t, &counter{}, cfg)) // Any storage call would panic.
	r.GET("/health", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	if w := request(r, "/health", ""); w.Code != 204 {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestKeyErrorsNeverFailOpen(t *testing.T) {
	sentinel := errors.New("private identity error")
	for _, wantErr := range []error{sentinel, ratelimit.ErrInvalidKey} {
		t.Run(wantErr.Error(), func(t *testing.T) {
			cfg := config()
			cfg.FailOpen = true
			cfg.KeyFunc = func(*gin.Context) (ratelimit.Key, error) {
				if wantErr == sentinel {
					return ratelimit.Key{}, sentinel
				}
				return ratelimit.Key{}, nil
			}
			r := gin.New()
			var observed error
			r.Use(func(c *gin.Context) { c.Next(); observed = c.Errors.Last().Err })
			r.GET("/", middleware(t, &counter{}, cfg), func(*gin.Context) { t.Fatal("handler ran") })
			w := request(r, "/", "")
			if w.Code != 500 || !errors.Is(observed, wantErr) || w.Body.String() != `{"error":"rate limiter key error"}` {
				t.Fatalf("status=%d error=%v body=%s", w.Code, observed, w.Body)
			}
		})
	}
}

func TestStorageFailuresAndRequestContext(t *testing.T) {
	sentinel := errors.New("private backend error")
	type contextKey struct{}
	for _, operation := range []string{"get", "hit"} {
		for _, failOpen := range []bool{false, true} {
			for _, canceled := range []bool{false, true} {
				name := operation
				if failOpen {
					name += "/open"
				} else {
					name += "/closed"
				}
				if canceled {
					name += "/canceled"
				}
				t.Run(name, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "request"))
					defer cancel()
					if canceled {
						cancel()
					}
					key, _ := fixedKey(nil)
					getCalls, hitCalls := 0, 0
					l := &counter{
						get: func(got context.Context, gotKey ratelimit.Key, window time.Duration) (int64, error) {
							getCalls++
							if got != ctx || gotKey != key || window != time.Minute {
								t.Fatal("get lost request arguments")
							}
							if operation == "get" {
								return 0, sentinel
							}
							return 0, nil
						},
						hit: func(got context.Context, gotKey ratelimit.Key) error {
							hitCalls++
							if got != ctx || gotKey != key {
								t.Fatal("hit lost request arguments")
							}
							return sentinel
						},
					}
					cfg := config()
					cfg.FailOpen = failOpen
					r := gin.New()
					var observed error
					r.Use(func(c *gin.Context) { c.Next(); observed = c.Errors.Last().Err })
					executions := 0
					r.GET("/", middleware(t, l, cfg), func(c *gin.Context) { executions++; c.Status(204) })
					w := httptest.NewRecorder()
					r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
					wantStatus, wantExecutions := 503, 0
					if failOpen && !canceled {
						wantStatus, wantExecutions = 204, 1
					}
					wantHits := 0
					if operation == "hit" {
						wantHits = 1
					}
					if w.Code != wantStatus || executions != wantExecutions || !errors.Is(observed, sentinel) || getCalls != 1 || hitCalls != wantHits {
						t.Fatalf("status=%d executions=%d error=%v get=%d hit=%d", w.Code, executions, observed, getCalls, hitCalls)
					}
					if wantStatus == 503 && w.Body.String() != `{"error":"rate limiter unavailable"}` {
						t.Fatalf("body=%s", w.Body)
					}
				})
			}
		}
	}
}

func TestCanceledRequestsPreserveContextErrors(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "deadline"}[expired], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			wantErr := context.Canceled
			if expired {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				wantErr = context.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			l, err := ratelimit.NewEventLog(memory.NewEventStore())
			if err != nil {
				t.Fatal(err)
			}
			cfg := config()
			cfg.FailOpen = true
			var observed error
			cfg.OnError = func(c *gin.Context, err error) { observed = err; c.Status(503) }
			r := gin.New()
			r.GET("/", middleware(t, l, cfg), func(*gin.Context) { t.Fatal("canceled request reached handler") })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
			if w.Code != 503 || !errors.Is(observed, wantErr) {
				t.Fatalf("status=%d error=%v", w.Code, observed)
			}
		})
	}
}

func TestCustomResponsesAbortDownstreamHandlers(t *testing.T) {
	sentinel := errors.New("backend failure")
	for _, scenario := range []string{"limit", "error", "fail open"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := config()
			cfg.FailOpen = scenario == "fail open"
			callbacks := 0
			cfg.OnLimit = func(c *gin.Context) { callbacks++; c.String(429, "custom limit") }
			cfg.OnError = func(c *gin.Context, err error) {
				callbacks++
				if !errors.Is(err, sentinel) {
					t.Fatalf("error=%v", err)
				}
				c.String(502, "custom failure")
			}
			l := &counter{get: func(context.Context, ratelimit.Key, time.Duration) (int64, error) {
				if scenario == "limit" {
					return 2, nil
				}
				return 0, sentinel
			}}
			r := gin.New()
			executions := 0
			r.GET("/", middleware(t, l, cfg), func(c *gin.Context) { executions++; c.Status(204) })
			w := request(r, "/", "")
			if scenario == "fail open" {
				if w.Code != 204 || callbacks != 0 || executions != 1 {
					t.Fatalf("status=%d callbacks=%d executions=%d", w.Code, callbacks, executions)
				}
			} else {
				wantStatus, wantBody := 429, "custom limit"
				if scenario == "error" {
					wantStatus, wantBody = 502, "custom failure"
				}
				if w.Code != wantStatus || w.Body.String() != wantBody || callbacks != 1 || executions != 0 {
					t.Fatalf("status=%d body=%s callbacks=%d executions=%d", w.Code, w.Body, callbacks, executions)
				}
			}
		})
	}
}

func TestConcurrentRequestsCanExceedLimit(t *testing.T) {
	// Force two independent middleware instances to read the same count before
	// either records a hit. This documents the non-atomic authorization contract.
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	var hits int
	l := &counter{
		get: func(context.Context, ratelimit.Key, time.Duration) (int64, error) {
			arrived <- struct{}{}
			<-release
			return 0, nil
		},
		hit: func(context.Context, ratelimit.Key) error { mu.Lock(); defer mu.Unlock(); hits++; return nil },
	}
	cfg := config()
	cfg.Limit = 1
	routers := []*gin.Engine{gin.New(), gin.New()}
	for _, r := range routers {
		r.GET("/", middleware(t, l, cfg), func(c *gin.Context) { c.Status(204) })
	}
	var requests sync.WaitGroup
	for _, r := range routers {
		requests.Go(func() {
			if w := request(r, "/", ""); w.Code != 204 {
				t.Errorf("status=%d", w.Code)
			}
		})
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-timer.C:
			close(release)
			requests.Wait()
			t.Fatal("concurrent requests did not reach the counter")
		}
	}
	close(release)
	requests.Wait()
	if hits != 2 {
		t.Fatalf("hits=%d", hits)
	}
}
