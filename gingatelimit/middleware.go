// Package gingatelimit adapts ratelimit counters to Gin middleware.
// Checking and recording are separate operations, so concurrent requests may
// exceed the configured limit, including when they share a remote store.
package gingatelimit

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	ratelimit "github.com/danielbastos/gin-rate-limit"
	"github.com/danielbastos/gin-rate-limit/internal/validation"
	"github.com/gin-gonic/gin"
)

// ErrInvalidConfig identifies invalid middleware configuration.
var ErrInvalidConfig = errors.New("gingatelimit: invalid config")

// Config defines one limiting policy. Use distinct key namespaces for policies
// that must not share counters. Callbacks may run concurrently across requests.
type Config struct {
	// Limit and Window must be positive.
	Limit  int64
	Window time.Duration
	// KeyFunc is required and must return a valid key. It should not consume the
	// request body or write a response. Authentication must run first if needed.
	KeyFunc func(*gin.Context) (ratelimit.Key, error)
	// Skip bypasses both key generation and storage when it returns true.
	Skip func(*gin.Context) bool
	// OnLimit replaces the default 429 JSON response. The middleware aborts the
	// chain before invoking it; the callback only needs to write its response.
	OnLimit gin.HandlerFunc
	// OnError replaces the default error response (500 for key errors, 503 for
	// counter errors). The chain is already aborted. Errors retain their identity
	// through wrapping and are also attached to c.Errors, including in FailOpen.
	// OnError is not called when FailOpen allows a request to continue.
	OnError func(*gin.Context, error)
	// FailOpen allows requests on Get or Hit failures. Key errors and failures
	// when the request context is already canceled always stop the chain.
	// A failed Hit may already have been persisted by a remote store.
	FailOpen bool
}

// New validates configuration and returns a middleware that counts admitted
// requests before executing downstream handlers, regardless of their outcome.
// Rejected requests do not record a hit. The caller owns cleanup and connections.
// No atomic authorization, automatic cleanup, or retry/reset headers are provided.
func New(limiter ratelimit.RateLimit, cfg Config) (gin.HandlerFunc, error) {
	if validation.IsNil(limiter) {
		return nil, fmt.Errorf("%w: limiter is required", ErrInvalidConfig)
	}
	if cfg.Limit <= 0 {
		return nil, fmt.Errorf("%w: limit must be positive", ErrInvalidConfig)
	}
	if cfg.Window <= 0 {
		return nil, fmt.Errorf("%w: window must be positive", ErrInvalidConfig)
	}
	if cfg.KeyFunc == nil {
		return nil, fmt.Errorf("%w: key function is required", ErrInvalidConfig)
	}

	fail := func(c *gin.Context, operation string, err error, status int, allow bool) {
		err = fmt.Errorf("gingatelimit: %s: %w", operation, err)
		_ = c.Error(err)
		if allow && c.Request.Context().Err() == nil {
			c.Next()
			return
		}
		c.Abort()
		if cfg.OnError != nil {
			cfg.OnError(c, err)
			return
		}
		message := "rate limiter unavailable"
		if status == http.StatusInternalServerError {
			message = "rate limiter key error"
		}
		c.JSON(status, gin.H{"error": message})
	}

	return func(c *gin.Context) {
		if cfg.Skip != nil && cfg.Skip(c) {
			c.Next()
			return
		}
		key, err := cfg.KeyFunc(c)
		if err == nil && key.String() == "" {
			err = ratelimit.ErrInvalidKey
		}
		if err != nil {
			fail(c, "key", err, http.StatusInternalServerError, false)
			return
		}
		ctx := c.Request.Context()
		count, err := limiter.Get(ctx, key, cfg.Window)
		if err != nil {
			fail(c, "get", err, http.StatusServiceUnavailable, cfg.FailOpen)
			return
		}
		if count >= cfg.Limit {
			c.Abort()
			if cfg.OnLimit != nil {
				cfg.OnLimit(c)
			} else {
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			}
			return
		}
		if err := limiter.Hit(ctx, key); err != nil {
			fail(c, "hit", err, http.StatusServiceUnavailable, cfg.FailOpen)
			return
		}
		c.Next()
	}, nil
}
