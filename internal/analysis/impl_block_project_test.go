package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"strings"
	"testing"
)

// Project-level impl-block tests: orphan + collision coherence runs
// over block-form impls through the BuildProject pipeline (with real stdlib),
// mirroring how the runtime drives analysis.

func buildImplBlockProject(src string) []analysis.TypeError {
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, "", nil)
	checkErrs := analysis.CheckTypes(fa, nodes)
	all := append([]analysis.TypeError{}, fa.TypeErrors...)
	all = append(all, checkErrs...)
	return all
}

func errsContain(errs []analysis.TypeError, anyOf ...string) bool {
	for _, e := range errs {
		for _, s := range anyOf {
			if strings.Contains(e.Message, s) {
				return true
			}
		}
	}
	return false
}

func errsJoin(errs []analysis.TypeError) string {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n  ")
}

// TestImplBlockProject_ModuleFunctionWithLocalTypeAccepted: module helpers for
// local types are ordinary functions and never orphan impls.
func TestImplBlockProject_ModuleFunctionWithLocalTypeAccepted(t *testing.T) {
	src := `pub struct Point {
  x: Int
  y: Int
}

pub fn origin(): Point { Point{x: 0, y: 0} }


fn main() { origin() }`
	errs := buildImplBlockProject(src)
	if errsContain(errs, "orphan") {
		t.Errorf("local module helper should NOT be an orphan, got: %s", errsJoin(errs))
	}
}

// TestImplBlockProject_Collision_DuplicateModuleFunction: two same-name module
// functions collide in the ordinary module scope.
func TestImplBlockProject_Collision_DuplicateModuleFunction(t *testing.T) {
	src := `pub struct Point {
  x: Int
  y: Int
}

pub fn make(): Point { Point{x: 0, y: 0} }
pub fn make(): Point { Point{x: 1, y: 1} }


fn main() {}`
	errs := buildImplBlockProject(src)
	if !errsContain(errs, "'make' is already defined") {
		t.Errorf("expected duplicate module function error for two make functions, got: %s", errsJoin(errs))
	}
}

// TestImplBlockProject_InterfaceImpl_Dispatch: a block-form interface impl
// type-checks and a bare-name dispatch call resolves (no spurious errors).
func TestImplBlockProject_InterfaceImpl_Dispatch(t *testing.T) {
	src := `pub interface Speech {
    fn speak(value: self): String
}

pub struct Dog { name: String }

impl Speech for Dog {
    fn speak(d: Dog): String { d.name }
}

fn demo(): String {
    Speech.speak(Dog{name: "Rex"})
}`
	errs := buildImplBlockProject(src)
	if len(errs) > 0 {
		t.Fatalf("block-form interface impl + dispatch should type-check, got: %s", errsJoin(errs))
	}
}
