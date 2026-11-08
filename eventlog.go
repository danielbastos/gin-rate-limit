package ratelimit

import (
	"github.com/danielbastos/gin-rate-limit/internal/eventlog"
	"github.com/danielbastos/gin-rate-limit/internal/validation"
	"github.com/danielbastos/gin-rate-limit/storage"
)

// NewEventLog constructs an exact event counter using the store's clock.
// The caller owns the store and its connections.
func NewEventLog(store storage.EventStore) (RateLimit, error) {
	if validation.IsNil(store) {
		return nil, ErrInvalidStore
	}
	return &limiter{engine: eventlog.New(store)}, nil
}
