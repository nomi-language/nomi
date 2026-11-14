package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// checkWithBuilderErrors is checkWithStdlib plus the errors the builder
// records on the file, where a misplaced declaration is reported.
func checkWithBuilderErrors(src string) []analysis.TypeError {
	src = withStdlibTestImports(src)
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	lib := loadStd()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	errs := append([]analysis.TypeError{}, analysis.BuildTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	return append(errs, fa.TypeErrors...)
}

// A `once` inside a function body is refused, and the message names both
// places a `once` may stand: file level and an inherent impl block.
func TestOnceInABody_NamesWhereItMayStand(t *testing.T) {
	errs := checkWithBuilderErrors(`import std::io

fn main()
  once x = 3
  io.inspect(x)
end
`)
	const want = "'once x' must appear at file level or in an inherent impl block — function bodies use ordinary 'name = value' bindings"
	for _, e := range errs {
		if e.Message == want {
			if e.Line != 4 {
				t.Fatalf("error at line %d, want 4", e.Line)
			}
			return
		}
	}
	t.Fatalf("no error %q; got %v", want, errs)
}

// An inherent impl block holds a `once`, read through its type.
func TestOnceInAnInherentImpl_IsAccepted(t *testing.T) {
	errs := checkWithBuilderErrors(`import std::io

struct Money
  cents: Int
end

impl Money
  pub once zero = Money{cents: 0}
end

fn main()
  io.inspect(Money.zero.cents)
end
`)
	if len(errs) != 0 {
		t.Fatalf("the front end rejects a once in an inherent impl: %v", errs)
	}
}
