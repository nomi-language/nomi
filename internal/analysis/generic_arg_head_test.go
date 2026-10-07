package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// An argument whose outermost type constructor differs from a generic
// parameter's is a mismatch even while the parameter's type variables are
// unsolved: no substitution makes a `Set<T>` of Unit. Before, the generic
// argument check let these through and the program could not run.
func TestGenericArgHead_ADifferentConstructorIsAMismatch(t *testing.T) {
	errs := checkWithStdlib(`
fn first_or<T>(xs: List<T>, d: T): T {
  Maybe.with_default(List.head(xs), d)
}

fn main() {
  u = {}
  _ = Set.size(u)
  _ = List.head({})
  _ = Map.size(1.5)
  _ = first_or(3, 1)
  _ = Maybe.some?(4)
  _ = Set.size(#{1})
  _ = first_or([1], 2)
  _ = List.head(1..3 |> Iter.to_list())
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{
		"8:16 argument 1: expected Set<T>, got Unit",
		"9:17 argument 1: expected List<T>, got Unit",
		"10:16 argument 1: expected Map<K, V>, got Float",
		"11:16 argument 1: expected List<T>, got Int",
		"12:19 argument 1: expected Maybe<T>, got Int",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
