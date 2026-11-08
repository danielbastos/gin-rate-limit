package ratelimit_test

import (
	"errors"
	"reflect"
	"testing"

	ratelimit "github.com/danielbastos/gin-rate-limit"
)

func TestKeyCanonicalAndImmutable(t *testing.T) {
	parts := []string{"ip", "192.0.2.1", "email", "a@example.com"}
	before := append([]string(nil), parts...)
	a, err := ratelimit.NewKey("login", parts...)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ratelimit.NewKey("login", "email", "a@example.com", "ip", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if a != b || !reflect.DeepEqual(before, parts) {
		t.Fatal("non-canonical key or mutated input")
	}
	const vector = "v1:19ca76a7475ec2988856cf9bac57c5fdbac0348708bea7fda4072df52c14b55c"
	if a.String() != vector {
		t.Fatalf("key encoding changed: %s", a)
	}
	copy := a
	parts[1] = "changed"
	if a != copy || a.String() == "" {
		t.Fatal("mutable key")
	}
}

func TestKeyValidation(t *testing.T) {
	for _, tc := range []struct {
		ns    string
		pairs []string
	}{
		{"", nil}, {"n", []string{"a"}}, {"n", []string{"", "v"}},
		{"n", []string{"a", ""}}, {"n", []string{"a", "v", "a", "other"}},
	} {
		if _, err := ratelimit.NewKey(tc.ns, tc.pairs...); !errors.Is(err, ratelimit.ErrInvalidKey) {
			t.Errorf("error = %v", err)
		}
	}
	if _, err := ratelimit.NewKey("namespace-only"); err != nil {
		t.Fatal(err)
	}
}

func TestKeyUnambiguousEncoding(t *testing.T) {
	seen := make(map[ratelimit.Key]bool)
	for _, args := range [][]string{
		{"a", "bc", "d"}, {"ab", "c", "d"}, {"a", "b", "cd"},
		{"a", "b:c", "d"}, {"a", "b", "c:d"}, {"a", "b\x00", "c"},
		{"a", "b", "\x00c"}, {"a", "Email", "A"}, {"a", "email", "a"}, {"á", "名", "值"},
	} {
		key, err := ratelimit.NewKey(args[0], args[1:]...)
		if err != nil {
			t.Fatal(err)
		}
		if seen[key] {
			t.Fatal("encoding collision")
		}
		seen[key] = true
	}
}
