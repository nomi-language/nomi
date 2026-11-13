package rt

import (
	"math"
	"testing"
)

// TestDynamicEquality holds DynamicEqual to the JSON-value rule its doc
// comment gives, and DynamicHash to it: every equal pair hashes alike,
// through rt.Equal and rt.Hash as a Map key is found.
func TestDynamicEquality(t *testing.T) {
	d := func(v any) Dynamic { return Dynamic{Inner: v} }
	equal := []struct {
		name string
		a, b any
	}{
		{"null", nil, nil},
		{"bool", true, true},
		{"string", "a", "a"},
		{"int widths", int8(1), int64(1)},
		{"unsigned and signed", uint32(7), int(7)},
		{"uint64 past int64", uint64(math.MaxUint64), uint(math.MaxUint64)},
		{"float widths", float32(0.5), 0.5},
		{"negative zero", 0.0, math.Copysign(0, -1)},
		{"array", []any{int64(1), "x", nil}, []any{1, "x", nil}},
		{"object in another order", map[string]any{"a": int64(1), "b": []any{true}}, map[string]any{"b": []any{true}, "a": 1}},
		{"nested", map[string]any{"k": map[string]any{"n": 2.5}}, map[string]any{"k": map[string]any{"n": float32(2.5)}}},
	}
	for _, c := range equal {
		a, b := d(c.a), d(c.b)
		if !Equal(a, b) || !Equal(b, a) {
			t.Errorf("%s: %v and %v are unequal", c.name, c.a, c.b)
		}
		if Hash(a) != Hash(b) {
			t.Errorf("%s: %v and %v are equal and hash apart", c.name, c.a, c.b)
		}
	}
	unequal := []struct {
		name string
		a, b any
	}{
		{"null and false", nil, false},
		{"Int and Float of one number", int64(1), 1.0},
		{"strings", "a", "b"},
		{"uint64 past int64 and -1", uint64(math.MaxUint64), int64(-1)},
		{"array order", []any{1, 2}, []any{2, 1}},
		{"array length", []any{1}, []any{1, 1}},
		{"object values", map[string]any{"a": 1}, map[string]any{"a": 2}},
		{"object keys", map[string]any{"a": 1}, map[string]any{"b": 1}},
		{"array and object", []any{}, map[string]any{}},
	}
	for _, c := range unequal {
		if Equal(d(c.a), d(c.b)) || Equal(d(c.b), d(c.a)) {
			t.Errorf("%s: %v and %v are equal", c.name, c.a, c.b)
		}
	}
	if Equal(d("a"), "a") {
		t.Error("a Dynamic equals the String it holds")
	}
	var m Map[any, any]
	m = MapPut(m, Hash, Equal, any(d(map[string]any{"a": 1, "b": 2})), any(int64(9)))
	if got, ok := MapLookup(m, Hash, Equal, any(d(map[string]any{"b": int64(2), "a": int8(1)}))); !ok || got != int64(9) {
		t.Errorf("a Map keyed by a Dynamic object does not find an equal key: %v, %v", got, ok)
	}
}
