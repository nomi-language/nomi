package irbuild

import (
	"sort"
	"testing"
)

// THE INSTRUMENT'S OWN POSITIVE. `refusalKeySet` reads `unsup.Constructs()`, and
// a reading that could not see `unbound name` would make the assertion above
// hold for any tree at all — the failure mode that killed eighteen witnesses in
// this package. So the key is planted and the same `contains` call that decides
// the test is required to find it.
func TestFuncRef_TheUnboundNameReadingIsNotBlind(t *testing.T) {
	planted := &UnsupportedError{Items: []Unsupported{
		{Construct: "non-scalar field type", Detail: "Bag.items: Iter<Int>"},
		{Construct: "unbound name", Detail: "mk"},
	}}
	keys := planted.Constructs()
	sort.Strings(keys)
	if !contains(keys, "unbound name") {
		t.Fatalf("the reading cannot see a planted `unbound name`, so every assertion in "+
			"this file is vacuous; it read %v", keys)
	}
}
