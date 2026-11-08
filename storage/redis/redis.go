// Package redis implements storage using a single Redis server. Clients and
// their connections belong to the caller. No TTL is configured.
package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/danielbastos/gin-rate-limit/internal/instant"
	"github.com/danielbastos/gin-rate-limit/storage"
	redisclient "github.com/redis/go-redis/v9"
)

var ErrInvalidConfig = errors.New("ratelimit/redis: client and nonempty prefix required")

type store struct {
	client *redisclient.Client
	prefix string
}

type EventStore struct{ store }
type BucketStore struct{ store }

var _ storage.EventStore = (*EventStore)(nil)
var _ storage.BucketStore = (*BucketStore)(nil)

// NewEventStore creates an adapter without contacting Redis. Prefix identifies
// a private storage space and must not overlap another application's prefix.
func NewEventStore(client *redisclient.Client, prefix string) (*EventStore, error) {
	if client == nil || prefix == "" {
		return nil, ErrInvalidConfig
	}
	return &EventStore{store{client, prefix}}, nil
}

func NewBucketStore(client *redisclient.Client, prefix string) (*BucketStore, error) {
	if client == nil || prefix == "" {
		return nil, ErrInvalidConfig
	}
	return &BucketStore{store{client, prefix}}, nil
}

func (s store) Now(ctx context.Context) (time.Time, error) {
	now, err := s.client.Time(ctx).Result()
	return now.UTC(), err
}

func (s store) eventKeys(key string) (string, string) {
	return s.prefix + ":events:data:" + hex.EncodeToString([]byte(key)), s.prefix + ":events:index"
}

func (s store) bucketKeys(key string, resolution time.Duration) (string, string, string) {
	base := s.prefix + ":buckets:" + strconv.FormatInt(int64(resolution), 10)
	data := base + ":data:" + hex.EncodeToString([]byte(key))
	return data, data + ":counts", base + ":index"
}

var appendEvent = redisclient.NewScript(`
redis.call('ZADD', KEYS[1], 0, ARGV[1])
redis.call('SADD', KEYS[2], KEYS[1])
return 1
`)

func (s *EventStore) Append(ctx context.Context, key string, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return fmt.Errorf("redis event id: %w", err)
	}
	data, index := s.eventKeys(key)
	member := instant.Lex(at) + ":" + hex.EncodeToString(id[:])
	return appendEvent.Run(ctx, s.client, []string{data, index}, member).Err()
}

func (s *EventStore) Count(ctx context.Context, key string, from, to time.Time) (int64, error) {
	data, _ := s.eventKeys(key)
	// '~' sorts after the separator and every hexadecimal event ID.
	return s.client.ZLexCount(ctx, data, "("+instant.Lex(from)+"~", "["+instant.Lex(to)+"~").Result()
}

var pruneEvents = redisclient.NewScript(`
local members = redis.call('ZRANGEBYLEX', KEYS[1], '-', ARGV[1], 'LIMIT', 0, 512)
for _, member in ipairs(members) do redis.call('ZREM', KEYS[1], member) end
if redis.call('ZCARD', KEYS[1]) == 0 then redis.call('SREM', KEYS[2], KEYS[1]) end
return #members
`)

func (s *EventStore) prune(ctx context.Context, data, index string, before time.Time) error {
	for {
		n, err := pruneEvents.Run(ctx, s.client, []string{data, index}, "("+instant.Lex(before)).Int64()
		if err != nil {
			return err
		}
		if n < 512 {
			return nil
		}
	}
}

func (s *EventStore) Prune(ctx context.Context, before time.Time) error {
	_, index := s.eventKeys("")
	return s.eachSeries(ctx, index, func(data string) error { return s.prune(ctx, data, index, before) })
}

func (s *EventStore) PruneKey(ctx context.Context, key string, before time.Time) error {
	data, index := s.eventKeys(key)
	return s.prune(ctx, data, index, before)
}

// SSCAN is intentionally scoped to the private index. No keyspace-wide KEYS,
// FLUSHDB or automatic expiry is used. Concurrently created series may be
// observed in this cleanup or the next one.
func (s store) eachSeries(ctx context.Context, index string, visit func(string) error) error {
	var cursor uint64
	for {
		keys, next, err := s.client.SScan(ctx, index, cursor, "*", 128).Result()
		if err != nil {
			return err
		}
		for _, key := range keys {
			if err := visit(key); err != nil {
				return err
			}
		}
		if next == 0 {
			return nil
		}
		cursor = next
	}
}

var incrementBucket = redisclient.NewScript(`
redis.call('HINCRBY', KEYS[2], ARGV[1], 1)
redis.call('ZADD', KEYS[1], 0, ARGV[1])
redis.call('SADD', KEYS[3], KEYS[1])
return 1
`)

func (s *BucketStore) IncrementBucket(ctx context.Context, key string, start time.Time, resolution time.Duration) error {
	data, counts, index := s.bucketKeys(key, resolution)
	return incrementBucket.Run(ctx, s.client, []string{data, counts, index}, instant.Lex(start)).Err()
}

var readBuckets = redisclient.NewScript(`
local members = redis.call('ZRANGEBYLEX', KEYS[1], ARGV[1], ARGV[2])
local result = {}
for _, member in ipairs(members) do
  table.insert(result, redis.call('HGET', KEYS[2], member))
end
return result
`)

func (s *BucketStore) SumBuckets(ctx context.Context, key string, from, to time.Time, resolution time.Duration) (int64, error) {
	data, counts, _ := s.bucketKeys(key, resolution)
	values, err := readBuckets.Run(ctx, s.client, []string{data, counts}, "("+instant.Lex(from.Add(-resolution)), "["+instant.Lex(to)).StringSlice()
	if err != nil {
		return 0, err
	}
	var total int64
	for _, value := range values {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, err
		}
		if n > math.MaxInt64-total {
			return 0, errors.New("ratelimit/redis: count overflow")
		}
		total += n
	}
	return total, nil
}

var pruneBucket = redisclient.NewScript(`
local members = redis.call('ZRANGEBYLEX', KEYS[1], '-', ARGV[1], 'LIMIT', 0, 512)
for _, member in ipairs(members) do
  redis.call('ZREM', KEYS[1], member)
  redis.call('HDEL', KEYS[2], member)
end
if redis.call('ZCARD', KEYS[1]) == 0 then
  redis.call('DEL', KEYS[1], KEYS[2])
  redis.call('SREM', KEYS[3], KEYS[1])
end
return #members
`)

func (s *BucketStore) prune(ctx context.Context, data, counts, index string, before, current time.Time) error {
	if current.Before(before) {
		before = current
	}
	for {
		n, err := pruneBucket.Run(ctx, s.client, []string{data, counts, index}, "("+instant.Lex(before)).Int64()
		if err != nil {
			return err
		}
		if n < 512 {
			return nil
		}
	}
}

func (s *BucketStore) PruneBuckets(ctx context.Context, before, current time.Time, resolution time.Duration) error {
	_, _, index := s.bucketKeys("", resolution)
	return s.eachSeries(ctx, index, func(data string) error { return s.prune(ctx, data, data+":counts", index, before, current) })
}

func (s *BucketStore) PruneKeyBuckets(ctx context.Context, key string, before, current time.Time, resolution time.Duration) error {
	data, counts, index := s.bucketKeys(key, resolution)
	return s.prune(ctx, data, counts, index, before, current)
}
