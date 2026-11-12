package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A header ends at the end of its line; brackets carry it on, and a line
// that starts with `where` or `|>` continues it. The body runs below to
// `end`.
func TestBody_HeaderEndsAtItsLine(t *testing.T) {
	for _, src := range []string{
		"fn f(\n    a: Int,\n    b: Int,\n) -> Int\n    a + b\nend",
		"fn f<T>(x: T) -> String\n    where T: Display\n    Display.to_string(x)\nend",
		"fn f(xs: List<Int>) -> Int\n    case xs\n        |> Iter.count()\n        0 -> 1\n        _ -> 2\n    end\nend",
		"struct P<T>\n    where T: Equatable\n    x: T\nend",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse(%q): %v", src, err)
		}
	}
	err := parseError(t, "fn f() -> Int 1\nend")
	if !strings.Contains(err.Error(), "line 1, col 15: unexpected `1`") ||
		!strings.Contains(err.Error(), "a function's body starts on the line after its header and ends at `end`") {
		t.Errorf("body on the header's line: %v", err)
	}
}

// `end` on the header's line is the empty body, for every construct that
// takes one.
func TestBody_EmptyBodyIsOneLine(t *testing.T) {
	for _, src := range []string{
		"fn noop() end",
		"struct Empty end",
		"impl Marker for P end",
		"interface Marker end",
		"x = begin end",
		"f = fn -> end",
		"x = case e end",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse(%q): %v", src, err)
		}
	}
	fd := parse(t, "fn noop() end")[0].(*ast.FuncDef)
	if len(fd.Body.Stmts) != 0 || fd.Body.EndLine != 1 || fd.Body.EndCol != 11 {
		t.Errorf("empty body %#v, want no statements and its end at 1:11", fd.Body)
	}
	lam := parseExpr(t, "fn -> end").(*ast.Lambda)
	if len(lam.Body.Stmts) != 0 {
		t.Errorf("`fn -> end` body %#v, want empty", lam.Body)
	}
}

// A body that never reaches its `end` is an error at its opening keyword.
func TestBody_MissingEndIsAnErrorAtTheHeader(t *testing.T) {
	for src, want := range map[string]string{
		"fn f()\n    1\n":                    "line 1, col 1: expected `end` to close a function's body",
		"x = 1\nstruct P\n    a: Int\n":      "line 2, col 8: expected `end` to close the struct",
		"fn f()\n    if c then\n        1\n": "line 2, col 5: expected `end` to close an `if` branch",
		"y = case x\n    _ -> 1\n":           "line 1, col 5: expected `end` to close the `case`",
		"g = fn x ->\n    x\n":               "line 1, col 5: expected `end` to close a lambda's body",
		"z = begin\n    1\n":                 "line 1, col 5: expected `end` to close a `begin` block",
		"test \"t\"\n    assert True\n":      "line 1, col 1: expected `end` to close a test's body",
	} {
		if err := parseError(t, src); !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want %q", src, err, want)
		}
	}
}

// An `if` whose `then` ends its line is the block form: each branch runs
// below to `else` or `end`, and an `else if` chain shares one `end`.
func TestIf_BlockForm(t *testing.T) {
	src := "x = if a then\n    1\nelse if b then\n    y = 2\n    y\nelse\n    3\nend"
	n := parse(t, src)[0].(*ast.Binding).Value.(*ast.If)
	if n.Inline || len(n.Then.Stmts) != 1 {
		t.Fatalf("then %#v, want a block of one statement", n.Then)
	}
	inner, ok := n.Else.(*ast.If)
	if !ok || inner.Inline || len(inner.Then.Stmts) != 2 {
		t.Fatalf("else %#v, want an `else if` of two statements", n.Else)
	}
	if els, ok := inner.Else.(*ast.Block); !ok || len(els.Stmts) != 1 {
		t.Fatalf("final else %#v", inner.Else)
	}
	// One `end` closes the whole chain; a second is the enclosing body's.
	if err := parseError(t, "fn f()\n    if a then\n        1\n    else if b then\n        2\n    end\n    end\nend"); !strings.Contains(err.Error(), "unexpected `end`") {
		t.Errorf("an `end` per rung: %v, want the extra `end` refused", err)
	}
}

