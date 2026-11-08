package ratelimit

import "errors"

var (
	ErrInvalidKey        = errors.New("ratelimit: invalid key")
	ErrInvalidWindow     = errors.New("ratelimit: invalid window")
	ErrInvalidResolution = errors.New("ratelimit: invalid resolution")
	ErrInvalidStore      = errors.New("ratelimit: invalid store")
)
