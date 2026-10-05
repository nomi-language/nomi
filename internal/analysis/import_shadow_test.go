package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// FileAnalysis.ModuleScope has one job its consumers actually ask of it: given
// a name, which declaration does it mean in this file? These tests pin the
// answer for the case where it used to lie.
//
// The lie: defineImport is annotation-side and BuildProject runs every file's
// declaration stubs before any file's annotations, so a selective import's
// scope.Define always wrote LAST and silently replaced whatever the file
// declared under that name. For every declaring kind —
// interface, struct, enum, function — `import std/json.{ToJson}` beside a local
// `ToJson` declaration left ModuleScope pointing at the ImportStmt symbol, with
// the only diagnostic being "imported name 'ToJson' is unused". That message
// names the SURVIVOR and calls it dead; the local declaration is what became
// unreachable.
//
// This is why `ModuleScope.Lookup` was not usable as a shadow oracle, and it
// was never interface-specific — the interface case was simply the one anybody
// looked at, because for the prelude-shadowed data kinds the program was
// already rejected by checkReservedTypeName and nobody read further.

// declKindCases covers every declaring kind against the same import, because a
// fix that only reached one kind would look complete from the angle it reached.
var declKindCases = []struct {
	kind string
	decl string
}{
	{"interface", "pub interface ToJson {\n  fn probe(v: self): String\n}\n"},
	{"struct", "pub struct ToJson {\n  n: Int\n}\n"},
	{"enum", "pub enum ToJson {\n  A\n}\n"},
	{"function", "pub fn ToJson(): Int { 1 }\n"},
}

// TestModuleScope_LocalDeclarationOutranksASelectiveImport is the oracle
// property itself: after a collision, the slot holds the file's own
// declaration, not the import.
func TestModuleScope_LocalDeclarationOutranksASelectiveImport(t *testing.T) {
	for _, c := range declKindCases {
		c := c
		t.Run(c.kind, func(t *testing.T) {
			fa, _ := reservedNamesFA(t, "import std/json.{ToJson}\n\n"+c.decl)
			sym := fa.ModuleScope.LookupLocal("ToJson")
			if sym == nil {
				t.Fatal("ToJson is absent from module scope entirely")
			}
			if _, imported := sym.Node.(*ast.ImportStmt); imported {
				t.Fatalf("module scope answers with the IMPORT for a name this file declares as a %s; "+
					"ModuleScope.Lookup is not a shadow oracle when it does that", c.kind)
			}
		})
	}
}

// TestModuleScope_ImportOnADeclaredNameIsReported: the collision is diagnosed
// rather than silently resolved. Without this the winner changes under the
// user with no message at all, which is how the original defect survived.
func TestModuleScope_ImportOnADeclaredNameIsReported(t *testing.T) {
	for _, c := range declKindCases {
		c := c
		t.Run(c.kind, func(t *testing.T) {
			errs := buildAndCollectErrs(t, "import std/json.{ToJson}\n\n"+c.decl)
			for _, e := range errs {
				if strings.Contains(e, "already defined in this scope") && strings.Contains(e, "ToJson") {
					return
				}
			}
			t.Fatalf("no redeclaration diagnostic for an import landing on a local %s declaration; got: %v", c.kind, errs)
		})
	}
}

// TestModuleScope_DeclarationOrderDoesNotDecideTheWinner. The original bug was
// an artifact of pass ordering, so the fix must not be one: the declaration
// wins whether it is written above or below the import.
func TestModuleScope_DeclarationOrderDoesNotDecideTheWinner(t *testing.T) {
	decl := "pub interface ToJson {\n  fn probe(v: self): String\n}\n"
	for _, c := range []struct {
		name string
		src  string
	}{
		{"import first", "import std/json.{ToJson}\n\n" + decl},
		{"declaration first", decl + "\nimport std/json.{ToJson}\n"},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			fa, _ := reservedNamesFA(t, c.src)
			sym := fa.ModuleScope.LookupLocal("ToJson")
			if sym == nil {
				t.Fatal("ToJson absent from module scope")
			}
			if _, ok := sym.Node.(*ast.InterfaceDef); !ok {
				t.Fatalf("winner depends on source order: got %T, want the local *ast.InterfaceDef", sym.Node)
			}
		})
	}
}

