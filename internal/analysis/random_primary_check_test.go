package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// TestRandomModuleTypechecksAsPrimary guards against type errors that only
// surface when std/random is checked as a *primary* module (what the LSP does
// when the file is open), not as an imported dependency. The regression that
// motivated it: the generator constructors (uniform/weighted/constant) shared
// one stale type-owner context, so their internal `int`/`list`/`map` calls
// instantiated that same `T` to concrete types and polluted signatures. Each
// constructor now declares its own `<T>`.
// This class of error is invisible to the test-program harnesses (they only
// consume the module), so it reached a user's editor before being caught.
//
// The check is preludeless (a stdlib module gets no prelude). Note: because the
// synthetic project root is empty, the opaque-construction *package-identity*
// check spuriously fires ("constructor of opaque type ... is private to its
// defining module") — that is a harness artifact of projectRoot="", not a real
// diagnostic (the real std build, which has a module root, does not emit it,
// and `go test ./analysis` over the real stdlib is clean). We filter
// exactly that string and assert everything else is empty.
func TestRandomModuleTypechecksAsPrimary(t *testing.T) {
	parse := func(s string) []ast.Node {
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(s))
		return nodes
	}
	entryNodes := parse("import std/random: Random\n")
	_, cache, extended := analysis.BuildProjectWithCache(entryNodes, nil, nil, nil, "", std.MakeLoader())
	fa := cache["std/random"]
	if fa == nil {
		t.Fatal("std/random module was not discovered into the project cache")
	}

	var real []string
	report := func(e analysis.TypeError) {
		msg := e.Error()
		if strings.Contains(msg, "constructor of opaque type") {
			return // projectRoot="" package-identity artifact; see doc comment
		}
		real = append(real, msg)
	}
	for _, e := range fa.TypeErrors {
		report(e)
	}
	for _, e := range analysis.CheckTypes(fa, extended["std/random"]) {
		report(e)
	}
	if len(real) > 0 {
		t.Fatalf("std/random.nomi has %d real type error(s) when checked as a primary module:\n  %s",
			len(real), strings.Join(real, "\n  "))
	}
}
