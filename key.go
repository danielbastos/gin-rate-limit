package ratelimit

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"
)

// Key is an immutable, canonical identifier. Its zero value is invalid.
type Key struct{ hash string }

// String returns the versioned digest, without the original components.
func (k Key) String() string { return k.hash }

// NewKey builds a key from a namespace and zero or more name/value pairs.
// Names must be unique. Names, values and namespace must not be empty.
// Pair order does not affect the result. Inputs are not normalized or modified.
func NewKey(namespace string, components ...string) (Key, error) {
	if namespace == "" || len(components)%2 != 0 {
		return Key{}, fmt.Errorf("%w: namespace required and components must be pairs", ErrInvalidKey)
	}
	fields := make(map[string]string, len(components)/2)
	names := make([]string, 0, len(components)/2)
	for i := 0; i < len(components); i += 2 {
		name, value := components[i], components[i+1]
		if name == "" || value == "" {
			return Key{}, fmt.Errorf("%w: empty component", ErrInvalidKey)
		}
		if _, exists := fields[name]; exists {
			return Key{}, fmt.Errorf("%w: duplicate component name", ErrInvalidKey)
		}
		fields[name] = value
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	_, _ = h.Write([]byte("ratelimit:key:v1\x00"))
	writeField(h, namespace)
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(names)))
	_, _ = h.Write(count[:])
	for _, name := range names {
		writeField(h, name)
		writeField(h, fields[name])
	}
	return Key{hash: "v1:" + hex.EncodeToString(h.Sum(nil))}, nil
}

func writeField(h hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write([]byte(value))
}
