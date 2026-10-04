package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"strings"
	"testing"
)

// buildProjectForDefaultConfig runs BuildProject + CheckTypes the same
// way the runtime does. Used by the tests below to exercise the
// project-build path the runtime drives.
func buildProjectForDefaultConfig(src string) []analysis.TypeError {
	tokens := lexer.Lex(src)
	nodes, parseErrors := parser.ParseWithRecovery(tokens)
	if len(parseErrors) > 0 {
		out := []analysis.TypeError{}
		for _, err := range parseErrors {
			out = append(out, analysis.TypeError{Message: err.Error()})
		}
		return out
	}
	lib := std.Load()
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, "", nil)
	checkErrs := analysis.CheckTypes(fa, nodes)
	all := append([]analysis.TypeError{}, fa.TypeErrors...)
	all = append(all, checkErrs...)
	return all
}

func expectNoErrorsT(t *testing.T, errs []analysis.TypeError) {
	t.Helper()
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Fatalf("expected no errors, got %d:\n  %s", len(errs), strings.Join(msgs, "\n  "))
	}
}

func expectErrorT(t *testing.T, errs []analysis.TypeError, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			return
		}
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	t.Fatalf("expected error containing %q, got %d errors:\n  %s", substr, len(errs), strings.Join(msgs, "\n  "))
}

func TestChecker_RejectShadowingPrelude_Typealias(t *testing.T) {
	src := `typealias Int String
fn main() {}
`
	errs := buildProjectForDefaultConfig(src)
	expectErrorT(t, errs, "Int")
	expectErrorT(t, errs, "reserved")
}

func TestChecker_RejectShadowingPrelude_Struct(t *testing.T) {
	src := `struct String { len: Int = 0 }
fn main() {}
`
	errs := buildProjectForDefaultConfig(src)
	expectErrorT(t, errs, "String")
	expectErrorT(t, errs, "reserved")
}

// TestChecker_AllowConfigAsTypealias: `Config` is just a conventional
// name now — it can be a typealias, a distinct type, an enum, or
// anything else. The compiler only finds Config-as-the-capability-
// struct via name-and-kind matching at the project-build pass.
func TestChecker_AllowConfigAsTypealias(t *testing.T) {
	src := `typealias Config Int
fn main() {}
`
	errs := buildProjectForDefaultConfig(src)
	if len(errs) != 0 {
		t.Errorf("expected no errors for `typealias Config Int` (Config is no longer reserved), got %v", errs)
	}
}

func TestChecker_AllowDistinctTypeWrapper(t *testing.T) {
	// Distinct types are the proper escape hatch when a project
	// genuinely wants a domain-specific Int-like type — they create a
	// real new type rather than confusingly aliasing the built-in.
	src := `type UserId Int
fn main() {}
`
	errs := buildProjectForDefaultConfig(src)
	if len(errs) != 0 {
		t.Errorf("expected no errors for distinct-type wrapper, got %v", errs)
	}
}
