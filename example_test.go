package ratelimit_test

import (
	"context"
	"fmt"
	"time"

	ratelimit "github.com/danielbastos/gin-rate-limit"
	"github.com/danielbastos/gin-rate-limit/storage/memory"
)

type exampleClock struct{ *memory.EventStore }

func (exampleClock) Now(context.Context) (time.Time, error) {
	return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC), nil
}

func ExampleNewEventLog() {
	key, err := ratelimit.NewKey("login", "ip", "192.0.2.1")
	if err != nil {
		panic(err)
	}
	limiter, err := ratelimit.NewEventLog(exampleClock{memory.NewEventStore()})
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	if err := limiter.Hit(ctx, key); err != nil {
		panic(err)
	}
	count, err := limiter.Get(ctx, key, time.Minute)
	if err != nil {
		panic(err)
	}
	fmt.Println(count)
	// Output: 1
}