// Anything after `then` on its line makes the `if` one inline expression
// with no `end`; its `else` branch extends as far right as an expression
// does, and an inline `else if` chains on the same line.
func TestIf_InlineForm(t *testing.T) {
	n := parseExpr(t, "if a then 1 else 2 |> f").(*ast.If)
	if !n.Inline {
		t.Fatal("want the inline form")
	}
	es := n.Else.(*ast.Block).Stmts[0].(*ast.ExprStmt)
	if pipe, ok := es.Expr.(*ast.Binary); !ok || pipe.Op != "|>" {
		t.Errorf("else branch %#v, want the pipe: it extends right", es.Expr)
	}
	chain := parseExpr(t, "if a then 1 else if b then 2 else 3").(*ast.If)
	if inner, ok := chain.Else.(*ast.If); !ok || !inner.Inline {
		t.Errorf("inline chain %#v", chain.Else)
	}
	guard := parse(t, "fn f(n: Int) -> Int\n    if n < 0 then return 0\n    n\nend")[0].(*ast.FuncDef)
	if _, ok := guard.Body.Stmts[0].(*ast.ExprStmt).Expr.(*ast.If).Then.Stmts[0].(*ast.Return); !ok {
		t.Errorf("guard %#v, want a `return` branch", guard.Body.Stmts[0])
	}
	parens := parseExpr(t, "(if a then 1 else 2) |> f").(*ast.Binary)
	if _, ok := parens.Left.(*ast.GroupedExpr); !ok {
		t.Errorf("parenthesized inline if %#v, want it piped whole", parens.Left)
	}
}

// The two forms do not mix.
func TestIf_MixedFormsAreErrors(t *testing.T) {
	for src, want := range map[string]string{
		"x = if a then 1\nelse 2":                     "unexpected `else`",
		"x = if a then 1 else\n    2":                 "an inline `if`'s `else` is followed by its value on the same line",
		"x = if a then 1 else if b then\n    2\nend":  "an inline `if`'s `else if` is inline too",
		"x = if a then\n    1\nelse 2\nend":           "`else` ends its line in an `if` written over several lines",
		"x = if a then\n    1\nelse if b then 2\nend": "an `else if` in an `if` written over several lines ends its line at `then`",
		"x = if a\n    1\nend":                        "expected `then` after the `if` condition",
	} {
		if err := parseError(t, src); !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want %q", src, err, want)
		}
	}
}

// An arm is `pattern -> expr` on one line, or an `->` that ends its line
// and the lines below up to the next line that starts an arm or `end`. A
// statement line never starts an arm, though it may begin like a pattern.
func TestCase_ArmBodies(t *testing.T) {
	src := "x = case r\n" +
		"    Ok(n) ->\n" +
		"        y = 1\n" +
		"        f(y)\n" +
		"        Some(n)\n" +
		"    Err(e) when e > 0 -> 1\n" +
		"    Err(_) ->\n" +
		"        2\n" +
		"end"
	c := parse(t, src)[0].(*ast.Binding).Value.(*ast.Case)
	if len(c.Branches) != 3 {
		t.Fatalf("%d arms, want 3", len(c.Branches))
	}
	if b, ok := c.Branches[0].Body.(*ast.Block); !ok || len(b.Stmts) != 3 {
		t.Errorf("first arm %#v, want a body of three statements", c.Branches[0].Body)
	}
	if c.Branches[1].Guard == nil {
		t.Error("second arm has no guard")
	}
	if _, ok := c.Branches[2].Body.(*ast.IntLit); !ok {
		t.Errorf("third arm %#v, want the one expression below its `->`", c.Branches[2].Body)
	}
	// Arms stand one per line.
	if err := parseError(t, "x = case r\n    A -> 1, B -> 2\nend"); !strings.Contains(err.Error(), "each arm stands on its own line") {
		t.Errorf("two arms on a line: %v", err)
	}
	// An `->` with nothing below it before the next arm has no body.
	if err := parseError(t, "x = case r\n    A ->\n    B -> 2\nend"); !strings.Contains(err.Error(), "expected the arm's body after `->`") {
		t.Errorf("empty arm: %v", err)
	}
}

