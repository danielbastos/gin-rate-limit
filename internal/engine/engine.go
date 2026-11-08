package engine

import (
	"context"
	"time"
)

// Engine uses already validated opaque string keys, avoiding imports of the
// public facade from strategies and storage.
type Engine interface {
	Get(context.Context, string, time.Duration) (int64, error)
	Hit(context.Context, string) error
	Clean(context.Context, time.Duration) error
	CleanKey(context.Context, string, time.Duration) error
}
