package analysis_test

import "testing"

// patternTypeVarPrelude declares a generic call whose result reaches the
// pattern checker as `Opt<T>` with T a type variable bound to the payload.
const patternTypeVarPrelude = `enum Opt<T> {
  Nothing
  Has(T)
}

struct Pt {
  x: Int
  y: Int
}

type Id Int

fn pick<T>(c: Bool, a: T, b: T): T {
  if c { b } else { a }
}
`

// Each pattern shape matches a payload whose type is a bound type variable.
// The program that runs these arms is internal/irbuild's
// TestIRPattern_EveryShapeThroughATypeVariable.
func TestChecker_PatternsResolveABoundTypeVariable(t *testing.T) {
	for _, tc := range []struct{ name, arms string }{
		{"tuple", `case pick(True, Opt.Nothing, Opt.Has((1, "x"))) {
    .Has((s, t)) -> s + String.length(t)
    .Nothing -> 0
  }`},
		{"struct", `case pick(True, Opt.Nothing, Opt.Has(Pt{x: 3, y: 4})) {
    .Has(Pt{x, y}) -> x + y
    .Nothing -> 0
  }`},
		{"list", `case pick(True, Opt.Nothing, Opt.Has([1, 2])) {
    .Has([a, ..rest]) -> a + Iter.count(rest)
    .Has([]) -> 0
    .Nothing -> 0
  }`},
		{"variant", `case pick(True, Opt.Nothing, Opt.Has(Opt.Has(7))) {
    .Has(.Has(n)) -> n
    .Has(.Nothing) -> 0
    .Nothing -> 0
  }`},
		{"map", `case pick(True, Opt.Nothing, Opt.Has({"k" => 8})) {
    .Has({"k" => v}) -> v
    .Has(_) -> 0
    .Nothing -> 0
  }`},
		{"distinct", `case pick(True, Opt.Nothing, Opt.Has(Id(9))) {
    .Has(Id(i)) -> i
    .Nothing -> 0
  }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := patternTypeVarPrelude + "\nfn f(): Int {\n  " + tc.arms + "\n}\n"
			_, errs := checkSourceWithStdlib(src)
			expectNoErrorsT(t, errs)
		})
	}
}

// Resolving the type variable keeps a real mismatch an error, worded with
// the concrete type the variable is bound to.
func TestChecker_PatternMismatchThroughATypeVariableIsRejected(t *testing.T) {
	for _, tc := range []struct{ name, arms, want string }{
		{"tuple", `case pick(True, Opt.Nothing, Opt.Has(5)) {
    .Has((s, t)) -> s + String.length(t)
    .Nothing -> 0
  }`, "tuple pattern requires a tuple type, got Int"},
		{"struct", `case pick(True, Opt.Nothing, Opt.Has(5)) {
    .Has(Pt{x, y}) -> x
    .Nothing -> 0
  }`, "struct pattern requires a struct type, got Int"},
		{"list", `case pick(True, Opt.Nothing, Opt.Has(5)) {
    .Has([a, ..rest]) -> a + Iter.count(rest)
    .Has(_) -> 0
    .Nothing -> 0
  }`, "list pattern requires a list type, got Int"},
		{"variant", `case pick(True, Opt.Nothing, Opt.Has(5)) {
    .Has(.Has(n)) -> n
    .Has(_) -> 0
    .Nothing -> 0
  }`, "enum pattern requires an enum type, got Int"},
		{"tuple arity", `case pick(True, Opt.Nothing, Opt.Has((1, 2))) {
    .Has((a, b, c)) -> a
    .Has(_) -> 0
    .Nothing -> 0
  }`, "tuple pattern has 3 elements but expected 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := patternTypeVarPrelude + "\nfn f(): Int {\n  " + tc.arms + "\n}\n"
			_, errs := checkSourceWithStdlib(src)
			expectErrorT(t, errs, tc.want)
		})
	}
}