// `case` alone on its line takes conditions, and `case e end` has no arms.
func TestCase_AdHocAndEmpty(t *testing.T) {
	c := parseExpr(t, "case\n    x > 0 ->\n        y = x\n        y\n    _ -> 0\nend").(*ast.Case)
	if c.Value != nil || len(c.Branches) != 2 {
		t.Fatalf("ad-hoc case %#v", c)
	}
	if b, ok := c.Branches[0].Body.(*ast.Block); !ok || len(b.Stmts) != 2 {
		t.Errorf("first condition's body %#v", c.Branches[0].Body)
	}
	empty := parseExpr(t, "case e end").(*ast.Case)
	if empty.Value == nil || len(empty.Branches) != 0 {
		t.Errorf("`case e end` = %#v", empty)
	}
}

// A lambda whose `->` ends its line takes the lines below to `end`; one
// with its body on the line takes one expression.
func TestLambda_Bodies(t *testing.T) {
	block := parseExpr(t, "f(fn x ->\n    y = x\n    y\nend)").(*ast.Call).Args[0].(*ast.Lambda)
	if len(block.Body.Stmts) != 2 || block.Body.EndLine != 4 {
		t.Errorf("block body %#v, want two statements ending at line 4", block.Body)
	}
	one := parseExpr(t, "fn x -> x + 1").(*ast.Lambda)
	if len(one.Body.Stmts) != 1 || one.Body.EndLine != 0 {
		t.Errorf("one-line body %#v", one.Body)
	}
	// A parenthesized parameter type before an `->` that ends its line is
	// a type, not a function type taking the body.
	typed := parseExpr(t, "fn p: (Int, Int) ->\n    p.first\nend").(*ast.Lambda)
	if ft, ok := typed.Params[0].TypeAnnotation.(*ast.FuncType); ok && ft.Return != nil {
		t.Errorf("parameter type %#v, want the tuple", typed.Params[0].TypeAnnotation)
	}
}

// `begin` opens a block expression closed by `end`.
func TestBegin_Block(t *testing.T) {
	b := parseExpr(t, "begin\n    x = 1\n    x + 1\nend").(*ast.Block)
	if len(b.Stmts) != 2 || b.Line != 1 || b.Col != 1 {
		t.Errorf("begin block %#v", b)
	}
	if err := parseError(t, "x = begin 1 end"); !strings.Contains(err.Error(), "a `begin` block starts on the line after its header") {
		t.Errorf("one-line begin: %v", err)
	}
}

// A binding's `else` takes its value on its line, or a body or arms below,
// closed by `end`; after a multi-line value it may start the next line.
func TestBindingElse_Forms(t *testing.T) {
	inline := parsePatternBinding(t, "Some(x) = m else return 0")
	if inline.Else.Block == nil || len(inline.Else.Block.Stmts) != 1 {
		t.Errorf("inline else %#v", inline.Else)
	}
	body := parsePatternBinding(t, "Some(x) = m else\n    io.print(\"none\")\n    return 0\nend")
	if body.Else.Block == nil || len(body.Else.Block.Stmts) != 2 {
		t.Errorf("body else %#v", body.Else)
	}
	arms := parsePatternBinding(t, "Ok(x) = r else\n    Err(e) when e > 1 -> return 1\n    Err(_) -> 0\nend")
	if len(arms.Else.Arms) != 2 {
		t.Errorf("arms else %#v", arms.Else)
	}
	next := parsePatternBinding(t, "Some(numbers) =\n    digits\n    |> parse()\nelse\n    return 0\nend")
	if next.Else == nil || next.Else.Line != 4 {
		t.Errorf("else after a multi-line value %#v", next.Else)
	}
}

