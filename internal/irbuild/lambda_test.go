package irbuild

import (
	"testing"
)

// The three pinned lambda programs. Each pins the REFERENCE text absolutely,
// because a comparison between two runs passes when both silently produce
// nothing, and "nothing" is exactly what a refused construct produces.

func TestLambda_ConflictingReturnTypesAreRejected(t *testing.T) {
	_, err := AnalyzeSource("main", `fn main() {
  _ = |x: Int| {
    if x > 0 { return "positive" }
    1
  }
}`)
	requireFrontEndRejection(t, err, "lambda return and fallthrough disagree", "return type mismatch")
}

// TestLambdaKindsInternByGoType is the kind-identity half of the interning
// contract, at the level `kind`'s `==` sees.
//
// Two function types are the same type exactly when their Go types are
// identical. Without interning, `kind{tag: tagFunc}` would compare equal for
// every function type in the language and `(Int) -> Int` would satisfy a
// `(String) -> Bool` position.
func TestLambdaKindsInternByGoType(t *testing.T) {
	g := &gen{}
	intInt := g.funcKind([]kind{kindInt}, kindInt)
	sameAgain := g.funcKind([]kind{kindInt}, kindInt)
	strBool := g.funcKind([]kind{kindString}, kindBool)
	noArgs := g.funcKind(nil, kindInt)

	if intInt != sameAgain {
		t.Fatalf("two spellings of (Int) -> Int are different kinds: %q vs %q",
			intInt.nomi(), sameAgain.nomi())
	}
	if intInt == strBool {
		t.Fatal("(Int) -> Int and (String) -> Bool compare equal")
	}
	if intInt == noArgs {
		t.Fatal("(Int) -> Int and () -> Int compare equal")
	}
	if got, want := intInt.nomi(), "(Int) -> Int"; got != want {
		t.Fatalf("Go type = %q, want %q — the frame must be in the signature", got, want)
	}
	if got, want := intInt.nomi(), "(Int) -> Int"; got != want {
		t.Fatalf("Nomi spelling = %q, want %q", got, want)
	}
}
