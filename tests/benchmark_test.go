package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	ratelimit "github.com/danielbastos/gin-rate-limit"
	"github.com/danielbastos/gin-rate-limit/storage"
	"github.com/danielbastos/gin-rate-limit/storage/memory"
)

func BenchmarkMemory(b *testing.B) {
	ctx := context.Background()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	for _, kind := range []string{"events", "buckets"} {
		b.Run(kind, func(b *testing.B) {
			key, err := ratelimit.NewKey("benchmark")
			if err != nil {
				b.Fatal(err)
			}
			fresh := func() ratelimit.RateLimit {
				var l ratelimit.RateLimit
				if kind == "events" {
					l, err = ratelimit.NewEventLog(memory.NewEventStore())
				} else {
					l, err = ratelimit.NewBuckets(memory.NewBucketStore(), time.Second)
				}
				if err != nil {
					b.Fatal(err)
				}
				return l
			}
			b.Run("Hit", func(b *testing.B) {
				l := fresh()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if i > 0 && i%4096 == 0 {
						b.StopTimer()
						l = fresh()
						b.StartTimer()
					}
					if err := l.Hit(ctx, key); err != nil {
						b.Fatal(err)
					}
				}
			})
			for _, keys := range []int{1, 100} {
				for _, history := range []int{100, 1000} {
					b.Run(fmt.Sprintf("Get/keys=%d/history=%d", keys, history), func(b *testing.B) {
						var events storage.EventStore = memory.NewEventStore()
						var buckets storage.BucketStore = memory.NewBucketStore()
						for k := 0; k < keys; k++ {
							for i := 0; i < history; i++ {
								at := now.Add(-time.Duration(i) * time.Second)
								if kind == "events" {
									err = events.Append(ctx, fmt.Sprint(k), at)
								} else {
									err = buckets.IncrementBucket(ctx, fmt.Sprint(k), at, time.Second)
								}
								if err != nil {
									b.Fatal(err)
								}
							}
						}
						b.ReportAllocs()
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							if kind == "events" {
								_, err = events.Count(ctx, "0", now.Add(-time.Minute), now)
							} else {
								_, err = buckets.SumBuckets(ctx, "0", now.Add(-time.Minute), now, time.Second)
							}
							if err != nil {
								b.Fatal(err)
							}
						}
					})
				}
			}
			for _, operation := range []string{"Clean", "CleanKey"} {
				b.Run(operation, func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						b.StopTimer()
						var events storage.EventStore = memory.NewEventStore()
						var buckets storage.BucketStore = memory.NewBucketStore()
						for k := 0; k < 8; k++ {
							for j := 0; j < 32; j++ {
								at := now.Add(-time.Duration(j) * time.Second)
								if kind == "events" {
									err = events.Append(ctx, fmt.Sprint(k), at)
								} else {
									err = buckets.IncrementBucket(ctx, fmt.Sprint(k), at, time.Second)
								}
								if err != nil {
									b.Fatal(err)
								}
							}
						}
						b.StartTimer()
						if kind == "events" {
							if operation == "Clean" {
								err = events.Prune(ctx, now.Add(-10*time.Second))
							} else {
								err = events.PruneKey(ctx, "0", now.Add(-10*time.Second))
							}
						} else {
							if operation == "Clean" {
								err = buckets.PruneBuckets(ctx, now.Add(-10*time.Second), now, time.Second)
							} else {
								err = buckets.PruneKeyBuckets(ctx, "0", now.Add(-10*time.Second), now, time.Second)
							}
						}
						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
