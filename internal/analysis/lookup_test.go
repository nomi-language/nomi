package analysis

import (
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"testing"
)

func TestSymbolAt_Reference(t *testing.T) {
	input := `fn double(x: Int): Int { x + x }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	// Find any reference to "x" and verify SymbolAt returns it
	for pos, sym := range file.References {
		if sym.Name == "x" {
			result := file.SymbolAt(pos)
			if result == nil {
				t.Fatal("SymbolAt returned nil for known reference")
			}
			if result.Name != "x" {
				t.Errorf("expected x, got %s", result.Name)
			}
			return
		}
	}
	t.Error("no reference to x found")
}

func TestSymbolAt_Definition(t *testing.T) {
	input := `fn add(x: Int, y: Int): Int { x + y }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	// Find the Add definition
	for pos, sym := range file.Definitions {
		if sym.Name == "add" {
			result := file.SymbolAt(pos)
			if result == nil {
				t.Fatal("SymbolAt returned nil for known definition")
			}
			if result.Name != "add" {
				t.Errorf("expected add, got %s", result.Name)
			}
			return
		}
	}
	t.Error("no definition of Add found")
}

// visibleNames returns the set of symbol names AllVisible reports for a scope —
// the exact data the LSP completion path consumes (completion.go:108).
func visibleNames(s *Scope) map[string]bool {
	names := map[string]bool{}
	if s == nil {
		return names
	}
	for _, sym := range s.AllVisible() {
		names[sym.Name] = true
	}
	return names
}

func TestScopeAt_InnermostScope(t *testing.T) {
	// `fn add(x: Int): Int { x }` — the body `x` sits at col 23. ScopeAt there
	// must descend into the fn body scope (NOT the module scope), and that
	// scope must see the parameter `x`.
	input := `fn add(x: Int): Int { x }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	scope := file.ScopeAt(Pos{Line: 1, Col: 23})
	if scope == nil {
		t.Fatal("ScopeAt returned nil")
	}
	if scope == file.ModuleScope {
		t.Error("expected the fn body scope, got the module scope")
	}
	if !visibleNames(scope)["x"] {
		t.Error("expected parameter x to be visible from the fn body scope")
	}
}

func TestScopeAt_LocalsVisibleInFunctionBody(t *testing.T) {
	// Tier 1 of the design's tangible example: locals bound earlier in a
	// function body must be visible to completion at a later position in that
	// body. Today ScopeAt returns the module scope, which structurally cannot
	// reach locals (they live in a child scope).
	input := "fn main() {\n  a = 1\n  b = 2\n  c = 3\n}"
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	names := visibleNames(file.ScopeAt(Pos{Line: 3, Col: 3}))
	for _, want := range []string{"a", "b", "c", "main"} {
		if !names[want] {
			t.Errorf("expected %q visible in fn body, got %v", want, names)
		}
	}
}

func TestScopeAt_LambdaParamVisibleInBody(t *testing.T) {
	// Tier 2 — the case a transparent-scope workaround would fail: inside a
	// single-expression lambda body, the lambda's own param must be visible.
	// `  f = |x| x + 1` — the body `x` is at col 11, inside the lambda span.
	input := "fn main() {\n  f = |x| x + 1\n}"
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	names := visibleNames(file.ScopeAt(Pos{Line: 2, Col: 11}))
	if !names["x"] {
		t.Errorf("expected lambda param x visible inside its body, got %v", names)
	}
}

func TestScopeAt_LambdaParamAtTrailingEdge(t *testing.T) {
	// Completion is naturally invoked right AFTER the last token of a lambda
	// body — the cursor then sits exactly at the scope's End. A cursor is a
	// point between characters, so the End must be treated as touching
	// (inclusive); otherwise the lambda's param vanishes from completion at the
	// one position where you're most likely to ask for it.
	input := "fn make_adder(n: Int): (Int) -> Int {\n  |x| n + x\n}"
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	// line 2 "  |x| n + x": last token x at col 11, so the trailing edge (one
	// past it) is col 12.
	names := visibleNames(file.ScopeAt(Pos{Line: 2, Col: 12}))
	if !names["x"] {
		t.Errorf("expected lambda param x visible at the trailing edge of its body, got %v", names)
	}
}

func TestScopeAt_GenericFunctionBody(t *testing.T) {
	// A generic fn nests a type-param scope between the module and the body
	// scope. ScopeAt must descend THROUGH the (span-set) type-param scope to
	// reach the body — if that scope were span-less the body would be
	// unreachable. `fn id<T>(x: T): T { x }` — body `x` at col 23.
	input := `fn id<T>(x: T): T { x }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	scope := file.ScopeAt(Pos{Line: 1, Col: 23})
	if scope == file.ModuleScope {
		t.Fatal("expected the generic fn body scope, got the module scope")
	}
	if !visibleNames(scope)["x"] {
		t.Errorf("expected parameter x visible in generic fn body, got %v", visibleNames(scope))
	}
}

func TestScopeAt_TypeBodyFunctionBody(t *testing.T) {
	// Type-body functions build a scope in BOTH the signature pass and the
	// bodies pass. Only the bodies-pass scope is span-set; ScopeAt must pick it
	// so the parameter `g` is visible in the function body. The body `g.v` sits
	// at col 24 on line 4.
	input := "struct P {\n  v: Int\n\n  fn get(g: P): Int { g.v }\n}"
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	names := visibleNames(file.ScopeAt(Pos{Line: 4, Col: 24}))
	if !names["g"] {
		t.Errorf("expected param visible in impl function body, got %v", names)
	}
	if names["self"] {
		t.Errorf("did not expect concrete type-body function to expose self, got %v", names)
	}
}
