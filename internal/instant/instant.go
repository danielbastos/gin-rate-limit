// Package instant encodes instants without converting nanoseconds to float64.
package instant

import (
	"fmt"
	"math/big"
	"time"
)

// Lex returns a fixed-width, order-preserving encoding: sign-biased 64-bit Unix
// seconds followed by 32-bit nanoseconds, both hexadecimal. Equal instants in
// different locations have the same encoding.
func Lex(t time.Time) string {
	return fmt.Sprintf("%016x%08x", uint64(t.Unix())^(uint64(1)<<63), t.Nanosecond())
}

// Decimal returns exact Unix nanoseconds for PostgreSQL numeric(30,0), including
// dates outside the narrower range of Time.UnixNano.
func Decimal(t time.Time) string {
	n := new(big.Int).SetInt64(t.Unix())
	n.Mul(n, big.NewInt(1_000_000_000))
	n.Add(n, big.NewInt(int64(t.Nanosecond())))
	return n.String()
}
