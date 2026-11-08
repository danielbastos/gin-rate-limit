// Package ratelimit counts occurrences over moving time windows.
// Authorization policy and scheduling of cleanup belong to the caller.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/danielbastos/gin-rate-limit/internal/engine"
)

// RateLimit provides counting and explicit cleanup. Get and Hit are separate
// operations and do not provide an atomic check-and-increment.
type RateLimit interface {
	Get(ctx context.Context, key Key, window time.Duration) (int64, error)
	Hit(ctx context.Context, key Key) error
	Clean(ctx context.Context, window time.Duration) error
	CleanKey(ctx context.Context, key Key, window time.Duration) error
}

type limiter struct{ engine engine.Engine }

func (l *limiter) Get(ctx context.Context, key Key, window time.Duration) (int64, error) {
	if err := validate(key, window); err != nil {
		return 0, err
	}
	n, err := l.engine.Get(ctx, key.String(), window)
	return n, operationError("get", err)
}

func (l *limiter) Hit(ctx context.Context, key Key) error {
	if key.hash == "" {
		return ErrInvalidKey
	}
	return operationError("hit", l.engine.Hit(ctx, key.String()))
}

func (l *limiter) Clean(ctx context.Context, window time.Duration) error {
	if window <= 0 {
		return ErrInvalidWindow
	}
	return operationError("clean", l.engine.Clean(ctx, window))
}

func (l *limiter) CleanKey(ctx context.Context, key Key, window time.Duration) error {
	if err := validate(key, window); err != nil {
		return err
	}
	return operationError("clean key", l.engine.CleanKey(ctx, key.String(), window))
}

func validate(key Key, window time.Duration) error {
	if key.hash == "" {
		return ErrInvalidKey
	}
	if window <= 0 {
		return ErrInvalidWindow
	}
	return nil
}

func operationError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("ratelimit: %s: %w", operation, err)
}
