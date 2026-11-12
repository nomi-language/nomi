package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A `{` where an expression starts always opens data: an anonymous struct
// or a struct update. Bodies are written below a header and closed by
// `end`, so no brace is a block.
func TestBrace_AlwaysData(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"q = {a: 1}", "struct"},
		{"q = {a:}", "struct"},
		{"q = {a:, b: 2}", "struct"},
		{"q = {\n    a: 1,\n    b: 2,\n}", "struct"},
		{"q = {\n    // first\n    a:,\n}", "struct"},
		{"q = {..p, a: 1}", "struct-update"},
		{"q = {a: f(x: 1), b: 2}", "struct"},
		{"q = {a: x == 1, b: y >= 2}", "struct"},
		{"q = {a: [1, 2], b: 1..=3}", "struct bracket"},
		{"q = {}", "map"},
		{"q = {a: \"x = ${y}\"}", "struct"},
		{"q = {f: if Some(n) = m then n else 0, g: 0}", "struct variant-pattern Some block block"},
	} {
		wantKinds(t, tc.src, tc.want)
	}
	for _, tc := range []struct{ src, want string }{
		{"q = {-a}", "expected field name in struct literal"},
		{"q = {f(a)}", "expected `,` or `}` after the field `f`"},
		{"q = {a: Int = 1\n    a + 1\n}", "expected field name in struct literal"},
	} {
		wantParseError(t, tc.src, tc.want)
	}
}

// Where a block is the only reading, a `{` is a block whatever follows it.
func TestBrace_BodiesAreBlocks(t *testing.T) {
	nodes := parse(t, "fn f(a: Int) -> Int\n    a\nend\n")
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("got %T, want a function", nodes[0])
	}
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("body = %#v, want a block of one statement", fn.Body)
	}
	if es, ok := fn.Body.Stmts[0].(*ast.ExprStmt); !ok || !isIdent(es.Expr) {
		t.Fatalf("body statement = %T, want the name `a`", fn.Body.Stmts[0])
	}
}

// A struct's arguments and patterns pun a field as `name:`, and a lambda,
// an arm and a call argument take a struct as any expression does.
func TestBrace_Puns(t *testing.T) {
	for _, src := range []string{
		"q = P{x:, y:}",
		"q = f({x:, y: 2})",
		"q = Response.json(body, status:)",
		"q = fn a -> {a:}",
		"q = case p\n    P{x:, y: 0} -> x\n    {x:} -> x\nend",
		"{x:, y: b} = p",
		"q = &{x: &1, y: 7}",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse(%q): %v", src, err)
		}
	}
	call := parseExpr(t, "f(status:)").(*ast.Call)
	arg := call.Args[0].(*ast.NamedArg)
	if id, ok := arg.Value.(*ast.Ident); !ok || id.Name != "status" {
		t.Fatalf("`status:` = %#v, want the name `status`", arg.Value)
	}
}

// The forms the brace replaced are errors whose help writes the new one.
func TestBrace_OldFormsAreErrors(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"q = .{a: 1}", "an anonymous struct is written with its brace alone"},
		{"fn f(p: .{a: Int}) -> Int { p.a }", "an anonymous struct is written with its brace alone"},
		{"q = case p\n    .{a:} -> a\nend", "an anonymous struct is written with its brace alone"},
		{"q = P{a}", "a field is written `a: value`, or `a:` for `a: a`"},
		{"q = {a, b: 1}", "a field is written `a: value`, or `a:` for `a: a`"},
		{"q = case p\n    P{a} -> a\nend", "a field is written `a: value`, or `a:` for `a: a`"},
		{"fn f(p: {}) -> Int { 1 }", "an anonymous struct has at least one field"},
	} {
		wantParseError(t, tc.src, tc.want)
	}
	_, err := Parse(lexer.Lex("q = P{a}"))
	pe, ok := err.(ParseError)
	if !ok || len(pe.Hints) != 1 || pe.Hints[0] != "write `P{a:}`" || pe.Fix == nil {
		t.Fatalf("P{a}: %#v, want the help `write P{a:}` and a fix", err)
	}
}

// Every field written as its name alone is reported in one pass: the parse
// reads each as the pun its fix writes and goes on, in a named or
// anonymous literal, a pattern and a destructure, two on one line or
// several in one statement.
func TestBrace_EveryBarePunInOnePass(t *testing.T) {
	src := `fn one(x: Int, y: Int) -> P
    P{x, y}
end

fn two(x: Int, y: Int) -> P
    a = P{
        x,
        y,
    }
    case a
        P{x, y: _} -> P{x: 1, y}
    end
end

fn three(x: Int) -> Int
    q = {x, y: 1}
    {x, y} = q
    x
end
`
	_, errs := ParseAll(lexer.Lex(src))
	type at struct {
		line, col int
		name      string
	}
	want := []at{{2, 7, "x"}, {2, 10, "y"}, {7, 9, "x"}, {8, 9, "y"}, {11, 11, "x"}, {11, 31, "y"}, {16, 10, "x"}, {17, 6, "x"}, {17, 9, "y"}}
	if len(errs) != len(want) {
		t.Fatalf("%d errors, want %d: %v", len(errs), len(want), errs)
	}
	for i, w := range want {
		e := errs[i]
		msg := "a field is written `" + w.name + ": value`, or `" + w.name + ":` for `" + w.name + ": " + w.name + "`"
		if e.Line != w.line || e.Col != w.col || e.Message != msg {
			t.Errorf("error %d: %d:%d %q, want %d:%d %q", i, e.Line, e.Col, e.Message, w.line, w.col, msg)
		}
	}
	// A strict parse stops at the first.
	_, err := Parse(lexer.Lex(src))
	if pe, ok := err.(ParseError); !ok || pe.Line != 2 || pe.Col != 7 {
		t.Fatalf("Parse: %v, want the error at 2:7", err)
	}
}

// A name alone in braces is a struct field written without its colon,
// wherever it stands, and its fix writes the `:`.
func TestBrace_NameAloneIsABarePun(t *testing.T) {
	const want = "a field is written `article: value`, or `article:` for `article: article`"
	for _, src := range []string{
		"r = Response.json({article})",
		"r = f(1, { article })",
		"x = {article}",
		"xs = [{article}]",
		"y = case n\n    _ -> {article}\nend",
		"g = fn -> {article}",
	} {
		_, err := Parse(lexer.Lex(src))
		pe, ok := err.(ParseError)
		if !ok || pe.Message != want {
			t.Errorf("%q: %v, want %q", src, err, want)
			continue
		}
		if pe.Fix == nil || len(pe.Fix.Edits) != 1 || pe.Fix.Edits[0].Text != ":" {
			t.Errorf("%q: fix %#v, want one that writes the `:`", src, pe.Fix)
		}
	}
	for _, src := range []string{
		"fn f(article: Int) -> Int\n    article\nend",
		"fn f(ok: Bool, a: Int, b: Int) -> Int\n    if ok then a else b\nend",
		"x = {article:}",
		"x = begin\n    article\nend",
		"g = fn a, // c\n    b ->\n    a\nend",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("%q: %v, want it to parse", src, err)
		}
	}
}

// isIdent reports whether n is a name.
func isIdent(n ast.Node) bool {
	_, ok := n.(*ast.Ident)
	return ok
}
