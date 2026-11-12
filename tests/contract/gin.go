package contract

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ratelimit "github.com/danielbastos/gin-rate-limit"
	"github.com/danielbastos/gin-rate-limit/gingatelimit"
	"github.com/danielbastos/gin-rate-limit/storage"
	"github.com/gin-gonic/gin"
)

// RunGinMiddleware checks HTTP admission and counter sharing using real stores.
// Factories follow the same isolation and cleanup contract as RunEventStore.
func RunGinMiddleware(t *testing.T, events func(*testing.T) storage.EventStore, buckets func(*testing.T) storage.BucketStore) {
	t.Helper()
	for _, strategy := range []string{"events", "buckets"} {
		t.Run(strategy, func(t *testing.T) {
			var build func() (ratelimit.RateLimit, error)
			if strategy == "events" {
				s := events(t)
				build = func() (ratelimit.RateLimit, error) { return ratelimit.NewEventLog(s) }
			} else {
				s := buckets(t)
				build = func() (ratelimit.RateLimit, error) { return ratelimit.NewBuckets(s, time.Second) }
			}
			routers := make([]*gin.Engine, 2)
			var observer ratelimit.RateLimit
			for i := range routers {
				l, err := build()
				requireNoError(t, err)
				observer = l
				m, err := gingatelimit.New(l, gingatelimit.Config{
					Limit: 2, Window: time.Hour,
					KeyFunc: func(c *gin.Context) (ratelimit.Key, error) {
						return ratelimit.NewKey("gin:"+c.FullPath(), "user", c.GetHeader("X-Test-User"))
					},
				})
				requireNoError(t, err)
				r := gin.New()
				r.Use(m)
				for _, path := range []string{"/limited/:id", "/other"} {
					r.GET(path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
				}
				routers[i] = r
			}
			for _, tc := range []struct {
				router int
				path   string
				user   string
				want   int
			}{
				{0, "/limited/1", "alice", 204},
				{1, "/limited/2", "alice", 204},
				{0, "/limited/3", "alice", 429},
				{1, "/limited/4", "alice", 429},
				{0, "/limited/1", "bob", 204},
				{1, "/other", "alice", 204},
			} {
				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, tc.path, nil)
				req.Header.Set("X-Test-User", tc.user)
				routers[tc.router].ServeHTTP(w, req)
				if w.Code != tc.want {
					t.Fatalf("router=%d path=%s user=%s status=%d want=%d body=%s", tc.router, tc.path, tc.user, w.Code, tc.want, w.Body)
				}
			}
			key, err := ratelimit.NewKey("gin:/limited/:id", "user", "alice")
			requireNoError(t, err)
			n, err := observer.Get(context.Background(), key, time.Hour)
			requireCount(t, n, err, 2)
		})
	}
}
