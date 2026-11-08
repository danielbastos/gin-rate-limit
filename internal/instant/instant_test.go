package instant

import (
	"math/big"
	"testing"
	"time"
)

func TestEncodingsPreserveOrderAndPrecision(t *testing.T) {
	values := []struct {
		at      time.Time
		decimal string
	}{
		{time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), "-62135596800000000000"},
		{time.Unix(-1, 999999999), "-1"},
		{time.Unix(0, 0), "0"},
		{time.Unix(0, 1), "1"},
		{time.Unix(1, 0), "1000000000"},
		{time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC), "253402300799999999999"},
	}
	var previous string
	var prevDecimal *big.Int
	for _, tc := range values {
		encoded := Lex(tc.at)
		if len(encoded) != 24 || previous != "" && encoded <= previous {
			t.Fatalf("invalid lex order: %s after %s", encoded, previous)
		}
		if Decimal(tc.at) != tc.decimal {
			t.Fatalf("decimal=%s want=%s", Decimal(tc.at), tc.decimal)
		}
		decimal, ok := new(big.Int).SetString(Decimal(tc.at), 10)
		if !ok || prevDecimal != nil && decimal.Cmp(prevDecimal) <= 0 {
			t.Fatal("invalid decimal order")
		}
		if Lex(tc.at.In(time.FixedZone("offset", -10800))) != encoded {
			t.Fatal("timezone changed encoding")
		}
		previous, prevDecimal = encoded, decimal
	}
}
