package redis_test

import (
	"errors"
	"testing"

	store "github.com/danielbastos/gin-rate-limit/storage/redis"
	"github.com/redis/go-redis/v9"
)

func TestInvalidConfiguration(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = client.Close() })
	for _, tc := range []struct {
		client *redis.Client
		prefix string
	}{{nil, "test"}, {client, ""}} {
		if _, err := store.NewEventStore(tc.client, tc.prefix); !errors.Is(err, store.ErrInvalidConfig) {
			t.Fatalf("event store error=%v", err)
		}
		if _, err := store.NewBucketStore(tc.client, tc.prefix); !errors.Is(err, store.ErrInvalidConfig) {
			t.Fatalf("bucket store error=%v", err)
		}
	}
}