// TestModuleScope_ExplicitPreludeReimportStaysLegal is the boundary, and it is
// the half that a wider fix gets wrong. `import std/maybe.{Maybe}` beside the
// prelude's own injected `Maybe` is the explicit-is-better idiom; stdlib files
// and several analysis fixtures write it. An import landing on ANOTHER IMPORT's
// slot is not a collision and must stay silent.
//
// Written as a table over prelude names of both kinds because the first attempt
// at this fix routed every selective import through the redeclare check and
// broke exactly these: four analysis tests, on `Maybe` and on `Display`.
func TestModuleScope_ExplicitPreludeReimportStaysLegal(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"Maybe", "import std/maybe.{Maybe}\n\npub fn f(_m: Maybe<Int>): Int { 0 }\n"},
		{"Display", "import std/display.{Display}\n\npub fn g(_d: Display): Int { 0 }\n"},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			for _, e := range buildAndCollectErrs(t, c.src) {
				if strings.Contains(e, "already defined in this scope") {
					t.Errorf("explicitly re-importing the prelude name %q is legal, got: %s", c.name, e)
				}
			}
		})
	}
}

// TestModuleScope_TwoSelectiveImportsStillLastWins documents the residual this
// fix deliberately does NOT close, so the next reader does not assume it did.
// Import-vs-import precedence is silent last-wins, unchanged. It is recorded as
// a pinned observation rather than an aspiration: if someone gives it a rule,
// this test fires and points at the decision.
func TestModuleScope_TwoSelectiveImportsStillLastWins(t *testing.T) {
	src := "import std/json.{ToJson}\nimport std/json.{ToJson}\n\npub fn f(_x: ToJson): Int { 0 }\n"
	fa, _ := reservedNamesFA(t, src)
	sym := fa.ModuleScope.LookupLocal("ToJson")
	if sym == nil {
		t.Fatal("ToJson absent from module scope")
	}
	if _, imported := sym.Node.(*ast.ImportStmt); !imported {
		t.Fatalf("expected an import symbol, got %T", sym.Node)
	}
	if sym.Pos.Line != 2 {
		t.Errorf("import-vs-import is documented as silent last-wins; the slot holds line %d, want 2. "+
			"If that rule changed deliberately, this test is the place to say so", sym.Pos.Line)
	}
	for _, e := range buildAndCollectErrs(t, src) {
		if strings.Contains(e, "already defined in this scope") {
			t.Errorf("import-vs-import is deliberately left undiagnosed by this fix, got: %s", e)
		}
	}
}

// TestModuleScope_SynthesizedPreludeImportIsNotReported. The auto-prepended
// prelude chain lands in the synth band and has no user-visible position, so it
// must never produce a diagnostic — a local declaration of a prelude name is
// reported at its OWN site by checkReservedTypeName instead. Asserted on a
// declaring kind that IS prelude-reserved, so the only message about it is the
// reserved-name one.
func TestModuleScope_SynthesizedPreludeImportIsNotReported(t *testing.T) {
	errs := buildAndCollectErrs(t, "pub struct Maybe {\n  n: Int\n}\n")
	reserved := 0
	for _, e := range errs {
		if strings.Contains(e, "already defined in this scope") {
			t.Errorf("the synthesized prelude import reported a collision at a position the user cannot see: %s", e)
		}
		if strings.Contains(e, "reserved") && strings.Contains(e, "'Maybe'") {
			reserved++
		}
	}
	if reserved != 1 {
		t.Errorf("expected exactly one reserved-name diagnostic for `struct Maybe`, got %d in %v", reserved, errs)
	}
}
