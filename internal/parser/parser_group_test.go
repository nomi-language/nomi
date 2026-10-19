package parser

import (
	"errors"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// `group {` at the top level is a group with its name missing. The parse
// reports that at the `{` and refuses the file, and the recovering parses
// still read the body as a group, so its tests are checked and an editor
// works inside it.
func TestParseGroupWithoutAName(t *testing.T) {
	src := "group {\n  test \"a\" {\n    assert 1 == 1\n  }\n}\n"
	const want = "a `group` needs a name; name the group for what its tests share, such as `group \"dates\" { ... }`"

	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("Parse admits a group without a name")
	}
	var pe ParseError
	if !errors.As(err, &pe) || pe.Message != want || pe.Line != 1 || pe.Col != 7 {
		t.Fatalf("Parse error %v, want %q at 1:7", err, want)
	}

	nodes, errs, _ := ParseResilient(lexer.Lex(src))
	if len(errs) != 1 || errs[0].Message != want {
		t.Fatalf("ParseResilient errors %v, want only %q", errs, want)
	}
	if len(nodes) != 1 {
		t.Fatalf("%d top-level nodes, want 1", len(nodes))
	}
	decl, ok := nodes[0].(*ast.TestDecl)
	if !ok || !decl.Group || decl.Name != "" || decl.NameLine != 0 {
		t.Fatalf("expected a nameless group, got %#v", nodes[0])
	}
	if len(decl.Body.Stmts) != 1 {
		t.Fatalf("%d tests, want 1", len(decl.Body.Stmts))
	}
	if child, ok := decl.Body.Stmts[0].(*ast.TestDecl); !ok || child.Name != "a" {
		t.Errorf("child: %#v", decl.Body.Stmts[0])
	}
}

// `group "name" { ... }` at the top level is a test group: a TestDecl with
// Group set, holding its lines and its tests.
func TestParseGroup(t *testing.T) {
	src := "group \"parsing\" {\n  setup 1\n\n  test \"x\", n {\n    assert n == 1\n  }\n}"
	decl, ok := parse(t, src)[0].(*ast.TestDecl)
	if !ok || !decl.Group {
		t.Fatalf("expected a group, got %#v", parse(t, src)[0])
	}
	if decl.Name != "parsing" || decl.Line != 1 || decl.Col != 1 {
		t.Errorf("group %q at %d:%d, want \"parsing\" at 1:1", decl.Name, decl.Line, decl.Col)
	}
	if decl.Setup == nil {
		t.Error("the group's setup is missing")
	}
	if len(decl.Body.Stmts) != 1 {
		t.Fatalf("%d tests, want 1", len(decl.Body.Stmts))
	}
	if child, ok := decl.Body.Stmts[0].(*ast.TestDecl); !ok || child.Group || child.Name != "x" {
		t.Errorf("child: %#v", decl.Body.Stmts[0])
	}
}

// `group` is a keyword only at the top level and only before the group's
// name string or its `{`. Everywhere else it is an identifier: a function, a
// parameter, a field, a binding, a call, a read and an `if` condition named
// `group` keep working. `tests`
// is an ordinary identifier everywhere.
func TestParseGroupIsAContextualKeyword(t *testing.T) {
	src := "struct Row {\n  group: String\n}\n\n" +
		"fn group(group: Int) -> Int {\n  group\n}\n\n" +
		"fn f(row: Row) {\n  group = row.group\n  tests = group\n  g(group, tests)\n  group(2)\n  r = #Row{group: \"a\"}\n  if group {\n    g(1)\n  }\n}\n\n" +
		"group(3)"
	nodes := parse(t, src)
	if len(nodes) != 4 {
		t.Fatalf("%d top-level nodes, want 4", len(nodes))
	}
	if st := nodes[0].(*ast.StructDef); len(st.Fields) != 1 || st.Fields[0].Name != "group" {
		t.Errorf("struct fields: %#v", st.Fields)
	}
	fn := nodes[1].(*ast.FuncDef)
	if fn.Name != "group" || len(fn.Params) != 1 || fn.Params[0].Name != "group" {
		t.Errorf("function %q with params %#v", fn.Name, fn.Params)
	}
	stmts := nodes[2].(*ast.FuncDef).Body.Stmts
	if len(stmts) != 6 {
		t.Fatalf("%d statements, want 6", len(stmts))
	}
	ifStmt := stmts[5]
	if es, ok := ifStmt.(*ast.ExprStmt); ok {
		ifStmt = es.Expr
	}
	if _, ok := ifStmt.(*ast.If); !ok {
		t.Errorf("`if group {`: %#v", stmts[5])
	}
	if b, ok := stmts[0].(*ast.Binding); !ok || b.Name != "group" {
		t.Errorf("binding: %#v", stmts[0])
	}
	if b, ok := stmts[1].(*ast.Binding); !ok || b.Name != "tests" {
		t.Errorf("binding: %#v", stmts[1])
	}
	for i, n := range nodes {
		if _, isTest := n.(*ast.TestDecl); isTest {
			t.Errorf("top-level node %d parsed as a test declaration", i)
		}
	}
}
