package analysis

import (
	"fmt"
	"strings"
	"testing"
)

// The walk over generic type declarations visits each declaration's parts
// once, however many declarations reach it: a chain of n structs, each
// holding the next and each walked from its own check and from the one
// before it, visits each part's type once, 4n+1 types in all here, and the
// cycle the chain closes is reported once, at the part that grows.
func TestInstantiationCycle_WalksEachTypeDeclarationOnce(t *testing.T) {
	const n = 300
	var b strings.Builder
	for i := 0; i < n; i++ {
		next := fmt.Sprintf("S%d<T>", i+1)
		if i == n-1 {
			next = "S0<List<T>>"
		}
		fmt.Fprintf(&b, "struct S%d<T>\n  value: T\n  next: List<%s>\nend\n\n", i, next)
	}
	fa, errs := checkSourceWithModules(t, b.String(), nil)
	reported := 0
	for _, e := range errs {
		if e.Code == PolymorphicRecursionCode {
			reported++
			if e.Line != 5*(n-1)+3 {
				t.Errorf("the cycle is reported at line %d, want the growing part's, %d: %v", e.Line, 5*(n-1)+3, e)
			}
		}
	}
	if reported != 1 {
		t.Fatalf("want the cycle reported once, got %d errors: %v", reported, errs)
	}
	c := newChecker(fa, nil)
	for i := 0; i < n; i++ {
		sym := fa.ModuleScope.Lookup(fmt.Sprintf("S%d", i))
		if sym == nil {
			t.Fatalf("no S%d", i)
		}
		c.recordTypeInstantiations(sym.Node)
	}
	// Each struct's parts are `T` (one type) and `List<S<T>>` (three), and
	// the last one's `List<S0<List<T>>>` is one more.
	if c.typeInstSteps != 4*n+1 {
		t.Errorf("the walk visited %d types, want %d", c.typeInstSteps, 4*n+1)
	}
	if len(c.typeDeclsWalked) != n {
		t.Errorf("%d declarations walked for %d declared", len(c.typeDeclsWalked), n)
	}
}