// In an interface a signature whose next line is another member, a doc
// comment, a `//!` prompt or `end` is required; any other line below it
// starts its default body.
func TestInterface_RequiredAndDefaultFunctions(t *testing.T) {
	src := "interface Shape\n" +
		"    fn area(s: self) -> Float\n" +
		"    /// The doubled area.\n" +
		"    fn twice(s: self) -> Float\n" +
		"        area(s) * 2.0\n" +
		"    end\n" +
		"    open fn name(s: self) -> String\n" +
		"        \"shape\"\n" +
		"    end\n" +
		"    fn noop(s: self) end\n" +
		"    fn last(s: self) -> Int\n" +
		"end"
	def := parse(t, src)[0].(*ast.InterfaceDef)
	want := map[string]bool{"area": false, "twice": true, "name": true, "noop": true, "last": false}
	if len(def.Methods) != len(want) {
		t.Fatalf("%d methods, want %d", len(def.Methods), len(want))
	}
	for _, m := range def.Methods {
		if (m.Body != nil) != want[m.Name] {
			t.Errorf("%s: body %v, want %v", m.Name, m.Body != nil, want[m.Name])
		}
	}
}

// `import` alone on its line opens an import block closed by `end`;
// `concurrent` alone on its line opens its scope; `group` alone on its
// line is a group whose name is missing.
func TestBody_KeywordsAloneOnTheirLine(t *testing.T) {
	nodes := parse(t, "import\n    std::io\n    std::iter.Iter\nend\n\nfn main()\n    x = concurrent\n        1\n    end\nend")
	if blk, ok := nodes[0].(*ast.ImportBlock); !ok || len(blk.Entries) != 2 {
		t.Errorf("import block %#v", nodes[0])
	}
	b := nodes[1].(*ast.FuncDef).Body.Stmts[0].(*ast.Binding)
	if _, ok := b.Value.(*ast.ConcurrentBlock); !ok {
		t.Errorf("concurrent %#v", b.Value)
	}
	_, errs, _ := ParseResilient(lexer.Lex("group\n    test \"a\"\n        assert True\n    end\nend"))
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "a `group` needs a name") {
		t.Errorf("group without a name: %v", errs)
	}
}

// Resilient recovery counts the bodies a damaged statement opens, so the
// `end` that closes one is not taken for the enclosing body's and the
// declarations after it survive.
func TestResilient_RecoveryAcrossEnd(t *testing.T) {
	src := "fn f() -> Int\n" +
		"    x = case 1\n" +
		"        _ -> ]\n" +
		"    end\n" +
		"    y = fn a ->\n" +
		"        a +\n" +
		"    end\n" +
		"    x\n" +
		"end\n\n" +
		"fn g() -> Int\n    1\nend\n"
	nodes, errs, _ := ParseResilient(lexer.Lex(src))
	if len(errs) == 0 {
		t.Fatal("no error reported")
	}
	var names []string
	for _, n := range nodes {
		if fd, ok := n.(*ast.FuncDef); ok {
			names = append(names, fd.Name)
		}
	}
	if strings.Join(names, ",") != "f,g" {
		t.Fatalf("functions %v, want f and g", names)
	}
	last := nodes[0].(*ast.FuncDef).Body.Stmts
	if id, ok := last[len(last)-1].(*ast.ExprStmt); !ok || !isIdent(id.Expr) {
		t.Errorf("f's last statement %#v, want `x`, read after the damage", last[len(last)-1])
	}
}
