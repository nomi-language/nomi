package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// Context is a prelude type. User declarations cannot replace its runtime identity.
func TestStdHostContextShadowDoesNotAnchor(t *testing.T) {
	for _, shadow := range []struct {
		name string
		src  string
	}{
		{"a struct", "pub struct Context {\n  n: Int\n}\n\nfn main(): Unit {\n  _ = Context{n: 1}\n}\n"},
		{"a distinct newtype", "pub type Context Int\n\nfn main(): Unit {\n  _ = Context(1)\n}\n"},
	} {
		t.Run(shadow.name, func(t *testing.T) {
			_, err := AnalyzeSource("m", shadow.src)
			if err == nil || !strings.Contains(err.Error(), "type name 'Context' is reserved") {
				t.Fatalf("expected reserved prelude name diagnostic, got %v", err)
			}
		})
	}
}

// TestStdHostContextAnchorsInStdAndInAnImporter is the POSITIVE half, and it
// exists because the negative one above passes trivially if the anchor never
// fires at all.
//
// Both doors, because they are separate conjuncts in stdHostOriginAnchored and a
// single-door test cannot tell which one is carrying: `fa.Origin` for
// std/context's own signature pass (`Context.root(): Context` has to be
// representable in the module that DECLARES it, or nothing lowers) and
// stdImportModuleOf for every module that imports the name.
func TestStdHostContextAnchorsInStdAndInAnImporter(t *testing.T) {
	// The IMPORTER door, through an ordinary user program.
	p, err := AnalyzeSource("m", "\n\n"+
		"fn main(): Unit {\n  _ = Context.root()\n}\n")
	if err != nil {
		t.Fatalf("front end rejected the fixture: %v", err)
	}
	if _, anchored := stdHostAnchors(p.Entry().FA)["Context"]; !anchored {
		t.Error("a module importing std/context.Context did NOT anchor, so every mention of the " +
			"type refuses and the stdHostSpecs row buys nothing")
	}
	// The DECLARING door. Reached through the lowering index rather than through
	// std's FileAnalysis directly, because that is the consumer whose answer
	// matters: `collectStdCandidates` runs the signature pass over std/context
	// itself, and a missing anchor there refuses `root` at its DECLARATION.
	idx := stdlibLowering()
	f, known := idx.byKey["context.Context.root"]
	if !known {
		t.Fatal("the stdlib index has no `context.Context.root`, so the declaring door cannot be measured")
	}
	if f.rtCall == "" && !f.body {
		t.Errorf("`context.Context.root` is refused as %q at its own declaration; the fa.Origin "+
			"conjunct is what makes `Context` representable inside std/context, where the name is "+
			"declared rather than imported", f.why)
	}
	// And the SHAPE conjunct, which neither door above exercises: a row must not
	// redirect a declaration it has not checked. Asserted through the one
	// property a test can reach without editing std — that the row's own
	// predicate rejects a GENERIC `host type`, which is the other branch of
	// buildExternTypeShell and a completely different mechanism (a *DistinctType
	// with TypeParams, which is what std/channels' Sender<T>/Receiver<T> are).
	var row *stdHostSpec
	for i := range stdHostSpecs {
		if stdHostSpecs[i].nomi == "Context" {
			row = &stdHostSpecs[i]
		}
	}
	if row == nil {
		t.Fatal("stdHostSpecs has no Context row")
	}
	for _, bad := range []struct {
		name string
		decl *ast.ExternType
	}{
		{"a private declaration", &ast.ExternType{Name: "Context"}},
		{"a differently named declaration", &ast.ExternType{Name: "Ctx", Public: true}},
		{"a GENERIC host type", &ast.ExternType{Name: "Context", Public: true,
			TypeParams: []ast.TypeParam{{Name: "T"}}}},
		{"a declaration with a body", &ast.ExternType{Name: "Context", Public: true, HasBody: true}},
		{"a declaration carrying a non-derive decorator", &ast.ExternType{Name: "Context", Public: true,
			Decorators: []ast.Decorator{{Name: "wrap"}}}},
	} {
		if row.declaredHostType(bad.decl) {
			t.Errorf("the Context row accepted %s; every check in declaredHostType decides "+
				"REPRESENTATION and must fail toward NO anchor", bad.name)
		}
	}
	if !row.declaredHostType(&ast.ExternType{Name: "Context", Public: true}) {
		t.Error("the Context row rejected the shape std/context actually declares, so the anchor " +
			"can never fire and the two doors above are passing for some other reason")
	}
	// `derive` must still be accepted, for opaqueSpec.matches' reason: it is
	// metadata about work already done, not a gap. Without this the predicate
	// could be tightened to "no decorators at all" and nothing would notice
	// until std added one.
	if !row.declaredHostType(&ast.ExternType{Name: "Context", Public: true,
		Decorators: []ast.Decorator{{Name: "derive"}}}) {
		t.Error("the Context row rejected a `derive` decorator; derive is synthesized into an " +
			"ordinary impl block that lowers through the ordinary path, so it is not a refusal " +
			"to be redirected past")
	}
}
