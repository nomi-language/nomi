package ir

import "testing"

// A generic struct's, enum's and distinct type's instance key is its name
// and type arguments, by one rule. A distinct's key comes from its type
// arguments and not its inner type, so two phantom instances over one inner
// type are two instances, and their types are not identical. A non-generic
// distinct and a marker have no key and spell as before.
func TestInstanceKey_ADistinctIsKeyedByItsTypeArguments(t *testing.T) {
	tagged := NewSymbol("main.Tagged")
	meters, feet := NewStructType(NewSymbol("main.Meters")), NewStructType(NewSymbol("main.Feet"))
	a := NewDistinctType(tagged, IntType, meters)
	b := NewDistinctType(tagged, IntType, feet)
	cases := []struct {
		t          *ValType
		key, spell string
	}{
		{NewStructType(NewSymbol("main.Box"), IntType), "main.Box<Int>", "main.Box<Int>"},
		{NewEnumType(NewSymbol("main.Opt"), StringType), "main.Opt<String>", "main.Opt<String>"},
		{NewDistinctType(NewSymbol("main.Wrap"), IntType, IntType), "main.Wrap<Int>", "main.Wrap<Int>(Int)"},
		{a, "main.Tagged<main.Meters>", "main.Tagged<main.Meters>(Int)"},
		{b, "main.Tagged<main.Feet>", "main.Tagged<main.Feet>(Int)"},
		{NewDistinctType(NewSymbol("main.Id"), IntType), "", "main.Id(Int)"},
		{NewDistinctType(NewSymbol("main.Done"), nil), "", "main.Done"},
		{NewStructType(NewSymbol("main.Point")), "", "main.Point"},
	}
	for _, c := range cases {
		if got := c.t.InstanceKey(); got != c.key {
			t.Errorf("%s: InstanceKey() = %q, want %q", c.spell, got, c.key)
		}
		if got := c.t.String(); got != c.spell {
			t.Errorf("String() = %q, want %q", got, c.spell)
		}
	}
	if a.Identical(b) || a.Accepts(b) {
		t.Errorf("%s and %s are one type", a, b)
	}
	if got := a.Class(); got != ClassWord {
		t.Errorf("%s is in class %s, want word: its inner type decides", a, got)
	}
}
