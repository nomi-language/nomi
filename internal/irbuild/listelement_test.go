package irbuild

import (
	"strings"
	"testing"
)

// i6FrontEndError is the front end's own rejection of a source, and fails when
// the front end ACCEPTED it.
//
// The inverse helper exists because two of the claims below are about the front
// end drawing the line, and "the builder refuses it" and "the program does not
// exist" are indistinguishable from a bare `lowers == false`.
func i6FrontEndError(t *testing.T, src string) string {
	t.Helper()
	if _, err := AnalyzeSource("main", src); err != nil {
		return err.Error()
	}
	t.Fatal("the front end ACCEPTED this program; the claim under test was that it does not")
	return ""
}

// --- the five untyped literals in a list literal ----------------------------

func TestListElement_GenuineMismatchStillRefuses(t *testing.T) {
	// THE PAIRED NEGATIVE, and it lands in the FRONT END rather than in the
	// builder, which builds a list literal at the type the checker recorded
	// for it. The checker rejects `[None, [1]]`, so the builder never sees
	// it, and it is asserted HERE so an analyzer that later admitted the
	// program produces a red test.
	const src = `import
  std::io
end

fn main()
  bad = [None, [1]]
  io.print("${Iter.count(bad)}")
end
`
	if msg := i6FrontEndError(t, src); !strings.Contains(msg, "list element type mismatch") {
		t.Fatalf("the front end rejected `[None, [1]]` for %q; the expected reason is its own "+
			"list element type mismatch.", msg)
	}
}

// --- the dot shorthand for a payload-less prelude variant -------------------

func TestDotVariant_AliasThroughTheDotIsAFrontEndError(t *testing.T) {
	// Half one of the alias pair. The name route cannot canonicalize an alias,
	// and this is what makes that harmless rather than a hole: the analyzer
	// resolves the dot against the DECLARATION's variant names, so the program
	// does not exist. If this ever starts ANALYZING, the builder will
	// decline on the local spelling and the site will refuse by name — which is
	// safe, but somebody should know, hence a red test rather than a comment.
	const src = `import
  std::io
  std::option.Option.{None as Nothing}
end

fn main()
  m: Option<Int> = .Nothing
  io.print("${m == m}")
end
`
	if msg := i6FrontEndError(t, src); !strings.Contains(msg, "no variant 'Nothing' on enum Option") {
		t.Fatalf("the front end rejected the aliased dot for %q, not for the missing "+
			"variant; the alias reachability argument in dotvariant.go rests on that reason", msg)
	}
}

// --- a named argument to a stdlib callee ------------------------------------

// --- the absent-key fault text ----------------------------------------------

// --- std/supervisors.Restart ------------------------------------------------
