package ratelimit

import (
	"time"

	"github.com/danielbastos/gin-rate-limit/internal/buckets"
	"github.com/danielbastos/gin-rate-limit/internal/validation"
	"github.com/danielbastos/gin-rate-limit/storage"
)

// NewBuckets constructs an approximate counter. Resolution must be a positive
// whole number of seconds. Buckets align to Go's zero time using Time.Truncate.
func NewBuckets(store storage.BucketStore, resolution time.Duration) (RateLimit, error) {
	if validation.IsNil(store) {
		return nil, ErrInvalidStore
	}
	if resolution <= 0 || resolution%time.Second != 0 {
		return nil, ErrInvalidResolution
	}
	return &limiter{engine: buckets.New(store, resolution)}, nil
}
