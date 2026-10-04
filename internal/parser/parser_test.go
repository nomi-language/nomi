package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
	"reflect"
	"strings"
	"testing"
)

func importNodeNames(nodes []ast.Node) []string {
	names := make([]string, len(nodes))
	for i, node := range nodes {
		names[i] = ast.ImportNodeName(node)
	}
	return names
}

func parse(t *testing.T, source string) []ast.Node {
	t.Helper()
	tokens := lexer.Lex(source)
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", source, err)
	}
	return nodes
}

func parseExpr(t *testing.T, source string) ast.Node {
	t.Helper()
	nodes := parse(t, source)
	if len(nodes) != 1 {
		t.Fatalf("Parse(%q): expected 1 statement, got %d", source, len(nodes))
	}
	stmt, ok := nodes[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("Parse(%q): expected ExprStmt, got %T", source, nodes[0])
	}
	return stmt.Expr
}

func TestIntLiteral(t *testing.T) {
	expr := parseExpr(t, "42")
	lit, ok := expr.(*ast.IntLit)
	if !ok {
		t.Fatalf("expected *ast.IntLit, got %T", expr)
	}
	if lit.Value != 42 {
		t.Errorf("expected 42, got %d", lit.Value)
	}
}

func TestFloatLiteral(t *testing.T) {
	expr := parseExpr(t, "3.14")
	lit, ok := expr.(*ast.FloatLit)
	if !ok {
		t.Fatalf("expected *ast.FloatLit, got %T", expr)
	}
	if lit.Value != 3.14 {
		t.Errorf("expected 3.14, got %f", lit.Value)
	}
}

func TestParseGoSelectorBindings(t *testing.T) {
	nodes := parse(t, `gopkg "example.com/app/ffi" as ffi

opaque type RawBox go ffi.Box

fn echo_upper(s: String): String go ffi.EchoUpper
`)
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}
	pkg, ok := nodes[0].(*ast.ExternPackage)
	if !ok {
		t.Fatalf("expected gopkg declaration to parse as *ast.ExternPackage, got %T", nodes[0])
	}
	if pkg.Alias != "ffi" || pkg.ImportPath != "example.com/app/ffi" {
		t.Fatalf("gopkg declaration: got alias=%q path=%q", pkg.Alias, pkg.ImportPath)
	}
	typ, ok := nodes[1].(*ast.ExternType)
	if !ok {
		t.Fatalf("expected Go selector type to parse as *ast.ExternType, got %T", nodes[1])
	}
	if typ.Name != "RawBox" || typ.ForeignAlias != "ffi" || typ.ForeignName != "Box" {
		t.Fatalf("Go selector type: got name=%q alias=%q name=%q", typ.Name, typ.ForeignAlias, typ.ForeignName)
	}
	fn, ok := nodes[2].(*ast.ExternFunc)
	if !ok {
		t.Fatalf("expected Go selector function to parse as *ast.ExternFunc, got %T", nodes[2])
	}
	if fn.Name != "echo_upper" || fn.ForeignAlias != "ffi" || fn.ForeignName != "EchoUpper" {
		t.Fatalf("Go selector function: got name=%q alias=%q name=%q", fn.Name, fn.ForeignAlias, fn.ForeignName)
	}
}

func TestDecimalLiteral(t *testing.T) {
	for _, src := range []string{"1.50d", "5d", "1_000.00d"} {
		expr := parseExpr(t, src)
		lit, ok := expr.(*ast.DecimalLit)
		if !ok {
			t.Fatalf("%q: expected *ast.DecimalLit, got %T", src, expr)
		}
		if lit.Lexeme != src {
			t.Errorf("%q: expected lexeme %q, got %q", src, src, lit.Lexeme)
		}
	}
}

func TestDecimalInBinary(t *testing.T) {
	// 1.50d + 1.5d parses as Binary{DecimalLit, "+", DecimalLit}.
	expr := parseExpr(t, "1.50d + 1.5d")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "+" {
		t.Errorf("expected op +, got %q", bin.Op)
	}
	l, ok := bin.Left.(*ast.DecimalLit)
	if !ok || l.Lexeme != "1.50d" {
		t.Errorf("expected left DecimalLit{1.50d}, got %T %+v", bin.Left, bin.Left)
	}
	r, ok := bin.Right.(*ast.DecimalLit)
	if !ok || r.Lexeme != "1.5d" {
		t.Errorf("expected right DecimalLit{1.5d}, got %T %+v", bin.Right, bin.Right)
	}
}

func TestDecimalCasePattern(t *testing.T) {
	// A decimal literal must be valid as a case pattern.
	src := "case x {\n  1.50d -> 1\n  _ -> 0\n}"
	expr := parseExpr(t, src)
	ce, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	if len(ce.Branches) == 0 {
		t.Fatalf("expected at least one case branch")
	}
	if _, ok := ce.Branches[0].Pattern.(*ast.DecimalLit); !ok {
		t.Errorf("expected first branch pattern *ast.DecimalLit, got %T", ce.Branches[0].Pattern)
	}
}

func TestStringLiteral(t *testing.T) {
	expr := parseExpr(t, `"hello"`)
	lit, ok := expr.(*ast.StringLit)
	if !ok {
		t.Fatalf("expected *ast.StringLit, got %T", expr)
	}
	if lit.Value != "hello" {
		t.Errorf("expected %q, got %q", "hello", lit.Value)
	}
}

func TestBoolLiteral(t *testing.T) {
	// true is no longer a keyword; it parses as an identifier
	expr := parseExpr(t, "true")
	ident, ok := expr.(*ast.Ident)
	if !ok {
		t.Fatalf("expected *ast.Ident, got %T", expr)
	}
	if ident.Name != "true" {
		t.Errorf("expected 'true', got %q", ident.Name)
	}
}

func TestQualifiedTypeDeclarationAndDestructure(t *testing.T) {
	nodes := parse(t, `type Day Int
type Day.Hours Int

fn value(hours: Day.Hours): Int {
  Day.Hours(n) = hours
  n
}
`)
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}
	td, ok := nodes[1].(*ast.TypeDef)
	if !ok {
		t.Fatalf("expected second node TypeDef, got %T", nodes[1])
	}
	if td.Name != "Day.Hours" {
		t.Fatalf("expected qualified type name Day.Hours, got %q", td.Name)
	}
	fn, ok := nodes[2].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected third node FuncDef, got %T", nodes[2])
	}
	qt, ok := fn.Params[0].TypeAnnotation.(*ast.QualifiedType)
	if !ok {
		t.Fatalf("expected qualified parameter type, got %T", fn.Params[0].TypeAnnotation)
	}
	if qt.TypeString() != "Day.Hours" {
		t.Fatalf("expected parameter type Day.Hours, got %q", qt.TypeString())
	}
	dd, ok := fn.Body.Stmts[0].(*ast.DistinctDestructure)
	if !ok {
		t.Fatalf("expected qualified distinct destructure, got %T", fn.Body.Stmts[0])
	}
	if dd.TypeName != "Day.Hours" {
		t.Fatalf("expected destructure type Day.Hours, got %q", dd.TypeName)
	}
	if dd.TypeNameExpr == nil || dd.TypeNameExpr.TypeString() != "Day.Hours" {
		t.Fatalf("expected destructure TypeNameExpr Day.Hours, got %#v", dd.TypeNameExpr)
	}
}

func TestBinaryAdd(t *testing.T) {
	// 1 + 2 → Binary{IntLit{1}, "+", IntLit{2}}
	expr := parseExpr(t, "1 + 2")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "+" {
		t.Errorf("expected op +, got %s", bin.Op)
	}
	left, ok := bin.Left.(*ast.IntLit)
	if !ok {
		t.Fatalf("expected left *ast.IntLit, got %T", bin.Left)
	}
	if left.Value != 1 {
		t.Errorf("expected left 1, got %d", left.Value)
	}
	right, ok := bin.Right.(*ast.IntLit)
	if !ok {
		t.Fatalf("expected right *ast.IntLit, got %T", bin.Right)
	}
	if right.Value != 2 {
		t.Errorf("expected right 2, got %d", right.Value)
	}
}

func TestPrecedenceMulOverAdd(t *testing.T) {
	// 1 + 2 * 3 → Binary{IntLit{1}, "+", Binary{IntLit{2}, "*", IntLit{3}}}
	expr := parseExpr(t, "1 + 2 * 3")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "+" {
		t.Errorf("expected top op +, got %s", bin.Op)
	}
	left, ok := bin.Left.(*ast.IntLit)
	if !ok {
		t.Fatalf("expected left *ast.IntLit, got %T", bin.Left)
	}
	if left.Value != 1 {
		t.Errorf("expected left 1, got %d", left.Value)
	}
	right, ok := bin.Right.(*ast.Binary)
	if !ok {
		t.Fatalf("expected right *ast.Binary, got %T", bin.Right)
	}
	if right.Op != "*" {
		t.Errorf("expected right op *, got %s", right.Op)
	}
	rl, ok := right.Left.(*ast.IntLit)
	if !ok || rl.Value != 2 {
		t.Errorf("expected right.left 2, got %v", right.Left)
	}
	rr, ok := right.Right.(*ast.IntLit)
	if !ok || rr.Value != 3 {
		t.Errorf("expected right.right 3, got %v", right.Right)
	}
}

func TestGrouping(t *testing.T) {
	// (1 + 2) * 3 keeps the explicit grouping around the left binary.
	expr := parseExpr(t, "(1 + 2) * 3")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "*" {
		t.Errorf("expected top op *, got %s", bin.Op)
	}
	group, ok := bin.Left.(*ast.GroupedExpr)
	if !ok {
		t.Fatalf("expected left *ast.GroupedExpr, got %T", bin.Left)
	}
	left, ok := group.Expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected grouped left *ast.Binary, got %T", group.Expr)
	}
	if left.Op != "+" {
		t.Errorf("expected left op +, got %s", left.Op)
	}
	right, ok := bin.Right.(*ast.IntLit)
	if !ok || right.Value != 3 {
		t.Fatalf("expected right IntLit{3}, got %T %v", bin.Right, bin.Right)
	}
}

func TestUnaryBang(t *testing.T) {
	// !true → Unary{"!", Ident{true}}
	expr := parseExpr(t, "!true")
	un, ok := expr.(*ast.Unary)
	if !ok {
		t.Fatalf("expected *ast.Unary, got %T", expr)
	}
	if un.Op != "!" {
		t.Errorf("expected op !, got %s", un.Op)
	}
	ident, ok := un.Right.(*ast.Ident)
	if !ok || ident.Name != "true" {
		t.Errorf("expected Ident{true}, got %T %v", un.Right, un.Right)
	}
}

func TestStringConcat(t *testing.T) {
	// "a" + "b" -> Binary{StringLit{"a"}, "+", StringLit{"b"}}
	expr := parseExpr(t, `"a" + "b"`)
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "+" {
		t.Errorf("expected op +, got %s", bin.Op)
	}
	left, ok := bin.Left.(*ast.StringLit)
	if !ok || left.Value != "a" {
		t.Errorf("expected left StringLit{a}, got %T %v", bin.Left, bin.Left)
	}
	right, ok := bin.Right.(*ast.StringLit)
	if !ok || right.Value != "b" {
		t.Errorf("expected right StringLit{b}, got %T %v", bin.Right, bin.Right)
	}
}

func TestStringPrefixPattern(t *testing.T) {
	expr := parseExpr(t, `case path {
  "/users/" + id -> id
  _ -> ""
}`)
	ce, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	if len(ce.Branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(ce.Branches))
	}
	bin, ok := ce.Branches[0].Pattern.(*ast.Binary)
	if !ok {
		t.Fatalf("expected string prefix pattern *ast.Binary, got %T", ce.Branches[0].Pattern)
	}
	if bin.Op != "+" {
		t.Fatalf("expected prefix pattern op +, got %q", bin.Op)
	}
	left, ok := bin.Left.(*ast.StringLit)
	if !ok || left.Value != "/users/" {
		t.Fatalf("expected left string prefix, got %T %#v", bin.Left, bin.Left)
	}
	right, ok := bin.Right.(*ast.IdentPattern)
	if !ok || right.Name != "id" {
		t.Fatalf("expected right id binding, got %T %#v", bin.Right, bin.Right)
	}
}

func TestLogicalWithComparison(t *testing.T) {
	// 1 > 2 and 3 < 4 → Binary{Binary{1, ">", 2}, "and", Binary{3, "<", 4}}
	expr := parseExpr(t, "1 > 2 and 3 < 4")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "and" {
		t.Errorf("expected top op and, got %s", bin.Op)
	}
	left, ok := bin.Left.(*ast.Binary)
	if !ok {
		t.Fatalf("expected left *ast.Binary, got %T", bin.Left)
	}
	if left.Op != ">" {
		t.Errorf("expected left op >, got %s", left.Op)
	}
	right, ok := bin.Right.(*ast.Binary)
	if !ok {
		t.Fatalf("expected right *ast.Binary, got %T", bin.Right)
	}
	if right.Op != "<" {
		t.Errorf("expected right op <, got %s", right.Op)
	}
}

func TestMultipleStatements(t *testing.T) {
	nodes := parse(t, "1\n2\n3")
	if len(nodes) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(nodes))
	}
	for i, n := range nodes {
		stmt, ok := n.(*ast.ExprStmt)
		if !ok {
			t.Fatalf("stmt[%d]: expected ExprStmt, got %T", i, n)
		}
		lit, ok := stmt.Expr.(*ast.IntLit)
		if !ok {
			t.Fatalf("stmt[%d]: expected IntLit, got %T", i, stmt.Expr)
		}
		if lit.Value != int64(i+1) {
			t.Errorf("stmt[%d]: expected %d, got %d", i, i+1, lit.Value)
		}
	}
}

func TestEmptyInput(t *testing.T) {
	nodes := parse(t, "")
	if len(nodes) != 0 {
		t.Fatalf("expected 0 statements, got %d", len(nodes))
	}
}

func TestBoolFalse(t *testing.T) {
	// false is no longer a keyword; it parses as an identifier
	expr := parseExpr(t, "false")
	ident, ok := expr.(*ast.Ident)
	if !ok {
		t.Fatalf("expected *ast.Ident, got %T", expr)
	}
	if ident.Name != "false" {
		t.Errorf("expected 'false', got %q", ident.Name)
	}
}

func TestSubtraction(t *testing.T) {
	expr := parseExpr(t, "5 - 3")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "-" {
		t.Errorf("expected op -, got %s", bin.Op)
	}
}

func TestDivisionAndModulo(t *testing.T) {
	// 10 / 3 % 2 → Binary{Binary{10, "/", 3}, "%", 2} (left-assoc same prec)
	expr := parseExpr(t, "10 / 3 % 2")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "%" {
		t.Errorf("expected top op %%, got %s", bin.Op)
	}
	left, ok := bin.Left.(*ast.Binary)
	if !ok {
		t.Fatalf("expected left *ast.Binary, got %T", bin.Left)
	}
	if left.Op != "/" {
		t.Errorf("expected left op /, got %s", left.Op)
	}
}

func TestEqualityOperators(t *testing.T) {
	expr := parseExpr(t, "1 == 2")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "==" {
		t.Errorf("expected op ==, got %s", bin.Op)
	}

	expr2 := parseExpr(t, "1 != 2")
	bin2, ok := expr2.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr2)
	}
	if bin2.Op != "!=" {
		t.Errorf("expected op !=, got %s", bin2.Op)
	}
}

func TestOrPrecedence(t *testing.T) {
	// true or false and true → Binary{true, "or", Binary{false, "and", true}}
	expr := parseExpr(t, "true or false and true")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "or" {
		t.Errorf("expected top op or, got %s", bin.Op)
	}
	right, ok := bin.Right.(*ast.Binary)
	if !ok {
		t.Fatalf("expected right *ast.Binary, got %T", bin.Right)
	}
	if right.Op != "and" {
		t.Errorf("expected right op and, got %s", right.Op)
	}
}

func TestNestedGrouping(t *testing.T) {
	expr := parseExpr(t, "((42))")
	outer, ok := expr.(*ast.GroupedExpr)
	if !ok {
		t.Fatalf("expected outer *ast.GroupedExpr, got %T", expr)
	}
	inner, ok := outer.Expr.(*ast.GroupedExpr)
	if !ok {
		t.Fatalf("expected inner *ast.GroupedExpr, got %T", outer.Expr)
	}
	lit, ok := inner.Expr.(*ast.IntLit)
	if !ok {
		t.Fatalf("expected *ast.IntLit, got %T", inner.Expr)
	}
	if lit.Value != 42 {
		t.Errorf("expected 42, got %d", lit.Value)
	}
}

func TestUnaryBangPrecedence(t *testing.T) {
	// !true and false → Binary{Unary{!, true}, "and", false}
	expr := parseExpr(t, "!true and false")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "and" {
		t.Errorf("expected top op and, got %s", bin.Op)
	}
	left, ok := bin.Left.(*ast.Unary)
	if !ok {
		t.Fatalf("expected left *ast.Unary, got %T", bin.Left)
	}
	if left.Op != "!" {
		t.Errorf("expected left op !, got %s", left.Op)
	}
}

func TestParseError(t *testing.T) {
	tokens := lexer.Lex("+ 1")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected parse error for '+ 1'")
	}
}

func TestUnmatchedParen(t *testing.T) {
	tokens := lexer.Lex("(1 + 2")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected parse error for '(1 + 2'")
	}
}

// Spec §7 (line 501) and §15 (line 1757): "No single-element tuples —
// `(x)` is just grouping." The trailing-comma form `(x,)` was silently
// constructing a 1-tuple value at runtime even though the type form
// `(T,)` doesn't parse — value-side acceptance was a bug.
func TestParseRejectsSingleElementTuple(t *testing.T) {
	tokens := lexer.Lex("(42,)")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected parse error for single-element tuple '(42,)'")
	}
}

func TestComparisonOperators(t *testing.T) {
	for _, op := range []string{"<=", ">="} {
		expr := parseExpr(t, "1 "+op+" 2")
		bin, ok := expr.(*ast.Binary)
		if !ok {
			t.Fatalf("%s: expected *ast.Binary, got %T", op, expr)
		}
		if bin.Op != op {
			t.Errorf("expected op %s, got %s", op, bin.Op)
		}
	}
}

// --- Bindings, Identifiers, Blocks, If/Else ---

func TestBinding(t *testing.T) {
	nodes := parse(t, "x = 5")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	b, ok := nodes[0].(*ast.Binding)
	if !ok {
		t.Fatalf("expected *ast.Binding, got %T", nodes[0])
	}
	if b.Name != "x" {
		t.Errorf("expected name x, got %s", b.Name)
	}
	lit, ok := b.Value.(*ast.IntLit)
	if !ok {
		t.Fatalf("expected *ast.IntLit, got %T", b.Value)
	}
	if lit.Value != 5 {
		t.Errorf("expected 5, got %d", lit.Value)
	}
}

func TestDiscardBinding(t *testing.T) {
	nodes := parse(t, "_ = 5")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	b, ok := nodes[0].(*ast.Binding)
	if !ok {
		t.Fatalf("expected *ast.Binding, got %T", nodes[0])
	}
	if b.Name != "_" {
		t.Errorf("expected name _, got %s", b.Name)
	}
	if _, ok := b.Value.(*ast.IntLit); !ok {
		t.Fatalf("expected *ast.IntLit, got %T", b.Value)
	}
}

func TestBinding_WithTypeAnnotation(t *testing.T) {
	src := `x: Int = 5`
	tokens := lexer.Lex(src)
	nodes, errs := ParseWithRecovery(tokens)
	if len(errs) != 0 {
		t.Fatalf("parse: %v", errs)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	b, ok := nodes[0].(*ast.Binding)
	if !ok {
		t.Fatalf("expected *ast.Binding, got %T", nodes[0])
	}
	if b.Name != "x" {
		t.Errorf("name: want x, got %q", b.Name)
	}
	if b.TypeAnnotation == nil {
		t.Fatal("TypeAnnotation should be set")
	}
	st, ok := b.TypeAnnotation.(*ast.SimpleType)
	if !ok {
		t.Fatalf("TypeAnnotation: want *SimpleType, got %T", b.TypeAnnotation)
	}
	if st.Name != "Int" {
		t.Errorf("type name: want Int, got %q", st.Name)
	}
}

func TestBinding_WithGenericTypeAnnotation(t *testing.T) {
	src := `m: Map<String, Int> = Map.empty()`
	tokens := lexer.Lex(src)
	nodes, errs := ParseWithRecovery(tokens)
	if len(errs) != 0 {
		t.Fatalf("parse: %v", errs)
	}
	b, ok := nodes[0].(*ast.Binding)
	if !ok {
		t.Fatalf("want *ast.Binding, got %T", nodes[0])
	}
	if b.TypeAnnotation == nil {
		t.Fatal("TypeAnnotation should be set")
	}
	gt, ok := b.TypeAnnotation.(*ast.GenericType)
	if !ok {
		t.Fatalf("want *GenericType, got %T", b.TypeAnnotation)
	}
	if gt.Name != "Map" || len(gt.Params) != 2 {
		t.Errorf("expected Map<_, _>, got %s with %d params", gt.Name, len(gt.Params))
	}
}

func TestIdent(t *testing.T) {
	expr := parseExpr(t, "x")
	id, ok := expr.(*ast.Ident)
	if !ok {
		t.Fatalf("expected *ast.Ident, got %T", expr)
	}
	if id.Name != "x" {
		t.Errorf("expected x, got %s", id.Name)
	}
}

func TestTypeIdent(t *testing.T) {
	expr := parseExpr(t, "Io")
	id, ok := expr.(*ast.TypeIdent)
	if !ok {
		t.Fatalf("expected *ast.TypeIdent, got %T", expr)
	}
	if id.Name != "Io" {
		t.Errorf("expected Io, got %s", id.Name)
	}
}

func TestIfElse(t *testing.T) {
	expr := parseExpr(t, "if flag { 1 } else { 2 }")
	ifNode, ok := expr.(*ast.If)
	if !ok {
		t.Fatalf("expected *ast.If, got %T", expr)
	}
	cond, ok := ifNode.Cond.(*ast.Ident)
	if !ok || cond.Name != "flag" {
		t.Fatalf("expected cond Ident{flag}, got %T %v", ifNode.Cond, ifNode.Cond)
	}
	if len(ifNode.Then.Stmts) != 1 {
		t.Fatalf("expected 1 then stmt, got %d", len(ifNode.Then.Stmts))
	}
	thenStmt, ok := ifNode.Then.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt in then, got %T", ifNode.Then.Stmts[0])
	}
	thenLit, ok := thenStmt.Expr.(*ast.IntLit)
	if !ok || thenLit.Value != 1 {
		t.Fatalf("expected IntLit{1} in then, got %T %v", thenStmt.Expr, thenStmt.Expr)
	}
	elseBlock, ok := ifNode.Else.(*ast.Block)
	if !ok {
		t.Fatalf("expected else *ast.Block, got %T", ifNode.Else)
	}
	if len(elseBlock.Stmts) != 1 {
		t.Fatalf("expected 1 else stmt, got %d", len(elseBlock.Stmts))
	}
	elseStmt, ok := elseBlock.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt in else, got %T", elseBlock.Stmts[0])
	}
	elseLit, ok := elseStmt.Expr.(*ast.IntLit)
	if !ok || elseLit.Value != 2 {
		t.Fatalf("expected IntLit{2} in else, got %T %v", elseStmt.Expr, elseStmt.Expr)
	}
}

func TestIfNoElse(t *testing.T) {
	expr := parseExpr(t, "if x > 0 { \"yes\" }")
	ifNode, ok := expr.(*ast.If)
	if !ok {
		t.Fatalf("expected *ast.If, got %T", expr)
	}
	// Condition should be Binary{Ident{x}, ">", IntLit{0}}
	bin, ok := ifNode.Cond.(*ast.Binary)
	if !ok {
		t.Fatalf("expected cond *ast.Binary, got %T", ifNode.Cond)
	}
	if bin.Op != ">" {
		t.Errorf("expected op >, got %s", bin.Op)
	}
	if len(ifNode.Then.Stmts) != 1 {
		t.Fatalf("expected 1 then stmt, got %d", len(ifNode.Then.Stmts))
	}
	if ifNode.Else != nil {
		t.Fatalf("expected nil else, got %T", ifNode.Else)
	}
}

func TestIfPatternCondition(t *testing.T) {
	expr := parseExpr(t, `if Some(name) = maybe_name { name } else { "none" }`)
	ifNode, ok := expr.(*ast.If)
	if !ok {
		t.Fatalf("expected *ast.If, got %T", expr)
	}
	pat, ok := ifNode.CondPattern.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("expected condition pattern *ast.EnumPattern, got %T", ifNode.CondPattern)
	}
	if pat.Binding != "name" {
		t.Fatalf("expected enum pattern binding name, got %q", pat.Binding)
	}
	cond, ok := ifNode.Cond.(*ast.Ident)
	if !ok || cond.Name != "maybe_name" {
		t.Fatalf("expected cond Ident{maybe_name}, got %T %v", ifNode.Cond, ifNode.Cond)
	}
	if _, ok := ifNode.Else.(*ast.Block); !ok {
		t.Fatalf("expected else *ast.Block, got %T", ifNode.Else)
	}
}

func TestIfTypeIdentCondition(t *testing.T) {
	// Regression: if True { ... } was parsed as struct literal True{...}
	expr := parseExpr(t, "if True { 42 }")
	ifNode, ok := expr.(*ast.If)
	if !ok {
		t.Fatalf("expected *ast.If, got %T", expr)
	}
	cond, ok := ifNode.Cond.(*ast.TypeIdent)
	if !ok {
		t.Fatalf("expected cond *ast.TypeIdent, got %T", ifNode.Cond)
	}
	if cond.Name != "True" {
		t.Errorf("expected cond name True, got %s", cond.Name)
	}
	if len(ifNode.Then.Stmts) != 1 {
		t.Fatalf("expected 1 then stmt, got %d", len(ifNode.Then.Stmts))
	}
}

func TestIfElseIfElse(t *testing.T) {
	expr := parseExpr(t, "if a { 1 } else if b { 2 } else { 3 }")
	ifNode, ok := expr.(*ast.If)
	if !ok {
		t.Fatalf("expected *ast.If, got %T", expr)
	}
	// else branch should be another If
	elseIf, ok := ifNode.Else.(*ast.If)
	if !ok {
		t.Fatalf("expected else *ast.If, got %T", ifNode.Else)
	}
	// else-if's else should be a Block
	elseBlock, ok := elseIf.Else.(*ast.Block)
	if !ok {
		t.Fatalf("expected else-if else *ast.Block, got %T", elseIf.Else)
	}
	if len(elseBlock.Stmts) != 1 {
		t.Fatalf("expected 1 stmt in final else, got %d", len(elseBlock.Stmts))
	}
}

func TestIfElseOnNewLine(t *testing.T) {
	expr := parseExpr(t, "if a {\n  1\n}\nelse {\n  2\n}")
	ifNode, ok := expr.(*ast.If)
	if !ok {
		t.Fatalf("expected *ast.If, got %T", expr)
	}
	if _, ok := ifNode.Else.(*ast.Block); !ok {
		t.Fatalf("expected else *ast.Block, got %T", ifNode.Else)
	}
}

func TestIfElseIfOnNewLine(t *testing.T) {
	src := "if a {\n  1\n}\nelse if b {\n  2\n}\nelse {\n  3\n}"
	expr := parseExpr(t, src)
	ifNode, ok := expr.(*ast.If)
	if !ok {
		t.Fatalf("expected *ast.If, got %T", expr)
	}
	elseIf, ok := ifNode.Else.(*ast.If)
	if !ok {
		t.Fatalf("expected else *ast.If, got %T", ifNode.Else)
	}
	if _, ok := elseIf.Else.(*ast.Block); !ok {
		t.Fatalf("expected final else *ast.Block, got %T", elseIf.Else)
	}
}

func TestMultiStatementWithBinding(t *testing.T) {
	nodes := parse(t, "x = 5\nx + 1")
	if len(nodes) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(nodes))
	}
	b, ok := nodes[0].(*ast.Binding)
	if !ok {
		t.Fatalf("stmt[0]: expected *ast.Binding, got %T", nodes[0])
	}
	if b.Name != "x" {
		t.Errorf("expected binding name x, got %s", b.Name)
	}
	stmt, ok := nodes[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt[1]: expected *ast.ExprStmt, got %T", nodes[1])
	}
	bin, ok := stmt.Expr.(*ast.Binary)
	if !ok {
		t.Fatalf("stmt[1]: expected *ast.Binary, got %T", stmt.Expr)
	}
	if bin.Op != "+" {
		t.Errorf("expected op +, got %s", bin.Op)
	}
}

// --- String Interpolation ---

func TestStringInterpSimple(t *testing.T) {
	// "hello ${name}" → StringInterp{[StringText{"hello "}, StringExpr{Ident{"name"}}]}
	expr := parseExpr(t, `"hello ${name}"`)
	interp, ok := expr.(*ast.StringInterp)
	if !ok {
		t.Fatalf("expected *ast.StringInterp, got %T", expr)
	}
	if len(interp.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(interp.Parts))
	}
	text, ok := interp.Parts[0].(ast.StringText)
	if !ok || text.Value != "hello " {
		t.Errorf("expected StringText{\"hello \"}, got %T %v", interp.Parts[0], interp.Parts[0])
	}
	se, ok := interp.Parts[1].(ast.StringExpr)
	if !ok {
		t.Fatalf("expected StringExpr, got %T", interp.Parts[1])
	}
	ident, ok := se.Expr.(*ast.Ident)
	if !ok || ident.Name != "name" {
		t.Errorf("expected Ident{name}, got %T %v", se.Expr, se.Expr)
	}
}

func TestStringInterpWithExpr(t *testing.T) {
	// "a ${x + 1} b" → StringInterp{[StringText{"a "}, StringExpr{Binary{...}}, StringText{" b"}]}
	expr := parseExpr(t, `"a ${x + 1} b"`)
	interp, ok := expr.(*ast.StringInterp)
	if !ok {
		t.Fatalf("expected *ast.StringInterp, got %T", expr)
	}
	if len(interp.Parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(interp.Parts))
	}
	text1, ok := interp.Parts[0].(ast.StringText)
	if !ok || text1.Value != "a " {
		t.Errorf("expected StringText{\"a \"}, got %T %v", interp.Parts[0], interp.Parts[0])
	}
	se, ok := interp.Parts[1].(ast.StringExpr)
	if !ok {
		t.Fatalf("expected StringExpr, got %T", interp.Parts[1])
	}
	bin, ok := se.Expr.(*ast.Binary)
	if !ok || bin.Op != "+" {
		t.Errorf("expected Binary with op +, got %T %v", se.Expr, se.Expr)
	}
	text2, ok := interp.Parts[2].(ast.StringText)
	if !ok || text2.Value != " b" {
		t.Errorf("expected StringText{\" b\"}, got %T %v", interp.Parts[2], interp.Parts[2])
	}
}

// --- Calls ---

func TestCallWithFieldAccess(t *testing.T) {
	// Io.print("hello") → Call{FieldAccess{TypeIdent{"Io"}, "print"}, [StringLit{"hello"}]}
	expr := parseExpr(t, `Io.print("hello")`)
	call, ok := expr.(*ast.Call)
	if !ok {
		t.Fatalf("expected *ast.Call, got %T", expr)
	}
	fa, ok := call.Func.(*ast.FieldAccess)
	if !ok {
		t.Fatalf("expected *ast.FieldAccess, got %T", call.Func)
	}
	ti, ok := fa.Object.(*ast.TypeIdent)
	if !ok || ti.Name != "Io" {
		t.Errorf("expected TypeIdent{Io}, got %T %v", fa.Object, fa.Object)
	}
	if fa.Field.Name != "print" {
		t.Errorf("expected field print, got %s", fa.Field.Name)
	}
	if len(call.Args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(call.Args))
	}
	sl, ok := call.Args[0].(*ast.StringLit)
	if !ok || sl.Value != "hello" {
		t.Errorf("expected StringLit{hello}, got %T %v", call.Args[0], call.Args[0])
	}
}

func TestCallMultipleArgs(t *testing.T) {
	// add(1, 2) → Call{Ident{"add"}, [IntLit{1}, IntLit{2}]}
	expr := parseExpr(t, "add(1, 2)")
	call, ok := expr.(*ast.Call)
	if !ok {
		t.Fatalf("expected *ast.Call, got %T", expr)
	}
	id, ok := call.Func.(*ast.Ident)
	if !ok || id.Name != "add" {
		t.Errorf("expected Ident{add}, got %T %v", call.Func, call.Func)
	}
	if len(call.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(call.Args))
	}
	a1, ok := call.Args[0].(*ast.IntLit)
	if !ok || a1.Value != 1 {
		t.Errorf("expected IntLit{1}, got %T %v", call.Args[0], call.Args[0])
	}
	a2, ok := call.Args[1].(*ast.IntLit)
	if !ok || a2.Value != 2 {
		t.Errorf("expected IntLit{2}, got %T %v", call.Args[1], call.Args[1])
	}
}

func TestCallNoArgs(t *testing.T) {
	// foo() → Call{Ident{"foo"}, []}
	expr := parseExpr(t, "foo()")
	call, ok := expr.(*ast.Call)
	if !ok {
		t.Fatalf("expected *ast.Call, got %T", expr)
	}
	id, ok := call.Func.(*ast.Ident)
	if !ok || id.Name != "foo" {
		t.Errorf("expected Ident{foo}, got %T %v", call.Func, call.Func)
	}
	if len(call.Args) != 0 {
		t.Fatalf("expected 0 args, got %d", len(call.Args))
	}
}

// --- Field Access ---

func TestChainedFieldAccess(t *testing.T) {
	// a.b.c → FieldAccess{FieldAccess{Ident{"a"}, "b"}, "c"}
	expr := parseExpr(t, "a.b.c")
	fa1, ok := expr.(*ast.FieldAccess)
	if !ok {
		t.Fatalf("expected *ast.FieldAccess, got %T", expr)
	}
	if fa1.Field.Name != "c" {
		t.Errorf("expected outer field c, got %s", fa1.Field.Name)
	}
	fa2, ok := fa1.Object.(*ast.FieldAccess)
	if !ok {
		t.Fatalf("expected inner *ast.FieldAccess, got %T", fa1.Object)
	}
	if fa2.Field.Name != "b" {
		t.Errorf("expected inner field b, got %s", fa2.Field.Name)
	}
	id, ok := fa2.Object.(*ast.Ident)
	if !ok || id.Name != "a" {
		t.Errorf("expected Ident{a}, got %T %v", fa2.Object, fa2.Object)
	}
}

// --- Function Definitions ---

func TestParseFuncDef(t *testing.T) {
	nodes := parse(t, "fn add(x, y) { x + y }")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected *ast.FuncDef, got %T", nodes[0])
	}
	if fd.Name != "add" {
		t.Errorf("expected name add, got %s", fd.Name)
	}
	if fd.Public {
		t.Error("expected private function (snake_case name)")
	}
	if len(fd.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(fd.Params))
	}
	if fd.Params[0].Name != "x" {
		t.Errorf("expected param[0] x, got %s", fd.Params[0].Name)
	}
	if fd.Params[1].Name != "y" {
		t.Errorf("expected param[1] y, got %s", fd.Params[1].Name)
	}
	if fd.ReturnTypeExpr != nil {
		t.Errorf("expected no return type, got %s", fd.ReturnTypeExpr.TypeString())
	}
	if len(fd.Body.Stmts) != 1 {
		t.Fatalf("expected 1 body stmt, got %d", len(fd.Body.Stmts))
	}
	stmt, ok := fd.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt in body, got %T", fd.Body.Stmts[0])
	}
	bin, ok := stmt.Expr.(*ast.Binary)
	if !ok || bin.Op != "+" {
		t.Errorf("expected Binary with op +, got %T %v", stmt.Expr, stmt.Expr)
	}
}

func TestParseFuncDefTyped(t *testing.T) {
	nodes := parse(t, "fn add(x: Int, y: Int): Int { x + y }")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected *ast.FuncDef, got %T", nodes[0])
	}
	if fd.Params[0].TypeAnnotation == nil || fd.Params[0].TypeAnnotation.TypeString() != "Int" {
		t.Errorf("expected param[0] type Int, got %v", fd.Params[0].TypeAnnotation)
	}
	if fd.Params[1].TypeAnnotation == nil || fd.Params[1].TypeAnnotation.TypeString() != "Int" {
		t.Errorf("expected param[1] type Int, got %v", fd.Params[1].TypeAnnotation)
	}
	if fd.ReturnTypeExpr == nil || fd.ReturnTypeExpr.TypeString() != "Int" {
		t.Errorf("expected return type Int, got %v", fd.ReturnTypeExpr)
	}
}

func TestParseFuncDefDefault(t *testing.T) {
	nodes := parse(t, `fn greet(name, greeting = "Hello") { greeting + " " + name }`)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected *ast.FuncDef, got %T", nodes[0])
	}
	if len(fd.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(fd.Params))
	}
	if fd.Params[0].Default != nil {
		t.Errorf("expected param[0] no default, got %v", fd.Params[0].Default)
	}
	if fd.Params[1].Default == nil {
		t.Fatal("expected param[1] to have a default")
	}
	sl, ok := fd.Params[1].Default.(*ast.StringLit)
	if !ok || sl.Value != "Hello" {
		t.Errorf("expected default StringLit{Hello}, got %T %v", fd.Params[1].Default, fd.Params[1].Default)
	}
}

func TestParseFuncDefPublic(t *testing.T) {
	nodes := parse(t, "pub fn add(x, y) { x + y }")
	var fd *ast.FuncDef
	for _, n := range nodes {
		if v, ok := n.(*ast.FuncDef); ok {
			fd = v
			break
		}
	}
	if fd == nil {
		t.Fatal("expected *ast.FuncDef among nodes")
	}
	if !fd.Public {
		t.Error("expected pub fn to be public")
	}
	if fd.Name != "add" {
		t.Errorf("expected name add, got %s", fd.Name)
	}
}

func TestParsePubFn(t *testing.T) {
	nodes := parse(t, "pub fn foo(x: Int): Int { x + 1 }")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected *ast.FuncDef, got %T", nodes[0])
	}
	if !fd.Public {
		t.Error("expected pub fn to set Public=true")
	}
	if fd.Name != "foo" {
		t.Errorf("expected name foo, got %s", fd.Name)
	}
}

func TestParseFnNotPubByDefault(t *testing.T) {
	nodes := parse(t, "fn foo(x: Int): Int { x + 1 }")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected *ast.FuncDef, got %T", nodes[0])
	}
	if fd.Public {
		t.Error("fn without pub should default to Public=false")
	}
}

func TestParsePubBeforeUnsupportedTokenErrors(t *testing.T) {
	// All declaration kinds that legitimately take `pub` are wired
	// (fn / type / opaque type / interface / typealias / once / extern).
	// The inline-pub dispatcher must still reject leading-pub on
	// non-declaration tokens — pick an integer literal as a representative
	// case that is never going to follow `pub`.
	tokens := lexer.Lex("pub 42")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected error for 'pub 42' (not a declaration)")
	}
	if !strings.Contains(err.Error(), "pub") {
		t.Errorf("expected error mentioning 'pub', got: %v", err)
	}
}

func TestParsePubTypeStruct(t *testing.T) {
	nodes := parse(t, "pub struct Foo { name: String; age: Int }")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("expected *ast.StructDef, got %T", nodes[0])
	}
	if !sd.Public {
		t.Error("expected pub type (struct) to set Public=true")
	}
	if sd.Name != "Foo" {
		t.Errorf("expected name Foo, got %s", sd.Name)
	}
}

func TestParsePubTypeEnum(t *testing.T) {
	nodes := parse(t, "pub enum Color { Red; Blue }")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	ed, ok := nodes[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("expected *ast.EnumDef, got %T", nodes[0])
	}
	if !ed.Public {
		t.Error("expected pub type (enum) to set Public=true")
	}
	if ed.Name != "Color" {
		t.Errorf("expected name Color, got %s", ed.Name)
	}
}

func TestParsePubTypeDistinct(t *testing.T) {
	nodes := parse(t, "pub type UserId Int")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("expected *ast.TypeDef, got %T", nodes[0])
	}
	if !td.Public {
		t.Error("expected pub type (distinct) to set Public=true")
	}
	if td.Name != "UserId" {
		t.Errorf("expected name UserId, got %s", td.Name)
	}
}

func TestParsePubInterface(t *testing.T) {
	nodes := parse(t, "pub interface Display { fn to_string(value: self): String }")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	id, ok := nodes[0].(*ast.InterfaceDef)
	if !ok {
		t.Fatalf("expected *ast.InterfaceDef, got %T", nodes[0])
	}
	if !id.Public {
		t.Error("expected pub interface to set Public=true")
	}
	if id.Name != "Display" {
		t.Errorf("expected name Display, got %s", id.Name)
	}
}

func TestParsePubTypealias(t *testing.T) {
	nodes := parse(t, "pub typealias Id String")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	ta, ok := nodes[0].(*ast.TypeAlias)
	if !ok {
		t.Fatalf("expected *ast.TypeAlias, got %T", nodes[0])
	}
	if !ta.Public {
		t.Error("expected pub typealias to set Public=true")
	}
	if ta.Name != "Id" {
		t.Errorf("expected name Id, got %s", ta.Name)
	}
}

func TestParsePubOnce(t *testing.T) {
	nodes := parse(t, "pub once max_retries: Int = 3")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	ob, ok := nodes[0].(*ast.OnceBinding)
	if !ok {
		t.Fatalf("expected *ast.OnceBinding, got %T", nodes[0])
	}
	if !ob.Public {
		t.Error("expected pub once to set Public=true")
	}
	if ob.Name != "max_retries" {
		t.Errorf("expected name max_retries, got %s", ob.Name)
	}
}

func TestParsePubExternFn(t *testing.T) {
	nodes := parse(t, "pub host fn println(value: String): Unit")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	ef, ok := nodes[0].(*ast.ExternFunc)
	if !ok {
		t.Fatalf("expected *ast.ExternFunc, got %T", nodes[0])
	}
	if !ef.Public {
		t.Error("expected pub host fn to set Public=true")
	}
	if ef.Name != "println" {
		t.Errorf("expected name println, got %s", ef.Name)
	}
}

func TestParsePubExternType(t *testing.T) {
	nodes := parse(t, "pub host type Regex")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	et, ok := nodes[0].(*ast.ExternType)
	if !ok {
		t.Fatalf("expected *ast.ExternType, got %T", nodes[0])
	}
	if !et.Public {
		t.Error("expected pub host type to set Public=true")
	}
	if et.Name != "Regex" {
		t.Errorf("expected name Regex, got %s", et.Name)
	}
}

func TestParsePubOpaqueType(t *testing.T) {
	nodes := parse(t, "pub opaque type UserId Int")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("expected *ast.TypeDef, got %T", nodes[0])
	}
	if !td.Public {
		t.Error("expected pub opaque type to set Public=true")
	}
	if !td.Opaque {
		t.Error("expected pub opaque type to set Opaque=true")
	}
	if td.Name != "UserId" {
		t.Errorf("expected name UserId, got %s", td.Name)
	}
}

func TestParseOpaqueTypeWithoutPub(t *testing.T) {
	// Top-level `opaque type Foo Int` (no pub) — parses as Public=false,
	// Opaque=true. Private opaque type — rare but syntactically valid.
	nodes := parse(t, "opaque type UserId Int")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("expected *ast.TypeDef, got %T", nodes[0])
	}
	if td.Public {
		t.Error("expected opaque type without pub to set Public=false")
	}
	if !td.Opaque {
		t.Error("expected opaque type to set Opaque=true")
	}
	if td.Name != "UserId" {
		t.Errorf("expected name UserId, got %s", td.Name)
	}
}

func TestParseOpaqueInlineGoTypeBinding(t *testing.T) {
	nodes := parse(t, `gopkg "example.com/app/ffi" as ffi

opaque type RawBox go ffi.Box`)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(nodes))
	}
	et, ok := nodes[1].(*ast.ExternType)
	if !ok {
		t.Fatalf("expected *ast.ExternType, got %T", nodes[1])
	}
	if et.Public {
		t.Error("expected inline Go type binding to parse as private")
	}
	if !et.Opaque {
		t.Error("expected inline Go type binding to carry Opaque=true")
	}
	if et.ForeignAlias != "ffi" || et.ForeignName != "Box" || et.Name != "RawBox" {
		t.Fatalf("unexpected inline Go binding: %+v", et)
	}
}

func TestParseGoPackageWithExplicitAlias(t *testing.T) {
	nodes := parse(t, `gopkg "example.com/tagged/ffi" as ffi`)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	pkg, ok := nodes[0].(*ast.ExternPackage)
	if !ok {
		t.Fatalf("expected *ast.ExternPackage, got %T", nodes[0])
	}
	if pkg.Alias != "ffi" || pkg.ImportPath != "example.com/tagged/ffi" {
		t.Fatalf("unexpected gopkg declaration: %+v", pkg)
	}
}

func TestParseGoBlock(t *testing.T) {
	nodes := parse(t, `go {
  import (
    "database/sql"
    sqlite "modernc.org/sqlite"
    _ "github.com/lib/pq"
  )

  type Conn struct {
    db *sql.DB
  }
}`)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	block, ok := nodes[0].(*ast.GoBlock)
	if !ok {
		t.Fatalf("expected *ast.GoBlock, got %T", nodes[0])
	}
	if !strings.Contains(block.Body, `import (`) || !strings.Contains(block.Body, `type Conn struct`) {
		t.Fatalf("unexpected Go block body: %q", block.Body)
	}
}

func TestParsePubTypeNotOpaqueByDefault(t *testing.T) {
	nodes := parse(t, "pub type UserId Int")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("expected *ast.TypeDef, got %T", nodes[0])
	}
	if !td.Public {
		t.Error("expected pub type to set Public=true")
	}
	if td.Opaque {
		t.Error("expected pub type (without 'opaque') to set Opaque=false")
	}
	if td.Name != "UserId" {
		t.Errorf("expected name UserId, got %s", td.Name)
	}
}

// --- Return ---

func TestParseReturn(t *testing.T) {
	nodes := parse(t, "fn f(x) { if x < 0 { return -x }\n x }")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected *ast.FuncDef, got %T", nodes[0])
	}
	if len(fd.Body.Stmts) != 2 {
		t.Fatalf("expected 2 body stmts, got %d", len(fd.Body.Stmts))
	}
	// First stmt should be an ExprStmt wrapping an If
	exprStmt, ok := fd.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", fd.Body.Stmts[0])
	}
	ifNode, ok := exprStmt.Expr.(*ast.If)
	if !ok {
		t.Fatalf("expected If, got %T", exprStmt.Expr)
	}
	// The then block should contain a Return
	if len(ifNode.Then.Stmts) != 1 {
		t.Fatalf("expected 1 stmt in if-then, got %d", len(ifNode.Then.Stmts))
	}
	ret, ok := ifNode.Then.Stmts[0].(*ast.Return)
	if !ok {
		t.Fatalf("expected *ast.Return, got %T", ifNode.Then.Stmts[0])
	}
	if ret.Value == nil {
		t.Fatal("expected return to have a value")
	}
	un, ok := ret.Value.(*ast.Unary)
	if !ok || un.Op != "-" {
		t.Errorf("expected Unary{-}, got %T %v", ret.Value, ret.Value)
	}
}

func TestParseReturnBare(t *testing.T) {
	nodes := parse(t, "fn f() { return }")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected *ast.FuncDef, got %T", nodes[0])
	}
	if len(fd.Body.Stmts) != 1 {
		t.Fatalf("expected 1 body stmt, got %d", len(fd.Body.Stmts))
	}
	ret, ok := fd.Body.Stmts[0].(*ast.Return)
	if !ok {
		t.Fatalf("expected *ast.Return, got %T", fd.Body.Stmts[0])
	}
	if ret.Value != nil {
		t.Errorf("expected bare return (nil value), got %v", ret.Value)
	}
}

// --- Lambda Tests ---

func TestLambdaSingleParam(t *testing.T) {
	// |x| x + 1
	expr := parseExpr(t, "|x| x + 1")
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(lam.Params))
	}
	if lam.Params[0].Name != "x" {
		t.Errorf("expected param name x, got %s", lam.Params[0].Name)
	}
	if lam.Params[0].TypeAnnotation != nil {
		t.Errorf("expected no type, got %s", lam.Params[0].TypeAnnotation.TypeString())
	}
	if lam.Params[0].Default != nil {
		t.Errorf("expected no default, got %v", lam.Params[0].Default)
	}
	if len(lam.Body.Stmts) != 1 {
		t.Fatalf("expected 1 body stmt, got %d", len(lam.Body.Stmts))
	}
	stmt, ok := lam.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", lam.Body.Stmts[0])
	}
	bin, ok := stmt.Expr.(*ast.Binary)
	if !ok || bin.Op != "+" {
		t.Errorf("expected Binary{+}, got %T %v", stmt.Expr, stmt.Expr)
	}
}

func TestLambdaMultiParam(t *testing.T) {
	// |a, b| a + b
	expr := parseExpr(t, "|a, b| a + b")
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(lam.Params))
	}
	if lam.Params[0].Name != "a" {
		t.Errorf("expected param[0] a, got %s", lam.Params[0].Name)
	}
	if lam.Params[1].Name != "b" {
		t.Errorf("expected param[1] b, got %s", lam.Params[1].Name)
	}
	if len(lam.Body.Stmts) != 1 {
		t.Fatalf("expected 1 body stmt, got %d", len(lam.Body.Stmts))
	}
}

func TestLambdaWithTypeAnnotation(t *testing.T) {
	// |x: Int| x * 2
	expr := parseExpr(t, "|x: Int| x * 2")
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(lam.Params))
	}
	if lam.Params[0].Name != "x" {
		t.Errorf("expected param name x, got %s", lam.Params[0].Name)
	}
	if lam.Params[0].TypeAnnotation == nil || lam.Params[0].TypeAnnotation.TypeString() != "Int" {
		t.Errorf("expected param type Int, got %v", lam.Params[0].TypeAnnotation)
	}
}

func TestLambdaWithDefault(t *testing.T) {
	// |x = 0| x + 1
	expr := parseExpr(t, "|x = 0| x + 1")
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(lam.Params))
	}
	if lam.Params[0].Name != "x" {
		t.Errorf("expected param name x, got %s", lam.Params[0].Name)
	}
	if lam.Params[0].Default == nil {
		t.Fatal("expected param default, got nil")
	}
	def, ok := lam.Params[0].Default.(*ast.IntLit)
	if !ok || def.Value != 0 {
		t.Errorf("expected default IntLit{0}, got %T %v", lam.Params[0].Default, lam.Params[0].Default)
	}
}

func TestLambdaTupleDestructureWithDefault(t *testing.T) {
	// |(a, b) = (0, 1)| a + b — destructure pattern with default value.
	expr := parseExpr(t, "|(a, b) = (0, 1)| a + b")
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(lam.Params))
	}
	p := lam.Params[0]
	if p.Destructure == nil {
		t.Fatal("expected Destructure, got nil")
	}
	if _, ok := p.Destructure.(*ast.TuplePattern); !ok {
		t.Fatalf("expected *ast.TuplePattern, got %T", p.Destructure)
	}
	if p.Default == nil {
		t.Fatal("expected default, got nil")
	}
	if _, ok := p.Default.(*ast.TupleLit); !ok {
		t.Fatalf("expected default *ast.TupleLit, got %T", p.Default)
	}
}

func TestLambdaMultiStatement(t *testing.T) {
	// |x| { y = x * 2; y + 1 } — block expr for multi-stmt body
	expr := parseExpr(t, "|x| { y = x * 2; y + 1 }")
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Body.Stmts) != 2 {
		t.Fatalf("expected 2 body stmts, got %d", len(lam.Body.Stmts))
	}
	b, ok := lam.Body.Stmts[0].(*ast.Binding)
	if !ok {
		t.Fatalf("expected binding, got %T", lam.Body.Stmts[0])
	}
	if b.Name != "y" {
		t.Errorf("expected binding name y, got %s", b.Name)
	}
	stmt, ok := lam.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", lam.Body.Stmts[1])
	}
	bin, ok := stmt.Expr.(*ast.Binary)
	if !ok || bin.Op != "+" {
		t.Errorf("expected Binary{+}, got %T %v", stmt.Expr, stmt.Expr)
	}
}

func TestLambdaWildcardParam(t *testing.T) {
	// |_| 42
	expr := parseExpr(t, "|_| 42")
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(lam.Params))
	}
	if lam.Params[0].Name != "_" {
		t.Errorf("expected param name _, got %s", lam.Params[0].Name)
	}
	if len(lam.Body.Stmts) != 1 {
		t.Fatalf("expected 1 body stmt, got %d", len(lam.Body.Stmts))
	}
	stmt, ok := lam.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", lam.Body.Stmts[0])
	}
	lit, ok := stmt.Expr.(*ast.IntLit)
	if !ok || lit.Value != 42 {
		t.Errorf("expected IntLit{42}, got %T %v", stmt.Expr, stmt.Expr)
	}
}

func TestLambdaTupleDestructureParam(t *testing.T) {
	// |(a, b)| a + b
	expr := parseExpr(t, "|(a, b)| a + b")
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(lam.Params))
	}
	p := lam.Params[0]
	if p.Destructure == nil {
		t.Fatal("expected Destructure to be non-nil")
	}
	tp, ok := p.Destructure.(*ast.TuplePattern)
	if !ok {
		t.Fatalf("expected *ast.TuplePattern, got %T", p.Destructure)
	}
	if len(tp.Patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(tp.Patterns))
	}
	if ip, ok := tp.Patterns[0].(*ast.IdentPattern); !ok || ip.Name != "a" {
		t.Errorf("expected pattern 0 = IdentPattern{a}, got %T", tp.Patterns[0])
	}
	if ip, ok := tp.Patterns[1].(*ast.IdentPattern); !ok || ip.Name != "b" {
		t.Errorf("expected pattern 1 = IdentPattern{b}, got %T", tp.Patterns[1])
	}
}

func TestLambdaTupleDestructureWithWildcard(t *testing.T) {
	// |(_, v)| v
	expr := parseExpr(t, "|(_, v)| v")
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(lam.Params))
	}
	p := lam.Params[0]
	if p.Destructure == nil {
		t.Fatal("expected Destructure to be non-nil")
	}
	tp, ok := p.Destructure.(*ast.TuplePattern)
	if !ok {
		t.Fatalf("expected *ast.TuplePattern, got %T", p.Destructure)
	}
	if len(tp.Patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(tp.Patterns))
	}
	if _, ok := tp.Patterns[0].(*ast.WildcardPattern); !ok {
		t.Errorf("expected pattern 0 = WildcardPattern, got %T", tp.Patterns[0])
	}
	if ip, ok := tp.Patterns[1].(*ast.IdentPattern); !ok || ip.Name != "v" {
		t.Errorf("expected pattern 1 = IdentPattern{v}, got %T", tp.Patterns[1])
	}
}

func TestLambdaTupleDestructureMultiParams(t *testing.T) {
	// |(a, b), c| a + b + c
	expr := parseExpr(t, "|(a, b), c| a + b + c")
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(lam.Params))
	}
	if lam.Params[0].Destructure == nil {
		t.Fatal("expected first param Destructure to be non-nil")
	}
	if lam.Params[1].Destructure != nil {
		t.Fatal("expected second param Destructure to be nil")
	}
	if lam.Params[1].Name != "c" {
		t.Errorf("expected second param name c, got %s", lam.Params[1].Name)
	}
}

func TestLambdaStructDestructureParam(t *testing.T) {
	expr := parseExpr(t, "|{x, y}| x + y")
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(lam.Params))
	}
	p := lam.Params[0]
	if p.Destructure == nil {
		t.Fatal("expected Destructure to be non-nil")
	}
	sp, ok := p.Destructure.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected *ast.StructPattern, got %T", p.Destructure)
	}
	if len(sp.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(sp.Fields))
	}
	if sp.Fields[0].Name != "x" {
		t.Errorf("expected field 0 = x, got %s", sp.Fields[0].Name)
	}
	if sp.Fields[1].Name != "y" {
		t.Errorf("expected field 1 = y, got %s", sp.Fields[1].Name)
	}
}

func TestLambdaStructDestructureRenameParam(t *testing.T) {
	expr := parseExpr(t, "|{name: n, age: a}| n")
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(lam.Params))
	}
	sp, ok := lam.Params[0].Destructure.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected *ast.StructPattern, got %T", lam.Params[0].Destructure)
	}
	if sp.Fields[0].Name != "name" || sp.Fields[0].Binding != "n" {
		t.Errorf("expected field name->n, got %s->%s", sp.Fields[0].Name, sp.Fields[0].Binding)
	}
}

func TestLambdaMapDestructureParam(t *testing.T) {
	expr := parseExpr(t, `|{"name" => n}| n`)
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(lam.Params))
	}
	mp, ok := lam.Params[0].Destructure.(*ast.MapPattern)
	if !ok {
		t.Fatalf("expected *ast.MapPattern, got %T", lam.Params[0].Destructure)
	}
	if len(mp.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(mp.Entries))
	}
}

func TestBlockStillWorks(t *testing.T) {
	// { 1 + 2 } should still parse as a Block, not a Lambda
	expr := parseExpr(t, "{ 1 + 2 }")
	block, ok := expr.(*ast.Block)
	if !ok {
		t.Fatalf("expected *ast.Block, got %T", expr)
	}
	if len(block.Stmts) != 1 {
		t.Fatalf("expected 1 stmt, got %d", len(block.Stmts))
	}
}

func TestBlockExpression(t *testing.T) {
	expr := parseExpr(t, "{ x = 5; x + 1 }")
	block, ok := expr.(*ast.Block)
	if !ok {
		t.Fatalf("expected *ast.Block, got %T", expr)
	}
	if len(block.Stmts) != 2 {
		t.Fatalf("expected 2 stmts in block, got %d", len(block.Stmts))
	}
	b, ok := block.Stmts[0].(*ast.Binding)
	if !ok {
		t.Fatalf("block stmt[0]: expected *ast.Binding, got %T", block.Stmts[0])
	}
	if b.Name != "x" {
		t.Errorf("expected binding name x, got %s", b.Name)
	}
	stmt, ok := block.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("block stmt[1]: expected *ast.ExprStmt, got %T", block.Stmts[1])
	}
	bin, ok := stmt.Expr.(*ast.Binary)
	if !ok {
		t.Fatalf("block stmt[1]: expected *ast.Binary, got %T", stmt.Expr)
	}
	if bin.Op != "+" {
		t.Errorf("expected op +, got %s", bin.Op)
	}
}

func TestBlockExpressionWithAnnotatedBinding(t *testing.T) {
	expr := parseExpr(t, "{ value: Maybe<Int> = None; value }")
	block, ok := expr.(*ast.Block)
	if !ok {
		t.Fatalf("expected *ast.Block, got %T", expr)
	}
	if len(block.Stmts) != 2 {
		t.Fatalf("expected 2 stmts in block, got %d", len(block.Stmts))
	}
	b, ok := block.Stmts[0].(*ast.Binding)
	if !ok {
		t.Fatalf("block stmt[0]: expected *ast.Binding, got %T", block.Stmts[0])
	}
	if b.TypeAnnotation == nil || b.TypeAnnotation.TypeString() != "Maybe<Int>" {
		t.Fatalf("expected Maybe<Int> annotation, got %v", b.TypeAnnotation)
	}
}

// --- Pipe Operator ---

func TestParsePipe(t *testing.T) {
	// "5 |> double()" → Binary{IntLit{5}, "|>", Call{Ident{"double"}, []}}
	expr := parseExpr(t, "5 |> double()")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "|>" {
		t.Errorf("expected op |>, got %s", bin.Op)
	}
	left, ok := bin.Left.(*ast.IntLit)
	if !ok || left.Value != 5 {
		t.Errorf("expected left IntLit{5}, got %T %v", bin.Left, bin.Left)
	}
	call, ok := bin.Right.(*ast.Call)
	if !ok {
		t.Fatalf("expected right *ast.Call, got %T", bin.Right)
	}
	fn, ok := call.Func.(*ast.Ident)
	if !ok || fn.Name != "double" {
		t.Errorf("expected Ident{double}, got %T %v", call.Func, call.Func)
	}
	if len(call.Args) != 0 {
		t.Errorf("expected 0 args, got %d", len(call.Args))
	}
}

func TestParsePipeBare(t *testing.T) {
	// "5 |> double" → Binary{IntLit{5}, "|>", Ident{"double"}}
	expr := parseExpr(t, "5 |> double")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "|>" {
		t.Errorf("expected op |>, got %s", bin.Op)
	}
	left, ok := bin.Left.(*ast.IntLit)
	if !ok || left.Value != 5 {
		t.Errorf("expected left IntLit{5}, got %T %v", bin.Left, bin.Left)
	}
	right, ok := bin.Right.(*ast.Ident)
	if !ok || right.Name != "double" {
		t.Errorf("expected right Ident{double}, got %T %v", bin.Right, bin.Right)
	}
}

func TestParsePipeChain(t *testing.T) {
	// "5 |> double() |> add(1)" → Binary{Binary{IntLit{5}, "|>", Call{...}}, "|>", Call{...}}
	// Left-associative
	expr := parseExpr(t, "5 |> double() |> add(1)")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "|>" {
		t.Errorf("expected top op |>, got %s", bin.Op)
	}
	// Right side: add(1)
	rightCall, ok := bin.Right.(*ast.Call)
	if !ok {
		t.Fatalf("expected right *ast.Call, got %T", bin.Right)
	}
	rightFn, ok := rightCall.Func.(*ast.Ident)
	if !ok || rightFn.Name != "add" {
		t.Errorf("expected Ident{add}, got %T %v", rightCall.Func, rightCall.Func)
	}
	if len(rightCall.Args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(rightCall.Args))
	}
	// Left side: 5 |> double()
	leftBin, ok := bin.Left.(*ast.Binary)
	if !ok {
		t.Fatalf("expected left *ast.Binary, got %T", bin.Left)
	}
	if leftBin.Op != "|>" {
		t.Errorf("expected left op |>, got %s", leftBin.Op)
	}
	leftLit, ok := leftBin.Left.(*ast.IntLit)
	if !ok || leftLit.Value != 5 {
		t.Errorf("expected IntLit{5}, got %T %v", leftBin.Left, leftBin.Left)
	}
}

func TestParsePipeDbgFinalStageBeforeNextStatement(t *testing.T) {
	src := `fn main() {
  [1, 2, 3]
  |> dbg

  value = 4
}`
	nodes := parse(t, src)
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected FuncDef, got %T", nodes[0])
	}
	if len(fn.Body.Stmts) != 2 {
		t.Fatalf("expected two body statements, got %d", len(fn.Body.Stmts))
	}
	stmt, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected first statement ExprStmt, got %T", fn.Body.Stmts[0])
	}
	bin, ok := stmt.Expr.(*ast.Binary)
	if !ok || bin.Op != "|>" {
		t.Fatalf("expected first statement pipe, got %T %#v", stmt.Expr, stmt.Expr)
	}
	dbg, ok := bin.Right.(*ast.Dbg)
	if !ok {
		t.Fatalf("expected pipe RHS dbg, got %T", bin.Right)
	}
	if dbg.Expr != nil {
		t.Fatalf("expected bare pipe-stage dbg, got expr %T", dbg.Expr)
	}
}

func TestParsePipePrecedence(t *testing.T) {
	// "1 + 2 |> f()" → Binary{Binary{IntLit{1}, "+", IntLit{2}}, "|>", Call{...}}
	// + binds tighter than |>
	expr := parseExpr(t, "1 + 2 |> f()")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "|>" {
		t.Errorf("expected top op |>, got %s", bin.Op)
	}
	leftBin, ok := bin.Left.(*ast.Binary)
	if !ok {
		t.Fatalf("expected left *ast.Binary, got %T", bin.Left)
	}
	if leftBin.Op != "+" {
		t.Errorf("expected left op +, got %s", leftBin.Op)
	}
	rightCall, ok := bin.Right.(*ast.Call)
	if !ok {
		t.Fatalf("expected right *ast.Call, got %T", bin.Right)
	}
	fn, ok := rightCall.Func.(*ast.Ident)
	if !ok || fn.Name != "f" {
		t.Errorf("expected Ident{f}, got %T %v", rightCall.Func, rightCall.Func)
	}
}

func TestParsePipeBindsTighterThanEquality(t *testing.T) {
	expr := parseExpr(t, "xs |> to_list() == ys")
	eq, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected equality *ast.Binary, got %T", expr)
	}
	if eq.Op != "==" {
		t.Fatalf("expected top op ==, got %s", eq.Op)
	}
	pipe, ok := eq.Left.(*ast.Binary)
	if !ok || pipe.Op != "|>" {
		t.Fatalf("expected equality left side to be pipe, got %T %#v", eq.Left, eq.Left)
	}
	call, ok := pipe.Right.(*ast.Call)
	if !ok {
		t.Fatalf("expected pipe RHS call, got %T", pipe.Right)
	}
	fn, ok := call.Func.(*ast.Ident)
	if !ok || fn.Name != "to_list" {
		t.Fatalf("expected pipe RHS to_list call, got %T %#v", call.Func, call.Func)
	}
	if _, ok := eq.Right.(*ast.Ident); !ok {
		t.Fatalf("expected equality right side ident, got %T", eq.Right)
	}
}

// --- Placeholder ---

func TestParsePlaceholder(t *testing.T) {
	// "add(1, _)" → Call{Ident{"add"}, [IntLit{1}, Placeholder{}]}
	expr := parseExpr(t, "add(1, _)")
	call, ok := expr.(*ast.Call)
	if !ok {
		t.Fatalf("expected *ast.Call, got %T", expr)
	}
	fn, ok := call.Func.(*ast.Ident)
	if !ok || fn.Name != "add" {
		t.Errorf("expected Ident{add}, got %T %v", call.Func, call.Func)
	}
	if len(call.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(call.Args))
	}
	a1, ok := call.Args[0].(*ast.IntLit)
	if !ok || a1.Value != 1 {
		t.Errorf("expected IntLit{1}, got %T %v", call.Args[0], call.Args[0])
	}
	_, ok = call.Args[1].(*ast.Placeholder)
	if !ok {
		t.Errorf("expected Placeholder, got %T", call.Args[1])
	}
}

func TestParseMultilinePipe(t *testing.T) {
	// "5\n|> double()" should parse same as "5 |> double()"
	expr := parseExpr(t, "5\n|> double()")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "|>" {
		t.Errorf("expected op |>, got %s", bin.Op)
	}
	left, ok := bin.Left.(*ast.IntLit)
	if !ok || left.Value != 5 {
		t.Errorf("expected left IntLit{5}, got %T %v", bin.Left, bin.Left)
	}
	call, ok := bin.Right.(*ast.Call)
	if !ok {
		t.Fatalf("expected right *ast.Call, got %T", bin.Right)
	}
	fn, ok := call.Func.(*ast.Ident)
	if !ok || fn.Name != "double" {
		t.Errorf("expected Ident{double}, got %T %v", call.Func, call.Func)
	}
}

func TestParseMultilinePipeChain(t *testing.T) {
	// Multi-line pipe chain
	src := "5\n|> double()\n|> add(1)"
	expr := parseExpr(t, src)
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "|>" {
		t.Errorf("expected top op |>, got %s", bin.Op)
	}
	// Right: add(1)
	rightCall, ok := bin.Right.(*ast.Call)
	if !ok {
		t.Fatalf("expected right *ast.Call, got %T", bin.Right)
	}
	rightFn, ok := rightCall.Func.(*ast.Ident)
	if !ok || rightFn.Name != "add" {
		t.Errorf("expected Ident{add}, got %T %v", rightCall.Func, rightCall.Func)
	}
	// Left: 5 |> double()
	leftBin, ok := bin.Left.(*ast.Binary)
	if !ok {
		t.Fatalf("expected left *ast.Binary, got %T", bin.Left)
	}
	if leftBin.Op != "|>" {
		t.Errorf("expected left op |>, got %s", leftBin.Op)
	}
}

func TestParseMultilineCallArgs(t *testing.T) {
	// f(\n  a,\n  b\n) should parse as f(a, b)
	src := "f(\n  a,\n  b\n)"
	expr := parseExpr(t, src)
	call, ok := expr.(*ast.Call)
	if !ok {
		t.Fatalf("expected *ast.Call, got %T", expr)
	}
	if len(call.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(call.Args))
	}
}

func TestParseMultilineList(t *testing.T) {
	src := "[\n  1,\n  2,\n  3\n]"
	expr := parseExpr(t, src)
	list, ok := expr.(*ast.ListLit)
	if !ok {
		t.Fatalf("expected *ast.ListLit, got %T", expr)
	}
	if len(list.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(list.Items))
	}
}

func TestParseVectorLiteral(t *testing.T) {
	expr := parseExpr(t, "#[1, 2, 3]")
	vector, ok := expr.(*ast.VectorLit)
	if !ok {
		t.Fatalf("expected *ast.VectorLit, got %T", expr)
	}
	if len(vector.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(vector.Items))
	}
}

func TestParseEmptyVectorLiteral(t *testing.T) {
	expr := parseExpr(t, "#[]")
	vector, ok := expr.(*ast.VectorLit)
	if !ok {
		t.Fatalf("expected *ast.VectorLit, got %T", expr)
	}
	if len(vector.Items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(vector.Items))
	}
}

func TestParseSetLiteral(t *testing.T) {
	expr := parseExpr(t, "#{1, 2, 3}")
	set, ok := expr.(*ast.SetLit)
	if !ok {
		t.Fatalf("expected *ast.SetLit, got %T", expr)
	}
	if len(set.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(set.Items))
	}
}

func TestParseEmptySetLiteral(t *testing.T) {
	expr := parseExpr(t, "#{}")
	set, ok := expr.(*ast.SetLit)
	if !ok {
		t.Fatalf("expected *ast.SetLit, got %T", expr)
	}
	if len(set.Items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(set.Items))
	}
}

func TestParsePipePlaceholder(t *testing.T) {
	// "10 |> divide(100, _)" → Binary{IntLit{10}, "|>", Call{Ident{"divide"}, [IntLit{100}, Placeholder{}]}}
	expr := parseExpr(t, "10 |> divide(100, _)")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected *ast.Binary, got %T", expr)
	}
	if bin.Op != "|>" {
		t.Errorf("expected op |>, got %s", bin.Op)
	}
	left, ok := bin.Left.(*ast.IntLit)
	if !ok || left.Value != 10 {
		t.Errorf("expected left IntLit{10}, got %T %v", bin.Left, bin.Left)
	}
	call, ok := bin.Right.(*ast.Call)
	if !ok {
		t.Fatalf("expected right *ast.Call, got %T", bin.Right)
	}
	fn, ok := call.Func.(*ast.Ident)
	if !ok || fn.Name != "divide" {
		t.Errorf("expected Ident{divide}, got %T %v", call.Func, call.Func)
	}
	if len(call.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(call.Args))
	}
	a1, ok := call.Args[0].(*ast.IntLit)
	if !ok || a1.Value != 100 {
		t.Errorf("expected IntLit{100}, got %T %v", call.Args[0], call.Args[0])
	}
	_, ok = call.Args[1].(*ast.Placeholder)
	if !ok {
		t.Errorf("expected Placeholder, got %T", call.Args[1])
	}
}

// --- Case Expressions ---

func TestCaseAdHoc(t *testing.T) {
	// case { x > 0 -> "positive"\n _ -> "other" }
	expr := parseExpr(t, "case { x > 0 -> \"positive\"\n _ -> \"other\" }")
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	if c.Value != nil {
		t.Fatalf("expected nil Value for ad-hoc case, got %T", c.Value)
	}
	if len(c.Branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(c.Branches))
	}
	// Branch 0: pattern is Binary expr (x > 0), body is StringLit
	bin, ok := c.Branches[0].Pattern.(*ast.Binary)
	if !ok {
		t.Fatalf("branch[0] pattern: expected *ast.Binary, got %T", c.Branches[0].Pattern)
	}
	if bin.Op != ">" {
		t.Errorf("branch[0] pattern op: expected >, got %s", bin.Op)
	}
	body0, ok := c.Branches[0].Body.(*ast.StringLit)
	if !ok || body0.Value != "positive" {
		t.Errorf("branch[0] body: expected StringLit{positive}, got %T %v", c.Branches[0].Body, c.Branches[0].Body)
	}
	// Branch 1: pattern is WildcardPattern, body is StringLit
	_, ok = c.Branches[1].Pattern.(*ast.WildcardPattern)
	if !ok {
		t.Fatalf("branch[1] pattern: expected *ast.WildcardPattern, got %T", c.Branches[1].Pattern)
	}
	body1, ok := c.Branches[1].Body.(*ast.StringLit)
	if !ok || body1.Value != "other" {
		t.Errorf("branch[1] body: expected StringLit{other}, got %T %v", c.Branches[1].Body, c.Branches[1].Body)
	}
}

func TestCaseValueMatch(t *testing.T) {
	// case x { 1 -> "one"\n 2 -> "two"\n _ -> "other" }
	expr := parseExpr(t, "case x { 1 -> \"one\"\n 2 -> \"two\"\n _ -> \"other\" }")
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	id, ok := c.Value.(*ast.Ident)
	if !ok || id.Name != "x" {
		t.Fatalf("expected Value Ident{x}, got %T %v", c.Value, c.Value)
	}
	if len(c.Branches) != 3 {
		t.Fatalf("expected 3 branches, got %d", len(c.Branches))
	}
	lit0, ok := c.Branches[0].Pattern.(*ast.IntLit)
	if !ok || lit0.Value != 1 {
		t.Errorf("branch[0] pattern: expected IntLit{1}, got %T %v", c.Branches[0].Pattern, c.Branches[0].Pattern)
	}
	lit1, ok := c.Branches[1].Pattern.(*ast.IntLit)
	if !ok || lit1.Value != 2 {
		t.Errorf("branch[1] pattern: expected IntLit{2}, got %T %v", c.Branches[1].Pattern, c.Branches[1].Pattern)
	}
	_, ok = c.Branches[2].Pattern.(*ast.WildcardPattern)
	if !ok {
		t.Fatalf("branch[2] pattern: expected *ast.WildcardPattern, got %T", c.Branches[2].Pattern)
	}
}

func TestCaseGuard(t *testing.T) {
	// case x { n when n > 10 -> "big"\n _ -> "small" }
	expr := parseExpr(t, "case x { n when n > 10 -> \"big\"\n _ -> \"small\" }")
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	if len(c.Branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(c.Branches))
	}
	ip, ok := c.Branches[0].Pattern.(*ast.IdentPattern)
	if !ok || ip.Name != "n" {
		t.Fatalf("branch[0] pattern: expected IdentPattern{n}, got %T %v", c.Branches[0].Pattern, c.Branches[0].Pattern)
	}
	if c.Branches[0].Guard == nil {
		t.Fatal("branch[0]: expected guard, got nil")
	}
	guard, ok := c.Branches[0].Guard.(*ast.Binary)
	if !ok || guard.Op != ">" {
		t.Errorf("branch[0] guard: expected Binary{>}, got %T %v", c.Branches[0].Guard, c.Branches[0].Guard)
	}
	body0, ok := c.Branches[0].Body.(*ast.StringLit)
	if !ok || body0.Value != "big" {
		t.Errorf("branch[0] body: expected StringLit{big}, got %T %v", c.Branches[0].Body, c.Branches[0].Body)
	}
	_, ok = c.Branches[1].Pattern.(*ast.WildcardPattern)
	if !ok {
		t.Fatalf("branch[1] pattern: expected *ast.WildcardPattern, got %T", c.Branches[1].Pattern)
	}
	if c.Branches[1].Guard != nil {
		t.Errorf("branch[1]: expected nil guard, got %T", c.Branches[1].Guard)
	}
}

func TestCaseIdentBinding(t *testing.T) {
	// case x { n -> n + 1 }
	expr := parseExpr(t, "case x { n -> n + 1 }")
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	if len(c.Branches) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(c.Branches))
	}
	ip, ok := c.Branches[0].Pattern.(*ast.IdentPattern)
	if !ok || ip.Name != "n" {
		t.Fatalf("branch[0] pattern: expected IdentPattern{n}, got %T %v", c.Branches[0].Pattern, c.Branches[0].Pattern)
	}
	bin, ok := c.Branches[0].Body.(*ast.Binary)
	if !ok || bin.Op != "+" {
		t.Errorf("branch[0] body: expected Binary{+}, got %T %v", c.Branches[0].Body, c.Branches[0].Body)
	}
}

func TestCaseStringMatch(t *testing.T) {
	// case s { "hello" -> "greeting"\n _ -> "unknown" }
	expr := parseExpr(t, "case s { \"hello\" -> \"greeting\"\n _ -> \"unknown\" }")
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	if len(c.Branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(c.Branches))
	}
	sl, ok := c.Branches[0].Pattern.(*ast.StringLit)
	if !ok || sl.Value != "hello" {
		t.Errorf("branch[0] pattern: expected StringLit{hello}, got %T %v", c.Branches[0].Pattern, c.Branches[0].Pattern)
	}
	body0, ok := c.Branches[0].Body.(*ast.StringLit)
	if !ok || body0.Value != "greeting" {
		t.Errorf("branch[0] body: expected StringLit{greeting}, got %T %v", c.Branches[0].Body, c.Branches[0].Body)
	}
	_, ok = c.Branches[1].Pattern.(*ast.WildcardPattern)
	if !ok {
		t.Fatalf("branch[1] pattern: expected *ast.WildcardPattern, got %T", c.Branches[1].Pattern)
	}
}

func TestParseLambdaAsLastArg(t *testing.T) {
	// f(1, |x| x + 1) — lambdas are passed explicitly in parens, no trailing sugar.
	expr := parseExpr(t, "f(1, |x| x + 1)")
	call, ok := expr.(*ast.Call)
	if !ok {
		t.Fatalf("expected *ast.Call, got %T", expr)
	}
	if len(call.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(call.Args))
	}
	lam, ok := call.Args[1].(*ast.Lambda)
	if !ok {
		t.Fatalf("expected arg[1] *ast.Lambda, got %T", call.Args[1])
	}
	if len(lam.Params) != 1 || lam.Params[0].Name != "x" {
		t.Errorf("expected lambda param x, got %v", lam.Params)
	}
}

func TestCaseBoolMatch(t *testing.T) {
	// case b { True -> "yes"\n False -> "no" }
	expr := parseExpr(t, "case b { True -> \"yes\"\n False -> \"no\" }")
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	if len(c.Branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(c.Branches))
	}
	bl0, ok := c.Branches[0].Pattern.(*ast.EnumPattern)
	if !ok || bl0.Variant.TypeString() != "True" {
		t.Errorf("branch[0] pattern: expected EnumPattern{True}, got %T %v", c.Branches[0].Pattern, c.Branches[0].Pattern)
	}
	body0, ok := c.Branches[0].Body.(*ast.StringLit)
	if !ok || body0.Value != "yes" {
		t.Errorf("branch[0] body: expected StringLit{yes}, got %T %v", c.Branches[0].Body, c.Branches[0].Body)
	}
	bl1, ok := c.Branches[1].Pattern.(*ast.EnumPattern)
	if !ok || bl1.Variant.TypeString() != "False" {
		t.Errorf("branch[1] pattern: expected EnumPattern{False}, got %T %v", c.Branches[1].Pattern, c.Branches[1].Pattern)
	}
	body1, ok := c.Branches[1].Body.(*ast.StringLit)
	if !ok || body1.Value != "no" {
		t.Errorf("branch[1] body: expected StringLit{no}, got %T %v", c.Branches[1].Body, c.Branches[1].Body)
	}
}

func TestParseTupleFieldAccess(t *testing.T) {
	tokens := lexer.Lex("pair.0")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	stmt := nodes[0].(*ast.ExprStmt)
	fa, ok := stmt.Expr.(*ast.FieldAccess)
	if !ok {
		t.Fatalf("expected FieldAccess, got %T", stmt.Expr)
	}
	if fa.Field.Name != "0" {
		t.Errorf("expected field '0', got '%s'", fa.Field.Name)
	}
}

func TestParseTupleLiteral(t *testing.T) {
	tokens := lexer.Lex(`(1, "hello")`)
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	stmt, ok := nodes[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", nodes[0])
	}
	tuple, ok := stmt.Expr.(*ast.TupleLit)
	if !ok {
		t.Fatalf("expected TupleLit, got %T", stmt.Expr)
	}
	if len(tuple.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(tuple.Items))
	}
}

func TestParseGroupingNotTuple(t *testing.T) {
	tokens := lexer.Lex("(1 + 2)")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	stmt := nodes[0].(*ast.ExprStmt)
	if _, ok := stmt.Expr.(*ast.TupleLit); ok {
		t.Error("expected grouping, got TupleLit")
	}
}

func TestParseTupleThreeElements(t *testing.T) {
	tokens := lexer.Lex(`(1, 2, 3)`)
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	stmt := nodes[0].(*ast.ExprStmt)
	tuple, ok := stmt.Expr.(*ast.TupleLit)
	if !ok {
		t.Fatalf("expected TupleLit, got %T", stmt.Expr)
	}
	if len(tuple.Items) != 3 {
		t.Errorf("expected 3 items, got %d", len(tuple.Items))
	}
}

func TestParseBraceIsAlwaysBlock(t *testing.T) {
	// Without trailing-lambda sugar and without `it` shorthand, { ... } is
	// always a block expression (or struct/map literal when shaped as one),
	// never a lambda.
	tokens := lexer.Lex(`{ 1 + 2 }`)
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	stmt := nodes[0].(*ast.ExprStmt)
	if _, ok := stmt.Expr.(*ast.Lambda); ok {
		t.Error("expected Block, got Lambda — `{ ... }` is never a lambda anymore")
	}
	if _, ok := stmt.Expr.(*ast.Block); !ok {
		t.Errorf("expected Block, got %T", stmt.Expr)
	}
}

func TestParseZeroArgLambda(t *testing.T) {
	// || body — zero-arg lambda.
	expr := parseExpr(t, `|| 42`)
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected Lambda, got %T", expr)
	}
	if len(lam.Params) != 0 {
		t.Errorf("expected 0 params, got %d", len(lam.Params))
	}
}

func TestParseTypeDef(t *testing.T) {
	tokens := lexer.Lex("type Id Int")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("expected TypeDef, got %T", nodes[0])
	}
	if td.Name != "Id" {
		t.Errorf("expected name 'Id', got %q", td.Name)
	}
	if td.InnerTypeExpr == nil || td.InnerTypeExpr.TypeString() != "Int" {
		t.Errorf("expected inner 'Int', got %v", td.InnerTypeExpr)
	}
}

func TestParseTypeDefZeroSized(t *testing.T) {
	tokens := lexer.Lex("type Expired")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("expected TypeDef, got %T", nodes[0])
	}
	if td.Name != "Expired" {
		t.Errorf("expected name 'Expired', got %q", td.Name)
	}
	if td.InnerTypeExpr != nil {
		t.Errorf("expected nil inner, got %v", td.InnerTypeExpr)
	}
}

// --- Type Alias ---

func TestParseTypeAlias(t *testing.T) {
	tokens := lexer.Lex("pub typealias Name String")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var alias *ast.TypeAlias
	for _, n := range nodes {
		if v, ok := n.(*ast.TypeAlias); ok {
			alias = v
			break
		}
	}
	if alias == nil {
		t.Fatal("expected *ast.TypeAlias among nodes")
	}
	if alias.Name != "Name" {
		t.Errorf("expected name 'Name', got %q", alias.Name)
	}
	if alias.TargetTypeExpr == nil || alias.TargetTypeExpr.TypeString() != "String" {
		t.Errorf("expected target 'String', got %v", alias.TargetTypeExpr)
	}
	if !alias.Public {
		t.Errorf("expected public (pub modifier)")
	}
}

func TestParseTypeAliasPrivate(t *testing.T) {
	tokens := lexer.Lex("typealias name String")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	alias := nodes[0].(*ast.TypeAlias)
	if alias.Public {
		t.Errorf("expected private (no pub modifier)")
	}
}

// Bound aliases — RHS is a conjunction of interface bounds. The alias is usable
// in `where T: Foo` positions only; the parser just records the list of bound
// TypeExprs. Single-type aliases keep the existing
// `TargetTypeExpr` shape; multi-bound aliases populate `Bounds` with
// every bound listed after the alias name.
func TestParseTypeAlias_MultiBound(t *testing.T) {
	tokens := lexer.Lex("typealias Foo A and B")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	alias, ok := nodes[0].(*ast.TypeAlias)
	if !ok {
		t.Fatalf("expected TypeAlias, got %T", nodes[0])
	}
	if alias.Name != "Foo" {
		t.Errorf("expected name 'Foo', got %q", alias.Name)
	}
	if len(alias.Bounds) != 2 {
		t.Fatalf("expected 2 bounds, got %d", len(alias.Bounds))
	}
	if alias.Bounds[0].TypeString() != "A" {
		t.Errorf("expected bound[0]=A, got %s", alias.Bounds[0].TypeString())
	}
	if alias.Bounds[1].TypeString() != "B" {
		t.Errorf("expected bound[1]=B, got %s", alias.Bounds[1].TypeString())
	}
	if alias.TargetTypeExpr != nil {
		t.Errorf("expected TargetTypeExpr nil for multi-bound alias, got %v", alias.TargetTypeExpr)
	}
}

func TestParseTypeAlias_MultiBound_ThreeWay(t *testing.T) {
	tokens := lexer.Lex("typealias Foo A and B and C")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	alias := nodes[0].(*ast.TypeAlias)
	if len(alias.Bounds) != 3 {
		t.Fatalf("expected 3 bounds, got %d", len(alias.Bounds))
	}
}

// Single-type alias still works after the multi-bound extension.
// `TargetTypeExpr` is set; `Bounds` is empty.
func TestParseTypeAlias_SingleType_StillWorks(t *testing.T) {
	tokens := lexer.Lex("typealias Name String")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	alias := nodes[0].(*ast.TypeAlias)
	if alias.TargetTypeExpr == nil {
		t.Error("expected TargetTypeExpr non-nil for single-type alias")
	}
	if len(alias.Bounds) != 0 {
		t.Errorf("expected no Bounds for single-type alias, got %d", len(alias.Bounds))
	}
}

// New form (no `=`) — explicit coverage for the hard cut.
func TestParser_TypeAlias_NoEqualsSign(t *testing.T) {
	tokens := lexer.Lex("pub typealias Handler (String, String) -> Result<String, String>")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var alias *ast.TypeAlias
	for _, n := range nodes {
		if v, ok := n.(*ast.TypeAlias); ok {
			alias = v
			break
		}
	}
	if alias == nil {
		t.Fatal("expected *ast.TypeAlias among nodes")
	}
	if alias.Name != "Handler" {
		t.Errorf("expected name 'Handler', got %q", alias.Name)
	}
	if !alias.Public {
		t.Errorf("expected public")
	}
	if alias.TargetTypeExpr == nil {
		t.Fatalf("expected TargetTypeExpr non-nil for function-type alias")
	}
	if _, ok := alias.TargetTypeExpr.(*ast.FuncType); !ok {
		t.Errorf("expected target to be FuncType, got %T", alias.TargetTypeExpr)
	}
}

func TestParser_TypeAlias_BoundAlias_NoEqualsSign(t *testing.T) {
	tokens := lexer.Lex("typealias ShowAndTag Showable and Tagged")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	alias := nodes[0].(*ast.TypeAlias)
	if alias.Name != "ShowAndTag" {
		t.Errorf("expected name 'ShowAndTag', got %q", alias.Name)
	}
	if len(alias.Bounds) != 2 {
		t.Fatalf("expected 2 bounds, got %d", len(alias.Bounds))
	}
	if alias.Bounds[0].TypeString() != "Showable" {
		t.Errorf("bound[0]: want Showable, got %s", alias.Bounds[0].TypeString())
	}
	if alias.Bounds[1].TypeString() != "Tagged" {
		t.Errorf("bound[1]: want Tagged, got %s", alias.Bounds[1].TypeString())
	}
}

func TestParser_TypeAlias_OldFormRejected(t *testing.T) {
	tokens := lexer.Lex("typealias Handler = (String, String) -> Result<String, String>")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatalf("expected parse error for old `typealias X = T` form, got none")
	}
}

// --- Try Operator ---

func TestParseTryOperator(t *testing.T) {
	tokens := lexer.Lex("try x")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	stmt := nodes[0].(*ast.ExprStmt)
	tryOp, ok := stmt.Expr.(*ast.TryOp)
	if !ok {
		t.Fatalf("expected TryOp, got %T", stmt.Expr)
	}
	ident, ok := tryOp.Expr.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident inside TryOp, got %T", tryOp.Expr)
	}
	if ident.Name != "x" {
		t.Errorf("expected 'x', got %q", ident.Name)
	}
}

func TestParseTryAfterCall(t *testing.T) {
	tokens := lexer.Lex("try foo(42)")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	stmt := nodes[0].(*ast.ExprStmt)
	tryOp, ok := stmt.Expr.(*ast.TryOp)
	if !ok {
		t.Fatalf("expected TryOp, got %T", stmt.Expr)
	}
	if _, ok := tryOp.Expr.(*ast.Call); !ok {
		t.Fatalf("expected Call inside TryOp, got %T", tryOp.Expr)
	}
}

func TestParseTryInPipe(t *testing.T) {
	// x |> foo() |> try
	// Should parse as: (x |> foo()) |> try
	tokens := lexer.Lex("x |> foo() |> try")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	stmt := nodes[0].(*ast.ExprStmt)
	// The outermost should be a Binary with op "|>"
	bin, ok := stmt.Expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected Binary (pipe), got %T", stmt.Expr)
	}
	if bin.Op != "|>" {
		t.Errorf("expected pipe operator, got %q", bin.Op)
	}
	// The left side of the outer pipe should also be a pipe
	leftPipe, ok := bin.Left.(*ast.Binary)
	if !ok {
		t.Fatalf("expected Binary (inner pipe), got %T", bin.Left)
	}
	if _, ok := leftPipe.Right.(*ast.Call); !ok {
		t.Fatalf("expected Call on inner pipe RHS, got %T", leftPipe.Right)
	}
	// The right side of the outer pipe should be a bare TryOp.
	tryOp, ok := bin.Right.(*ast.TryOp)
	if !ok {
		t.Fatalf("expected TryOp on pipe RHS, got %T", bin.Right)
	}
	if tryOp.Expr != nil {
		t.Fatalf("expected bare TryOp in pipe, got operand %T", tryOp.Expr)
	}
}

// --- Prefix `try` keyword (the only error-propagation spelling) ---

func TestParseTryKeywordCall(t *testing.T) {
	// `try foo()` parses to a TryOp wrapping the call foo().
	expr := parseExpr(t, "try foo()")
	tryOp, ok := expr.(*ast.TryOp)
	if !ok {
		t.Fatalf("expected TryOp, got %T", expr)
	}
	call, ok := tryOp.Expr.(*ast.Call)
	if !ok {
		t.Fatalf("expected Call inside TryOp, got %T", tryOp.Expr)
	}
	if fn, ok := call.Func.(*ast.Ident); !ok || fn.Name != "foo" {
		t.Errorf("expected call to foo, got %T %v", call.Func, call.Func)
	}
}

func TestParseTryKeywordPipePrecedence(t *testing.T) {
	// `try a() |> b()` parses as `(try a()) |> b()` — try binds the a()
	// stage only, never the whole pipe.
	expr := parseExpr(t, "try a() |> b()")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected Binary (pipe) at top, got %T", expr)
	}
	if bin.Op != "|>" {
		t.Errorf("expected pipe operator, got %q", bin.Op)
	}
	tryOp, ok := bin.Left.(*ast.TryOp)
	if !ok {
		t.Fatalf("expected TryOp on pipe LHS, got %T", bin.Left)
	}
	if _, ok := tryOp.Expr.(*ast.Call); !ok {
		t.Fatalf("expected Call inside TryOp, got %T", tryOp.Expr)
	}
}

func TestParseTryKeywordArithmeticPrecedence(t *testing.T) {
	// `try a() + b()` parses as `(try a()) + b()` — try binds tighter than +.
	expr := parseExpr(t, "try a() + b()")
	bin, ok := expr.(*ast.Binary)
	if !ok {
		t.Fatalf("expected Binary (+) at top, got %T", expr)
	}
	if bin.Op != "+" {
		t.Errorf("expected + operator, got %q", bin.Op)
	}
	if _, ok := bin.Left.(*ast.TryOp); !ok {
		t.Fatalf("expected TryOp on + LHS, got %T", bin.Left)
	}
}

func TestParseDbgKeywordPipePrecedence(t *testing.T) {
	// `dbg a() |> b()` parses as `dbg (a() |> b())` — unlike `try`, `dbg`
	// observes the whole expression to its right unless parentheses narrow it.
	expr := parseExpr(t, "dbg a() |> b()")
	dbg, ok := expr.(*ast.Dbg)
	if !ok {
		t.Fatalf("expected Dbg at top, got %T", expr)
	}
	if bin, ok := dbg.Expr.(*ast.Binary); !ok || bin.Op != "|>" {
		t.Fatalf("expected pipe inside Dbg, got %T %[1]v", dbg.Expr)
	}
}

func TestParseDbgKeywordArithmeticPrecedence(t *testing.T) {
	// `dbg a() + b()` parses as `dbg (a() + b())`.
	expr := parseExpr(t, "dbg a() + b()")
	dbg, ok := expr.(*ast.Dbg)
	if !ok {
		t.Fatalf("expected Dbg at top, got %T", expr)
	}
	if bin, ok := dbg.Expr.(*ast.Binary); !ok || bin.Op != "+" {
		t.Fatalf("expected + inside Dbg, got %T %[1]v", dbg.Expr)
	}
}

func TestParseDbgCanBeNarrowedWithParens(t *testing.T) {
	// Parentheses let multiple dbg observations live inside one larger
	// expression instead of one dbg swallowing the whole expression.
	expr := parseExpr(t, "(dbg a()) + (dbg b())")
	bin, ok := expr.(*ast.Binary)
	if !ok || bin.Op != "+" {
		t.Fatalf("expected + at top, got %T %[1]v", expr)
	}
	if _, ok := bin.Left.(*ast.GroupedExpr); !ok {
		t.Fatalf("expected grouped dbg on left, got %T", bin.Left)
	}
	if _, ok := bin.Right.(*ast.GroupedExpr); !ok {
		t.Fatalf("expected grouped dbg on right, got %T", bin.Right)
	}
}

func TestParseTryKeywordWithTrailingQuestion(t *testing.T) {
	// `try foo()?` — the postfix `?` operator was removed, so a trailing `?`
	// is now a parse error (the lexer emits ILLEGAL for the stray glyph).
	tokens := lexer.Lex("try foo()?")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatalf("expected a parse error for trailing `?`, got none")
	}
	if !strings.Contains(err.Error(), "?") {
		t.Errorf("expected error to reference the stray `?` glyph, got %q", err.Error())
	}
}

func TestParseTryKeywordFieldChain(t *testing.T) {
	// `try a.b.c` parses as `try (a.b.c)` — applies to the whole field chain.
	expr := parseExpr(t, "try a.b.c")
	tryOp, ok := expr.(*ast.TryOp)
	if !ok {
		t.Fatalf("expected TryOp at top, got %T", expr)
	}
	fa, ok := tryOp.Expr.(*ast.FieldAccess)
	if !ok {
		t.Fatalf("expected FieldAccess inside TryOp, got %T", tryOp.Expr)
	}
	if fa.Field.Name != "c" {
		t.Errorf("expected outer field 'c', got %q", fa.Field.Name)
	}
	if _, ok := fa.Object.(*ast.FieldAccess); !ok {
		t.Fatalf("expected nested FieldAccess (a.b) inside, got %T", fa.Object)
	}
}

func TestParseTryKeywordPosition(t *testing.T) {
	// TryOp.Line/Col point at the `try` keyword token.
	expr := parseExpr(t, "try foo()")
	tryOp := expr.(*ast.TryOp)
	if tryOp.Line != 1 || tryOp.Col != 1 {
		t.Errorf("expected try token at (1,1), got (%d,%d)", tryOp.Line, tryOp.Col)
	}
}

func TestPostfixQuestionIsParseError(t *testing.T) {
	// The postfix `?` operator was removed: `foo()?` is now a parse error.
	// `try` is the only error-propagation spelling.
	tokens := lexer.Lex("foo()?")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatalf("expected a parse error for postfix `?`, got none")
	}
	if !strings.Contains(err.Error(), "?") {
		t.Errorf("expected error to reference the stray `?` glyph, got %q", err.Error())
	}
}

func TestLexerTryLongestMatch(t *testing.T) {
	// `try_send` is a single IDENT, not the `try` keyword + `_send`.
	tokens := lexer.Lex("try_send = 1")
	if tokens[0].Type != token.IDENT {
		t.Fatalf("expected IDENT for try_send, got %s", tokens[0].Type)
	}
	if tokens[0].Lexeme != "try_send" {
		t.Errorf("expected lexeme 'try_send', got %q", tokens[0].Lexeme)
	}
	// Bare `try` is the keyword.
	kw := lexer.Lex("try")
	if kw[0].Type != token.TRY {
		t.Fatalf("expected TRY keyword for bare 'try', got %s", kw[0].Type)
	}
}

func TestStructDestructurePunning(t *testing.T) {
	nodes := parse(t, "{x, y} = point")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	sd, ok := nodes[0].(*ast.StructDestructure)
	if !ok {
		t.Fatalf("expected StructDestructure, got %T", nodes[0])
	}
	if len(sd.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(sd.Fields))
	}
	if sd.Fields[0].Name != "x" || sd.Fields[0].Binding != "x" {
		t.Errorf("field 0: expected x/x, got %s/%s", sd.Fields[0].Name, sd.Fields[0].Binding)
	}
	if sd.Fields[1].Name != "y" || sd.Fields[1].Binding != "y" {
		t.Errorf("field 1: expected y/y, got %s/%s", sd.Fields[1].Name, sd.Fields[1].Binding)
	}
}

func TestStructDestructureRename(t *testing.T) {
	nodes := parse(t, "{x: a, y: b} = point")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	sd, ok := nodes[0].(*ast.StructDestructure)
	if !ok {
		t.Fatalf("expected StructDestructure, got %T", nodes[0])
	}
	if sd.Fields[0].Name != "x" || sd.Fields[0].Binding != "a" {
		t.Errorf("field 0: expected x/a, got %s/%s", sd.Fields[0].Name, sd.Fields[0].Binding)
	}
	if sd.Fields[1].Name != "y" || sd.Fields[1].Binding != "b" {
		t.Errorf("field 1: expected y/b, got %s/%s", sd.Fields[1].Name, sd.Fields[1].Binding)
	}
}

func TestPatternAssertionBindingEnumPattern(t *testing.T) {
	nodes := parse(t, "assert Error.InvalidFormat(_) = error")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	pd, ok := nodes[0].(*ast.PatternDestructure)
	if !ok {
		t.Fatalf("expected PatternDestructure, got %T", nodes[0])
	}
	if _, ok := pd.Pattern.(*ast.EnumPattern); !ok {
		t.Fatalf("expected EnumPattern, got %T", pd.Pattern)
	}
	if pd.AssertLine != 1 || pd.AssertCol != 1 {
		t.Fatalf("expected assert keyword at 1:1, got %d:%d", pd.AssertLine, pd.AssertCol)
	}
}

func TestCaseAnonStructPattern(t *testing.T) {
	expr := parseExpr(t, `case point {
	{x: 1} -> "one"
	_ -> "other"
}`)
	ce, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected Case, got %T", expr)
	}
	sp, ok := ce.Branches[0].Pattern.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected StructPattern, got %T", ce.Branches[0].Pattern)
	}
	if sp.TypeName != nil {
		t.Errorf("expected nil TypeName for anon struct, got %v", sp.TypeName)
	}
	if len(sp.Fields) != 1 || sp.Fields[0].Name != "x" {
		t.Errorf("expected field x, got %+v", sp.Fields)
	}
}

func TestCaseAnonStructPatternBinding(t *testing.T) {
	expr := parseExpr(t, `case point {
	{x, y} -> x + y
	_ -> 0
}`)
	ce, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected Case, got %T", expr)
	}
	sp, ok := ce.Branches[0].Pattern.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected StructPattern, got %T", ce.Branches[0].Pattern)
	}
	if sp.TypeName != nil {
		t.Errorf("expected nil TypeName, got %v", sp.TypeName)
	}
	if len(sp.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(sp.Fields))
	}
	if sp.Fields[0].Name != "x" || sp.Fields[0].Binding != "x" {
		t.Errorf("field 0: expected x/x, got %s/%s", sp.Fields[0].Name, sp.Fields[0].Binding)
	}
}

// `_` in a struct-pattern field position is a wildcard — accepts any
// value for that field without binding. Both anon (`{a: _}`) and
// variant-qualified (`Foo.Bar{a: _}`) struct patterns accept it.
func TestCaseStructPatternWildcardField(t *testing.T) {
	expr := parseExpr(t, `case point {
  {x: 1, y: _} -> "x=1"
  _ -> "other"
}`)
	ce, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected Case, got %T", expr)
	}
	sp, ok := ce.Branches[0].Pattern.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected StructPattern, got %T", ce.Branches[0].Pattern)
	}
	if len(sp.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(sp.Fields))
	}
	if _, ok := sp.Fields[1].Pattern.(*ast.WildcardPattern); !ok {
		t.Fatalf("field 1: expected WildcardPattern, got %T", sp.Fields[1].Pattern)
	}
	if sp.Fields[1].Binding != "" {
		t.Errorf("field 1: wildcard should not bind a name, got %q", sp.Fields[1].Binding)
	}
}

func TestCaseStructPatternFloat(t *testing.T) {
	expr := parseExpr(t, `case point {
	{score: 3.14} -> "pi"
	_ -> "other"
}`)
	ce, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected Case, got %T", expr)
	}
	sp, ok := ce.Branches[0].Pattern.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected StructPattern, got %T", ce.Branches[0].Pattern)
	}
	fl, ok := sp.Fields[0].Pattern.(*ast.FloatLit)
	if !ok {
		t.Fatalf("expected FloatLit pattern, got %T", sp.Fields[0].Pattern)
	}
	if fl.Value != 3.14 {
		t.Errorf("expected 3.14, got %f", fl.Value)
	}
}

func TestCaseStructPatternNegativeInt(t *testing.T) {
	expr := parseExpr(t, `case point {
	{x: -1} -> "negative"
	_ -> "other"
}`)
	ce, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected Case, got %T", expr)
	}
	sp, ok := ce.Branches[0].Pattern.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected StructPattern, got %T", ce.Branches[0].Pattern)
	}
	il, ok := sp.Fields[0].Pattern.(*ast.IntLit)
	if !ok {
		t.Fatalf("expected IntLit pattern, got %T", sp.Fields[0].Pattern)
	}
	if il.Value != -1 {
		t.Errorf("expected -1, got %d", il.Value)
	}
}

func TestCaseNamedStructPatternFloat(t *testing.T) {
	expr := parseExpr(t, `case p {
	Point{score: 3.14} -> "pi"
	_ -> "other"
}`)
	ce, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected Case, got %T", expr)
	}
	sp, ok := ce.Branches[0].Pattern.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected StructPattern, got %T", ce.Branches[0].Pattern)
	}
	fl, ok := sp.Fields[0].Pattern.(*ast.FloatLit)
	if !ok {
		t.Fatalf("expected FloatLit pattern, got %T", sp.Fields[0].Pattern)
	}
	if fl.Value != 3.14 {
		t.Errorf("expected 3.14, got %f", fl.Value)
	}
}

func TestCaseNamedStructPatternNegativeInt(t *testing.T) {
	expr := parseExpr(t, `case p {
	Point{x: -1} -> "negative"
	_ -> "other"
}`)
	ce, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected Case, got %T", expr)
	}
	sp, ok := ce.Branches[0].Pattern.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected StructPattern, got %T", ce.Branches[0].Pattern)
	}
	il, ok := sp.Fields[0].Pattern.(*ast.IntLit)
	if !ok {
		t.Fatalf("expected IntLit pattern, got %T", sp.Fields[0].Pattern)
	}
	if il.Value != -1 {
		t.Errorf("expected -1, got %d", il.Value)
	}
}

// Map-distinct call-form pattern: `Kvs({"a" => v})` parses as an
// EnumPattern wrapping an anonymous MapPattern (TypeName=nil). Mirrors
// `Pair((a, b))` for tuple-distinct patterns. The literal-attach form
// `Kvs{"a" => v}` (a MapPattern with TypeName=Kvs) is unaffected.
func TestParser_MapPattern_CallForm(t *testing.T) {
	expr := parseExpr(t, `case kv {
	Kvs({"a" => v}) -> v
}`)
	ce, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected Case, got %T", expr)
	}
	ep, ok := ce.Branches[0].Pattern.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("expected EnumPattern, got %T", ce.Branches[0].Pattern)
	}
	if ep.Variant.TypeString() != "Kvs" {
		t.Errorf("expected variant Kvs, got %s", ep.Variant.TypeString())
	}
	mp, ok := ep.Payload.(*ast.MapPattern)
	if !ok {
		t.Fatalf("expected MapPattern payload, got %T", ep.Payload)
	}
	if mp.TypeName != nil {
		t.Errorf("expected anonymous MapPattern (TypeName=nil), got %s", mp.TypeName.TypeString())
	}
	if len(mp.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(mp.Entries))
	}
	k, ok := mp.Entries[0].Key.(*ast.StringLit)
	if !ok {
		t.Fatalf("expected StringLit key, got %T", mp.Entries[0].Key)
	}
	if k.Value != "a" {
		t.Errorf("expected key 'a', got %q", k.Value)
	}
	ip, ok := mp.Entries[0].Pattern.(*ast.IdentPattern)
	if !ok {
		t.Fatalf("expected IdentPattern, got %T", mp.Entries[0].Pattern)
	}
	if ip.Name != "v" {
		t.Errorf("expected 'v', got %s", ip.Name)
	}
}

func TestMapLiteral(t *testing.T) {
	expr := parseExpr(t, `{"alice" => 30, "bob" => 25}`)
	ml, ok := expr.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected MapLit, got %T", expr)
	}
	if len(ml.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(ml.Entries))
	}
	k0, ok := ml.Entries[0].Key.(*ast.StringLit)
	if !ok {
		t.Fatalf("expected StringLit key, got %T", ml.Entries[0].Key)
	}
	if k0.Value != "alice" {
		t.Errorf("expected key 'alice', got %q", k0.Value)
	}
}

func TestMapLiteralSingleEntry(t *testing.T) {
	expr := parseExpr(t, `{1 => "one"}`)
	ml, ok := expr.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected MapLit, got %T", expr)
	}
	if len(ml.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ml.Entries))
	}
}

func TestMapLiteralWithVariableKeys(t *testing.T) {
	expr := parseExpr(t, `{x => 1, y => 2}`)
	ml, ok := expr.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected MapLit, got %T", expr)
	}
	if len(ml.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(ml.Entries))
	}
}

// TypeName{"k" => v, ...} parses as a MapLit with a TypeName attached, NOT
// as a struct literal. The presence of `=>` (instead of `:`) after the first
// key disambiguates: brace literals with `=>` separators are map literals,
// brace literals with `:` are struct literals.
func TestParser_MapLit_TypePrefixed(t *testing.T) {
	expr := parseExpr(t, `Kvs{"a" => 1, "b" => 2}`)
	ml, ok := expr.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected MapLit, got %T", expr)
	}
	if ml.TypeName == nil {
		t.Fatalf("expected TypeName to be set")
	}
	if ml.TypeName.TypeString() != "Kvs" {
		t.Errorf("expected TypeName 'Kvs', got %q", ml.TypeName.TypeString())
	}
	if len(ml.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(ml.Entries))
	}
}

// Regression: TypeName{field: value, ...} still parses as a struct literal.
func TestParser_StructLit_TypePrefixed_StillWorks(t *testing.T) {
	expr := parseExpr(t, `Foo{a: 1, b: "x"}`)
	sl, ok := expr.(*ast.StructLit)
	if !ok {
		t.Fatalf("expected StructLit, got %T", expr)
	}
	if sl.TypeName == nil || sl.TypeName.TypeString() != "Foo" {
		t.Errorf("expected TypeName 'Foo', got %v", sl.TypeName)
	}
	if len(sl.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(sl.Fields))
	}
}

// A LITERAL BODY ATTACHES TO A MODULE-QUALIFIED NAME. At 41a60d35 none of
// these parsed as a literal: the chain stopped at the member access and the
// body became a separate anonymous struct or list, which the checker then
// reported as `non-final expression has type {r: Int}`.
//
// The boundary is the LITERAL-ATTACH forms specifically. `shapes.Shape.Square(4)`
// worked throughout because a call is a postfix form; `{` and `[` are only
// built by the primary parser, whose equivalent chain scan sat under
// `case token.TYPE_IDENT` and so never fired for a lowercase module head.
func TestParser_LiteralAttach_ModuleQualifiedHead(t *testing.T) {
	cases := []struct {
		name       string
		src        string
		wantModule string
		wantMember string
		wantFields int
		wantItems  int
		list       bool
	}{
		{
			name:       "two segments — module plus struct",
			src:        `shapes.Circle{r: 1}`,
			wantModule: "shapes",
			wantMember: "Circle",
			wantFields: 1,
		},
		{
			name:       "three segments — module plus enum, then the variant",
			src:        `shapes.Shape.Ring{r: 1, k: 2}`,
			wantModule: "shapes.Shape",
			wantMember: "Ring",
			wantFields: 2,
		},
		{
			// FOUR segments, and the middle two are one DOTTED TYPE NAME:
			// `pub enum Probe.Reading` reached through the module that
			// exports it. The owner is everything but the last segment
			// either way, so the scan needs no arity rule.
			name:       "four segments — a module plus a dotted enum name",
			src:        `telemetry.Probe.Reading.Blip{at: 9}`,
			wantModule: "telemetry.Probe.Reading",
			wantMember: "Blip",
			wantFields: 1,
		},
		{
			name:       "the bracket spelling, three segments",
			src:        `shapes.Bag.Items[1, 2, 3]`,
			wantModule: "shapes.Bag",
			wantMember: "Items",
			wantItems:  3,
			list:       true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr := parseExpr(t, tc.src)
			var typeName ast.TypeExpr
			if tc.list {
				ll, ok := expr.(*ast.ListLit)
				if !ok {
					t.Fatalf("expected ListLit, got %T", expr)
				}
				if len(ll.Items) != tc.wantItems {
					t.Fatalf("expected %d items, got %d", tc.wantItems, len(ll.Items))
				}
				typeName = ll.TypeName
			} else {
				sl, ok := expr.(*ast.StructLit)
				if !ok {
					t.Fatalf("expected StructLit, got %T", expr)
				}
				if len(sl.Fields) != tc.wantFields {
					t.Fatalf("expected %d fields, got %d", tc.wantFields, len(sl.Fields))
				}
				typeName = sl.TypeName
			}
			qt, ok := typeName.(*ast.QualifiedType)
			if !ok {
				t.Fatalf("expected a QualifiedType TypeName, got %T", typeName)
			}
			if qt.Module != tc.wantModule {
				t.Errorf("Module = %q, want %q", qt.Module, tc.wantModule)
			}
			if got := qt.Member.TypeString(); got != tc.wantMember {
				t.Errorf("Member = %q, want %q", got, tc.wantMember)
			}
		})
	}
}

// ONLY A BODY CLOSES THE CHAIN OFF. A dotted chain that ends in anything else
// is ordinary member access and the postfix parser owns it — taking it in the
// primary parser would change how `shapes.Shape.Square` and `mod.Enum.Variant(x)`
// parse. These rows are the reason the scan looks ahead for the terminator
// before consuming a single token.
func TestParser_LiteralAttach_ModuleQualifiedHeadLeavesMemberAccessAlone(t *testing.T) {
	for _, src := range []string{
		`shapes.Shape.Square`,
		`shapes.Shape.Square(4)`,
		`shapes.Circle`,
		`shapes.helper(1)`,
	} {
		t.Run(src, func(t *testing.T) {
			expr := parseExpr(t, src)
			switch expr.(type) {
			case *ast.StructLit, *ast.ListLit:
				t.Fatalf("%q was taken as a literal-attach; it is member access", src)
			}
		})
	}

	// A SECOND LOWERCASE SEGMENT IS NOT PART OF ANY NOMI NAME, so the scan
	// must not start at all: the chain is a lowercase head followed by
	// UPPERCASE segments. `a.b.Outer` stays member access and `{v: 1}` stays
	// its own anonymous struct — TWO statements, exactly as before the scan
	// existed. It gets its own assertion because `parseExpr` requires a
	// single statement and so cannot express a two-statement parse.
	t.Run("a.b.Outer{v: 1} stays two statements", func(t *testing.T) {
		nodes, err := Parse(lexer.Lex("fn main() {\n  a.b.Outer{v: 1}\n}\n"))
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		fn, ok := nodes[0].(*ast.FuncDef)
		if !ok {
			t.Fatalf("expected a FuncDef, got %T", nodes[0])
		}
		if fn.Body == nil || len(fn.Body.Stmts) != 2 {
			t.Fatalf("expected 2 statements in the body, got %v", fn.Body)
		}
		if _, isLit := fn.Body.Stmts[0].(*ast.StructLit); isLit {
			t.Fatal("the first statement was taken as a literal-attach")
		}
	})
}

func TestInterfaceDef(t *testing.T) {
	nodes := parse(t, `interface Display {
	fn ToString(value: self): String
}`)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	idef, ok := nodes[0].(*ast.InterfaceDef)
	if !ok {
		t.Fatalf("expected InterfaceDef, got %T", nodes[0])
	}
	if idef.Name != "Display" {
		t.Errorf("expected name Display, got %s", idef.Name)
	}
	if len(idef.Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(idef.Methods))
	}
	m := idef.Methods[0]
	if m.Name != "ToString" {
		t.Errorf("expected method ToString, got %s", m.Name)
	}
	if len(m.Params) != 1 || m.Params[0].Name != "value" || m.Params[0].TypeAnnotation == nil || m.Params[0].TypeAnnotation.TypeString() != "self" {
		t.Errorf("unexpected params: %+v", m.Params)
	}
	if m.ReturnTypeExpr == nil || m.ReturnTypeExpr.TypeString() != "String" {
		t.Errorf("expected return type String, got %v", m.ReturnTypeExpr)
	}
	if m.Body != nil {
		t.Error("expected nil body for abstract method")
	}
}

func TestInterfaceDefWithDefault(t *testing.T) {
	nodes := parse(t, `interface Formatted {
	fn format(value: self, style: String): String
	fn format_pretty(value: self): String {
		format(value, "pretty")
	}
}`)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	idef := nodes[0].(*ast.InterfaceDef)
	if len(idef.Methods) != 2 {
		t.Fatalf("expected 2 methods, got %d", len(idef.Methods))
	}
	if idef.Methods[0].Body != nil {
		t.Error("first method should be abstract")
	}
	if idef.Methods[1].Body == nil {
		t.Error("second method should have default body")
	}
}

// TestInterfaceMethodWhereClause verifies a method-level `where` clause on an
// interface method parses into InterfaceMethod.WhereClauses — the surface for
// constrained interface functions (e.g. `sort ... where T: Comparable`).
func TestInterfaceMethodWhereClause(t *testing.T) {
	nodes := parse(t, `interface Sortable<T> {
	fn sort<K>(source: self, _key: (T) -> K): List<T> where T: Comparable, K: Hashable {
		source
	}
}`)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	idef := nodes[0].(*ast.InterfaceDef)
	if len(idef.Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(idef.Methods))
	}
	m := idef.Methods[0]
	if m.Body == nil {
		t.Fatal("expected default body after where clause")
	}
	if len(m.TypeParams) != 1 || m.TypeParams[0].Name != "K" {
		t.Fatalf("expected method type param K, got %#v", m.TypeParams)
	}
	if len(m.WhereClauses) != 2 {
		t.Fatalf("expected 2 where constraints, got %d", len(m.WhereClauses))
	}
	wc := m.WhereClauses[0]
	if wc.Name != "T" {
		t.Errorf("expected where constraint on T, got %q", wc.Name)
	}
	if len(wc.Bounds) != 1 {
		t.Fatalf("expected 1 bound, got %d", len(wc.Bounds))
	}
	if wc.Bounds[0].TypeString() != "Comparable" {
		t.Errorf("expected bound Comparable, got %q", wc.Bounds[0].TypeString())
	}
	if m.WhereClauses[1].Name != "K" || len(m.WhereClauses[1].Bounds) != 1 || m.WhereClauses[1].Bounds[0].TypeString() != "Hashable" {
		t.Fatalf("expected K: Hashable, got %#v", m.WhereClauses[1])
	}
}

// TestInterfaceMethodWhereClauseMultiple verifies a where clause with several
// constraints and an `and`-joined bound list parses fully.
func TestInterfaceMethodWhereClauseMultiple(t *testing.T) {
	nodes := parse(t, `interface Keyed<T> {
	fn group<K>(source: self, _key: (T) -> K): List<T> where T: Comparable, K: Hashable and Equatable {
		source
	}
}`)
	m := nodes[0].(*ast.InterfaceDef).Methods[0]
	if len(m.TypeParams) != 1 || m.TypeParams[0].Name != "K" {
		t.Fatalf("expected method type param K, got %#v", m.TypeParams)
	}
	if len(m.WhereClauses) != 2 {
		t.Fatalf("expected 2 where constraints, got %d", len(m.WhereClauses))
	}
	if m.WhereClauses[0].Name != "T" || len(m.WhereClauses[0].Bounds) != 1 {
		t.Errorf("constraint 0: got %q with %d bounds", m.WhereClauses[0].Name, len(m.WhereClauses[0].Bounds))
	}
	if m.WhereClauses[1].Name != "K" || len(m.WhereClauses[1].Bounds) != 2 {
		t.Errorf("constraint 1: got %q with %d bounds", m.WhereClauses[1].Name, len(m.WhereClauses[1].Bounds))
	}
}

func TestInterfaceMethodWhereClauseOnRequiredMethod(t *testing.T) {
	nodes := parse(t, `interface Sortable<T> {
	fn sort(source: self): List<T> where T: Comparable
}`)
	m := nodes[0].(*ast.InterfaceDef).Methods[0]
	if m.Body != nil {
		t.Fatal("expected required method, got default body")
	}
	if len(m.WhereClauses) != 1 || m.WhereClauses[0].Name != "T" {
		t.Fatalf("expected required method where T, got %#v", m.WhereClauses)
	}
}

func TestFuncDefWhereClause(t *testing.T) {
	nodes := parse(t, `fn step_by<T, S>(
	r: Range<T>,
	_by: S,
): Iter<T>
where T: Comparable and Steppable<S> {
	r
}`)
	fn := nodes[0].(*ast.FuncDef)
	if len(fn.WhereClauses) != 1 {
		t.Fatalf("expected 1 where constraint, got %d", len(fn.WhereClauses))
	}
	wc := fn.WhereClauses[0]
	if wc.Name != "T" {
		t.Errorf("expected where constraint on T, got %q", wc.Name)
	}
	if len(wc.Bounds) != 2 {
		t.Fatalf("expected 2 bounds, got %d", len(wc.Bounds))
	}
	if got := wc.Bounds[1].TypeString(); got != "Steppable<S>" {
		t.Errorf("expected generic bound Steppable<S>, got %q", got)
	}
}

func TestExternFuncWhereClause(t *testing.T) {
	nodes := parse(t, `host fn sorted<T>(items: List<T>): List<T> where T: Comparable`)
	ef := nodes[0].(*ast.ExternFunc)
	if len(ef.WhereClauses) != 1 {
		t.Fatalf("expected 1 where constraint, got %d", len(ef.WhereClauses))
	}
	if ef.WhereClauses[0].Name != "T" {
		t.Errorf("expected where constraint on T, got %q", ef.WhereClauses[0].Name)
	}
}

func TestStructDefPublic(t *testing.T) {
	nodes := parse(t, `pub struct User { name: String }`)
	var sd *ast.StructDef
	for _, n := range nodes {
		if v, ok := n.(*ast.StructDef); ok {
			sd = v
			break
		}
	}
	if sd == nil {
		t.Fatal("expected *ast.StructDef among nodes")
	}
	if !sd.Public {
		t.Error("expected pub struct to be public")
	}
}

func TestStructDefPrivate(t *testing.T) {
	nodes := parse(t, `struct Config { name: String }`)
	sd := nodes[0].(*ast.StructDef)
	if sd.Public {
		t.Error("expected struct without `pub` modifier to be private")
	}
}

// TestParser_StructDef_Basic verifies that `struct Foo { a: Int; b: String }`
// parses to a StructDef with two fields, public flag, and field types.
func TestParser_StructDef_Basic(t *testing.T) {
	nodes := parse(t, `pub struct Foo { a: Int; b: String }`)
	var sd *ast.StructDef
	for _, n := range nodes {
		if v, ok := n.(*ast.StructDef); ok {
			sd = v
			break
		}
	}
	if sd == nil {
		t.Fatal("expected *ast.StructDef among nodes")
	}
	if sd.Name != "Foo" {
		t.Errorf("expected name Foo, got %q", sd.Name)
	}
	if !sd.Public {
		t.Error("expected public")
	}
	if len(sd.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(sd.Fields))
	}
	if sd.Fields[0].Name != "a" || sd.Fields[0].TypeAnnotation.TypeString() != "Int" {
		t.Errorf("field 0 = %s: %v", sd.Fields[0].Name, sd.Fields[0].TypeAnnotation)
	}
	if sd.Fields[1].Name != "b" || sd.Fields[1].TypeAnnotation.TypeString() != "String" {
		t.Errorf("field 1 = %s: %v", sd.Fields[1].Name, sd.Fields[1].TypeAnnotation)
	}
}

// TestParser_StructDef_Generic verifies generic struct declarations of
// the form `struct Box<T> { v: T }`.
func TestParser_StructDef_Generic(t *testing.T) {
	nodes := parse(t, `struct Box<T> { v: T }`)
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("expected StructDef, got %T", nodes[0])
	}
	if sd.Name != "Box" {
		t.Errorf("expected name Box, got %q", sd.Name)
	}
	if len(sd.TypeParams) != 1 || sd.TypeParams[0].Name != "T" {
		t.Errorf("TypeParams = %v, want ['T]", sd.TypeParams)
	}
	if len(sd.Fields) != 1 || sd.Fields[0].Name != "v" || sd.Fields[0].TypeAnnotation.TypeString() != "T" {
		t.Errorf("expected single field v: T, got %v", sd.Fields)
	}
}

// TestParser_StructDef_FieldDefault verifies field defaults survive
// `struct Foo { a: Int = 0 }`.
func TestParser_StructDef_FieldDefault(t *testing.T) {
	nodes := parse(t, `struct Foo { a: Int = 0 }`)
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("expected StructDef, got %T", nodes[0])
	}
	if len(sd.Fields) != 1 || sd.Fields[0].Default == nil {
		t.Fatalf("expected field a with default, got %+v", sd.Fields)
	}
}

// TestParser_StructDef_Empty verifies that the empty-brace form
// `struct Empty {}` yields a zero-field StructDef.
func TestParser_StructDef_Empty(t *testing.T) {
	nodes := parse(t, `struct Empty {}`)
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("expected StructDef, got %T", nodes[0])
	}
	if sd.Name != "Empty" {
		t.Errorf("expected Empty, got %q", sd.Name)
	}
	if len(sd.Fields) != 0 {
		t.Errorf("expected 0 fields, got %d", len(sd.Fields))
	}
}

// TestParser_StructDef_Private verifies that a struct declared without
// `pub` parses with Public=false.
func TestParser_StructDef_Private(t *testing.T) {
	nodes := parse(t, `struct Foo { a: Int }`)
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("expected StructDef, got %T", nodes[0])
	}
	if sd.Public {
		t.Error("expected private (no pub keyword)")
	}
	if sd.Name != "Foo" || len(sd.Fields) != 1 {
		t.Errorf("unexpected struct: %+v", sd)
	}
}

// TestParser_EnumDef_Generic verifies that `enum Maybe<T> { None; Some T }`
// parses to an EnumDef with the expected name, public flag, type params,
// and a bare + positional variant.
func TestParser_EnumDef_Generic(t *testing.T) {
	src := "pub enum Maybe<T> { None; Some T }"
	nodes := parse(t, src)
	var ed *ast.EnumDef
	for _, n := range nodes {
		if v, ok := n.(*ast.EnumDef); ok {
			ed = v
			break
		}
	}
	if ed == nil {
		t.Fatal("expected *ast.EnumDef among nodes")
	}
	if ed.Name != "Maybe" {
		t.Errorf("expected name Maybe, got %q", ed.Name)
	}
	if !ed.Public {
		t.Error("expected public")
	}
	if len(ed.TypeParams) != 1 || ed.TypeParams[0].Name != "T" {
		t.Errorf("TypeParams = %v, want ['T]", ed.TypeParams)
	}
	if len(ed.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(ed.Variants))
	}
	if ed.Variants[0].Name != "None" || ed.Variants[0].Kind != "bare" {
		t.Errorf("variant 0 = %s/%s, want None/bare", ed.Variants[0].Name, ed.Variants[0].Kind)
	}
	if ed.Variants[1].Name != "Some" || ed.Variants[1].Kind != "positional" {
		t.Errorf("variant 1 = %s/%s, want Some/positional", ed.Variants[1].Name, ed.Variants[1].Kind)
	}
}

// TestParser_EnumDef_EmptyBodyRejected verifies that an empty enum body
// (`enum Foo {}`) is rejected — every enum must have at least one variant.
func TestParser_EnumDef_EmptyBodyRejected(t *testing.T) {
	tokens := lexer.Lex(`enum Foo {}`)
	if _, err := Parse(tokens); err == nil {
		t.Fatal("expected parse error for empty enum body, got none")
	}
}

// TestParser_EnumDef_EmbedsVariant verifies an enum body accepts
// `embeds TypeName` variants alongside bare variants.
func TestParser_EnumDef_EmbedsVariant(t *testing.T) {
	src := "enum Drawable { embeds Circle; Line }"
	nodes := parse(t, src)
	ed, ok := nodes[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("expected *ast.EnumDef, got %T", nodes[0])
	}
	if len(ed.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(ed.Variants))
	}
	if ed.Variants[0].Kind != "embedded" || ed.Variants[0].Name != "Circle" {
		t.Errorf("variant 0 = %s/%s, want Circle/embedded", ed.Variants[0].Name, ed.Variants[0].Kind)
	}
	if ed.Variants[1].Kind != "bare" || ed.Variants[1].Name != "Line" {
		t.Errorf("variant 1 = %s/%s, want Line/bare", ed.Variants[1].Name, ed.Variants[1].Kind)
	}
}

// TestParser_EnumDef_StructPayloadVariant verifies the struct-payload
// variant form (`Rectangle{w: Float, h: Float}`) parses inside an enum body.
func TestParser_EnumDef_StructPayloadVariant(t *testing.T) {
	src := "enum Shape {\n  Rectangle{w: Float, h: Float}\n  Point\n}"
	nodes := parse(t, src)
	ed, ok := nodes[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("expected *ast.EnumDef, got %T", nodes[0])
	}
	if len(ed.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(ed.Variants))
	}
	if ed.Variants[0].Kind != "struct" || len(ed.Variants[0].Fields) != 2 {
		t.Errorf("variant 0 = %s/%s with %d fields, want Rectangle/struct with 2 fields",
			ed.Variants[0].Name, ed.Variants[0].Kind, len(ed.Variants[0].Fields))
	}
	if ed.Variants[1].Kind != "bare" || ed.Variants[1].Name != "Point" {
		t.Errorf("variant 1 = %s/%s, want Point/bare", ed.Variants[1].Name, ed.Variants[1].Kind)
	}
}

// TestParser_EnumDef_SingleVariant verifies the minimum-viable sum type
// (one bare variant, no `|` separator) parses to an EnumDef.
func TestParser_EnumDef_SingleVariant(t *testing.T) {
	nodes := parse(t, "enum Foo { A }")
	ed, ok := nodes[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("expected *ast.EnumDef, got %T", nodes[0])
	}
	if len(ed.Variants) != 1 || ed.Variants[0].Name != "A" || ed.Variants[0].Kind != "bare" {
		t.Errorf("expected one bare variant A, got %+v", ed.Variants)
	}
}

func TestEnumDefPublic(t *testing.T) {
	nodes := parse(t, `pub enum Color { Red; Green; Blue }`)
	var ed *ast.EnumDef
	for _, n := range nodes {
		if v, ok := n.(*ast.EnumDef); ok {
			ed = v
			break
		}
	}
	if ed == nil {
		t.Fatal("expected *ast.EnumDef among nodes")
	}
	if !ed.Public {
		t.Error("expected pub enum to be public")
	}
}

// TestParser_StructKeyword verifies the `struct` keyword produces a
// StructDef with the expected name, public flag, and fields.
func TestParser_StructKeyword(t *testing.T) {
	nodes := parse(t, "pub struct Point { x: Int; y: Int }")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("expected *ast.StructDef, got %T", nodes[0])
	}
	if !sd.Public {
		t.Error("expected pub struct to set Public=true")
	}
	if sd.Name != "Point" {
		t.Errorf("expected name Point, got %s", sd.Name)
	}
	if len(sd.Fields) != 2 || sd.Fields[0].Name != "x" || sd.Fields[1].Name != "y" {
		t.Errorf("expected fields x, y; got %+v", sd.Fields)
	}
}

// TestParser_EnumKeyword verifies the `enum` keyword with a body of
// `variant` items produces an EnumDef.
func TestParser_EnumKeyword(t *testing.T) {
	nodes := parse(t, "pub enum Color { Red; Green; Blue }")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	ed, ok := nodes[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("expected *ast.EnumDef, got %T", nodes[0])
	}
	if !ed.Public {
		t.Error("expected pub enum to set Public=true")
	}
	if ed.Name != "Color" {
		t.Errorf("expected name Color, got %s", ed.Name)
	}
	if len(ed.Variants) != 3 {
		t.Fatalf("expected 3 variants, got %d", len(ed.Variants))
	}
	if ed.Variants[0].Name != "Red" || ed.Variants[1].Name != "Green" || ed.Variants[2].Name != "Blue" {
		t.Errorf("unexpected variant names: %+v", ed.Variants)
	}
}

// TestParser_EnumSingleVariant verifies a single-variant enum parses
// successfully.
func TestParser_EnumSingleVariant(t *testing.T) {
	nodes := parse(t, "enum Singleton { Only }")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	ed, ok := nodes[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("expected *ast.EnumDef, got %T", nodes[0])
	}
	if len(ed.Variants) != 1 || ed.Variants[0].Name != "Only" {
		t.Errorf("expected single variant Only, got %+v", ed.Variants)
	}
}

func TestInterfaceDefPublic(t *testing.T) {
	nodes := parse(t, `pub interface Display {
	fn to_string(value: self): String
}`)
	var id *ast.InterfaceDef
	for _, n := range nodes {
		if v, ok := n.(*ast.InterfaceDef); ok {
			id = v
			break
		}
	}
	if id == nil {
		t.Fatal("expected *ast.InterfaceDef among nodes")
	}
	if !id.Public {
		t.Error("expected pub interface to be public")
	}
}

func TestImportFileNamespace(t *testing.T) {
	nodes := parse(t, `import std/io`)
	imp, ok := nodes[0].(*ast.ImportStmt)
	if !ok {
		t.Fatalf("expected ImportStmt, got %T", nodes[0])
	}
	if len(imp.ModulePath) != 2 ||
		ast.ImportNodeName(imp.ModulePath[0]) != "std" ||
		ast.ImportNodeName(imp.ModulePath[1]) != "io" {
		t.Errorf("expected path [std io], got %v", imp.ModulePath)
	}
	if len(imp.Names) != 0 {
		t.Errorf("expected no selective names, got %v", imp.Names)
	}
	if imp.ModuleAlias != nil {
		t.Errorf("expected no module alias, got %v", imp.ModuleAlias)
	}
}

func TestImportRelativeParentModule(t *testing.T) {
	nodes := parse(t, `import ../test_helpers: TestHelpers`)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	imp, ok := nodes[0].(*ast.ImportStmt)
	if !ok {
		t.Fatalf("expected ImportStmt, got %T", nodes[0])
	}
	if len(imp.ModulePath) != 2 ||
		ast.ImportNodeName(imp.ModulePath[0]) != ".." ||
		ast.ImportNodeName(imp.ModulePath[1]) != "test_helpers" {
		t.Errorf("expected path [.. test_helpers], got %v", imp.ModulePath)
	}
	if len(imp.Names) != 1 || ast.ImportNodeName(imp.Names[0]) != "TestHelpers" {
		t.Errorf("expected names [TestHelpers], got %v", imp.Names)
	}
}

func TestImportSpecific(t *testing.T) {
	nodes := parse(t, `import Users.User`)
	imp := nodes[0].(*ast.ImportStmt)
	if len(imp.ModulePath) != 1 || ast.ImportNodeName(imp.ModulePath[0]) != "Users" {
		t.Errorf("expected path [Users], got %v", imp.ModulePath)
	}
	if len(imp.Names) != 1 || ast.ImportNodeName(imp.Names[0]) != "User" {
		t.Errorf("expected names [User], got %v", imp.Names)
	}
}

func TestImportSelective(t *testing.T) {
	nodes := parse(t, `import Http.{Request, Response}`)
	imp := nodes[0].(*ast.ImportStmt)
	if len(imp.ModulePath) != 1 || ast.ImportNodeName(imp.ModulePath[0]) != "Http" {
		t.Errorf("expected path [Http], got %v", imp.ModulePath)
	}
	if len(imp.Names) != 2 || ast.ImportNodeName(imp.Names[0]) != "Request" || ast.ImportNodeName(imp.Names[1]) != "Response" {
		t.Errorf("expected names [Request, Response], got %v", imp.Names)
	}
}

// Block form: entries separated by newlines (the canonical form),
// terminated by `}`.
func TestImportBlock_NewlineSeparated(t *testing.T) {
	nodes := parse(t, "import {\n  std/lists: List\n  std/maps: Map\n}")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	blk, ok := nodes[0].(*ast.ImportBlock)
	if !ok {
		t.Fatalf("expected ImportBlock, got %T", nodes[0])
	}
	if len(blk.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(blk.Entries))
	}
	if ast.ImportNodeName(blk.Entries[0].ModulePath[1]) != "lists" {
		t.Errorf("expected first entry std/lists, got %v", blk.Entries[0].ModulePath)
	}
	if ast.ImportNodeName(blk.Entries[1].ModulePath[1]) != "maps" {
		t.Errorf("expected second entry std/maps, got %v", blk.Entries[1].ModulePath)
	}
}

// A comma between block entries is rejected with a helpful message — commas
// belong to the per-import selective list, not the import block.
func TestImportBlock_CommaRejected(t *testing.T) {
	_, err := Parse(lexer.Lex("import { std/lists: List, std/maps: Map }"))
	if err == nil {
		t.Fatal("expected comma-separated import block to be rejected")
	}
	if !strings.Contains(err.Error(), "expected") {
		t.Errorf("expected a helpful parse message, got: %v", err)
	}
}

// `self` inside a selective brace list binds the brace-list owner alongside
// the named items. For a file-level selector, the owner is the file API object.
func TestImportSelfMarker_FileNamespace(t *testing.T) {
	nodes := parse(t, `import std/maybe.{self, Maybe}`)
	stmt := nodes[0].(*ast.ImportStmt)
	if !stmt.IncludeParent {
		t.Fatal("expected self marker to set IncludeParent")
	}
	if got := importNodeNames(stmt.ModulePath); !reflect.DeepEqual(got, []string{"std", "maybe"}) {
		t.Fatalf("ModulePath = %v, want [std maybe]", got)
	}
	if got := importNodeNames(stmt.Names); !reflect.DeepEqual(got, []string{"Maybe"}) {
		t.Fatalf("Names = %v, want [Maybe]", got)
	}
}

func TestImportSelfMarker_DrillThrough(t *testing.T) {
	nodes := parse(t, `import std/maybe.Maybe.{self, None, Some}`)
	imp := nodes[0].(*ast.ImportStmt)
	if !imp.IncludeParent {
		t.Errorf("expected IncludeParent=true on `Maybe.{self, None, Some}`")
	}
	if len(imp.Names) != 2 || ast.ImportNodeName(imp.Names[0]) != "None" || ast.ImportNodeName(imp.Names[1]) != "Some" {
		t.Errorf("expected names [None, Some], got %v", imp.Names)
	}
}

func TestImportSelfMarker_RejectsAlias(t *testing.T) {
	tokens := lexer.Lex(`import std/foo.{self as bar}`)
	_, errs := ParseWithRecovery(tokens)
	if len(errs) == 0 {
		t.Fatal("expected parse error for `self as bar` — self is a marker, not a renamable name")
	}
}

func TestImportNestedModule(t *testing.T) {
	nodes := parse(t, `import Http.Request`)
	imp := nodes[0].(*ast.ImportStmt)
	if len(imp.ModulePath) != 1 || ast.ImportNodeName(imp.ModulePath[0]) != "Http" {
		t.Errorf("expected path [Http], got %v", imp.ModulePath)
	}
	if len(imp.Names) != 1 || ast.ImportNodeName(imp.Names[0]) != "Request" {
		t.Errorf("expected names [Request], got %v", imp.Names)
	}
}

func TestImportFileNamespaceAlias(t *testing.T) {
	nodes := parse(t, `import std/lists as l`)
	imp, ok := nodes[0].(*ast.ImportStmt)
	if !ok {
		t.Fatalf("expected ImportStmt, got %T", nodes[0])
	}
	if len(imp.ModulePath) != 2 ||
		ast.ImportNodeName(imp.ModulePath[0]) != "std" ||
		ast.ImportNodeName(imp.ModulePath[1]) != "lists" {
		t.Errorf("expected path [std lists], got %v", imp.ModulePath)
	}
	if imp.ModuleAlias == nil || ast.ImportNodeName(imp.ModuleAlias) != "l" {
		t.Errorf("expected module alias l, got %v", imp.ModuleAlias)
	}
	if len(imp.Names) != 0 {
		t.Errorf("expected no selective names, got %v", imp.Names)
	}
}

func TestImportSelectiveAlias(t *testing.T) {
	nodes := parse(t, `import std/maybe.{Maybe as Opt, Some as Just, None}`)
	imp := nodes[0].(*ast.ImportStmt)
	if len(imp.Names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(imp.Names))
	}
	if len(imp.Aliases) != 3 {
		t.Fatalf("expected 3 alias slots, got %d", len(imp.Aliases))
	}
	names := []string{
		ast.ImportNodeName(imp.Names[0]),
		ast.ImportNodeName(imp.Names[1]),
		ast.ImportNodeName(imp.Names[2]),
	}
	if names[0] != "Maybe" || names[1] != "Some" || names[2] != "None" {
		t.Errorf("unexpected names: %v", names)
	}
	if imp.Aliases[0] == nil || ast.ImportNodeName(imp.Aliases[0]) != "Opt" {
		t.Errorf("expected Maybe aliased to Opt, got %v", imp.Aliases[0])
	}
	if imp.Aliases[1] == nil || ast.ImportNodeName(imp.Aliases[1]) != "Just" {
		t.Errorf("expected Some aliased to Just, got %v", imp.Aliases[1])
	}
	if imp.Aliases[2] != nil {
		t.Errorf("expected no alias for None, got %v", imp.Aliases[2])
	}
}

func TestImportPerItemExport(t *testing.T) {
	nodes := parse(t, `import some_mod.{Thing export as Tng, other}`)
	imp := nodes[0].(*ast.ImportStmt)
	if len(imp.Names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(imp.Names))
	}
	if len(imp.ExportFlags) != 2 {
		t.Fatalf("expected 2 export flags, got %d", len(imp.ExportFlags))
	}
	if !imp.ExportFlags[0] {
		t.Errorf("expected ExportFlags[0] = true")
	}
	if imp.ExportFlags[1] {
		t.Errorf("expected ExportFlags[1] = false")
	}
	if len(imp.ExportAliases) != 2 {
		t.Fatalf("expected 2 export aliases slots, got %d", len(imp.ExportAliases))
	}
	if imp.ExportAliases[0] == nil || ast.ImportNodeName(imp.ExportAliases[0]) != "Tng" {
		t.Errorf("expected ExportAliases[0] = 'Tng', got %v", imp.ExportAliases[0])
	}
	if imp.ExportAliases[1] != nil {
		t.Errorf("expected ExportAliases[1] = nil, got %v", imp.ExportAliases[1])
	}
}

func TestImportPerItemExportNoRename(t *testing.T) {
	nodes := parse(t, `import some_mod.{helper export}`)
	imp := nodes[0].(*ast.ImportStmt)
	if len(imp.ExportFlags) != 1 || !imp.ExportFlags[0] {
		t.Errorf("expected ExportFlags = [true], got %v", imp.ExportFlags)
	}
	if len(imp.ExportAliases) != 1 || imp.ExportAliases[0] != nil {
		t.Errorf("expected ExportAliases = [nil], got %v", imp.ExportAliases)
	}
}

func TestImportLocalRenameAndExportRename(t *testing.T) {
	nodes := parse(t, `import some_mod.{Widget as W export as WPub}`)
	imp := nodes[0].(*ast.ImportStmt)
	if len(imp.Aliases) != 1 || imp.Aliases[0] == nil || ast.ImportNodeName(imp.Aliases[0]) != "W" {
		t.Errorf("expected Aliases[0] = 'W', got %v", imp.Aliases)
	}
	if len(imp.ExportFlags) != 1 || !imp.ExportFlags[0] {
		t.Errorf("expected ExportFlags = [true], got %v", imp.ExportFlags)
	}
	if len(imp.ExportAliases) != 1 || imp.ExportAliases[0] == nil || ast.ImportNodeName(imp.ExportAliases[0]) != "WPub" {
		t.Errorf("expected ExportAliases[0] = 'WPub', got %v", imp.ExportAliases)
	}
}

func TestImportNoExportByDefault(t *testing.T) {
	nodes := parse(t, `import some_mod.{Thing, other}`)
	imp := nodes[0].(*ast.ImportStmt)
	if len(imp.ExportFlags) != 2 {
		t.Fatalf("expected 2 export flag slots, got %d", len(imp.ExportFlags))
	}
	if imp.ExportFlags[0] || imp.ExportFlags[1] {
		t.Errorf("expected all ExportFlags = false, got %v", imp.ExportFlags)
	}
	if len(imp.ExportAliases) != 2 {
		t.Fatalf("expected 2 export alias slots, got %d", len(imp.ExportAliases))
	}
	if imp.ExportAliases[0] != nil || imp.ExportAliases[1] != nil {
		t.Errorf("expected all ExportAliases = nil, got %v", imp.ExportAliases)
	}
}

func TestImportPerItemExportMissingAliasName(t *testing.T) {
	tokens := lexer.Lex(`import some_mod.{Thing export as}`)
	if _, err := Parse(tokens); err == nil {
		t.Fatal("expected error for missing identifier after 'export as'")
	}
}

func TestParseImport_LineLevelExport_FlatSelectorList(t *testing.T) {
	nodes := parse(t, `import some_mod: a, b, c export`)
	imp, ok := nodes[0].(*ast.ImportStmt)
	if !ok {
		t.Fatalf("expected *ast.ImportStmt, got %T", nodes[0])
	}
	if !imp.ExportAll {
		t.Errorf("expected ExportAll = true")
	}
	if imp.ExportAlias != nil {
		t.Errorf("expected ExportAlias = nil, got %v", imp.ExportAlias)
	}
	if len(imp.Names) != 3 {
		t.Errorf("expected 3 names, got %d", len(imp.Names))
	}
}

func TestParseImport_LineLevelExport_BracedFlatSelectorList(t *testing.T) {
	nodes := parse(t, `import some_mod: {a, b, c} export`)
	imp, ok := nodes[0].(*ast.ImportStmt)
	if !ok {
		t.Fatalf("expected *ast.ImportStmt, got %T", nodes[0])
	}
	if !imp.ExportAll {
		t.Errorf("expected ExportAll = true")
	}
	if len(imp.Names) != 3 {
		t.Errorf("expected 3 names, got %d", len(imp.Names))
	}
}

func TestParseImport_LineLevelExport_MultiSelectorRenameRejected(t *testing.T) {
	tokens := lexer.Lex(`import some_mod: a, b export as foo`)
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected error for 'export as <name>' after a multi-name selector list")
	}
	msg := err.Error()
	if !strings.Contains(msg, "export") || !strings.Contains(msg, "per-item") {
		t.Errorf("expected error mentioning 'export' and 'per-item', got %q", msg)
	}
}

func TestParseImport_NoLineLevelExportByDefault(t *testing.T) {
	nodes := parse(t, `import some_mod.{a, b}`)
	imp, ok := nodes[0].(*ast.ImportStmt)
	if !ok {
		t.Fatalf("expected *ast.ImportStmt, got %T", nodes[0])
	}
	if imp.ExportAll {
		t.Errorf("expected ExportAll = false")
	}
}

// TestParseImport_PerItemAndLineLevelExportCombined verifies the
// interaction between per-item `export [as <Pub>]` (B.2) and the
// line-level `export` shorthand (B.3). The plan permits the redundancy:
// per-item rename wins for that item, line-level applies to the rest.
// Both paths produce the same effect for items without a per-item rename
// (re-export under the imported name), so this is silently consistent.
func TestParseImport_PerItemAndLineLevelExportCombined(t *testing.T) {
	nodes := parse(t, `import some_mod.{a export as aa, b} export`)
	imp, ok := nodes[0].(*ast.ImportStmt)
	if !ok {
		t.Fatalf("expected *ast.ImportStmt, got %T", nodes[0])
	}
	if !imp.ExportAll {
		t.Errorf("expected ExportAll = true (line-level export present)")
	}
	if len(imp.ExportFlags) != 2 || !imp.ExportFlags[0] || imp.ExportFlags[1] {
		t.Errorf("expected ExportFlags = [true, false], got %v", imp.ExportFlags)
	}
	if len(imp.ExportAliases) != 2 {
		t.Fatalf("expected len(ExportAliases) = 2, got %d", len(imp.ExportAliases))
	}
	alias, ok := imp.ExportAliases[0].(*ast.Ident)
	if !ok {
		t.Fatalf("expected ExportAliases[0] to be *ast.Ident, got %T", imp.ExportAliases[0])
	}
	if alias.Name != "aa" {
		t.Errorf("expected ExportAliases[0].Name = 'aa', got %q", alias.Name)
	}
	if imp.ExportAliases[1] != nil {
		t.Errorf("expected ExportAliases[1] = nil (no per-item rename on b)")
	}
}

// TestParseImportSlashPaths covers the slash separator between module-path
// segments. Bilingual phase: parser accepts both `/` and `.`. Selective
// braces, drill-through types, and aliases interact with `/` the same way
// they do with `.` (the `.` boundary stays where it always was — between
// the resolved module and a member).
func TestParseImportSlashPaths(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantPath  []string
		wantNames []string
		wantAlias string
	}{
		{"bare slash path", "import std/lists", []string{"std", "lists"}, nil, ""},
		{"bare slash path alias", "import std/lists as l", []string{"std", "lists"}, nil, "l"},
		{"slash with selective brace", "import std/lists.{head, tail}", []string{"std", "lists"}, []string{"head", "tail"}, ""},
		{"slash with drill-through", "import std/maybe.Maybe.{Some, None}", []string{"std", "maybe", "Maybe"}, []string{"Some", "None"}, ""},
		{"slash with lower-case single selective", "import std/io.print", []string{"std", "io"}, []string{"print"}, ""},
		{"slash with single selective", "import models/users.User", []string{"models", "users"}, []string{"User"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodes := parse(t, tc.src)
			imp, ok := nodes[0].(*ast.ImportStmt)
			if !ok {
				t.Fatalf("expected *ast.ImportStmt, got %T", nodes[0])
			}
			gotPath := make([]string, len(imp.ModulePath))
			for i, n := range imp.ModulePath {
				gotPath[i] = ast.ImportNodeName(n)
			}
			if !equalStringSlices(gotPath, tc.wantPath) {
				t.Errorf("ModulePath = %v, want %v", gotPath, tc.wantPath)
			}
			gotNames := make([]string, len(imp.Names))
			for i, n := range imp.Names {
				gotNames[i] = ast.ImportNodeName(n)
			}
			if !equalStringSlices(gotNames, tc.wantNames) {
				t.Errorf("Names = %v, want %v", gotNames, tc.wantNames)
			}
			gotAlias := ""
			if imp.ModuleAlias != nil {
				gotAlias = ast.ImportNodeName(imp.ModuleAlias)
			}
			if gotAlias != tc.wantAlias {
				t.Errorf("ModuleAlias = %q, want %q", gotAlias, tc.wantAlias)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestParseImportTreatsDotAsSelector verifies that `.` is the API boundary:
// the resolved path is `std`, and `io` is the selected name.
func TestParseImportTreatsDotAsSelector(t *testing.T) {
	nodes := parse(t, "import std.io\n")
	imp, ok := nodes[0].(*ast.ImportStmt)
	if !ok {
		t.Fatalf("expected *ast.ImportStmt, got %T", nodes[0])
	}
	if got := importNodeNames(imp.ModulePath); !equalStringSlices(got, []string{"std"}) {
		t.Errorf("ModulePath = %v, want [std]", got)
	}
	if got := importNodeNames(imp.Names); !equalStringSlices(got, []string{"io"}) {
		t.Errorf("Names = %v, want [io]", got)
	}
}

// TestParseImportAllowsDottedDrillThrough confirms `.` is still valid as
// the boundary between the resolved module and a drill-through type.
func TestParseImportAllowsDottedDrillThrough(t *testing.T) {
	if _, err := Parse(lexer.Lex("import std/maybe.Maybe.{Some, None}\n")); err != nil {
		t.Fatalf("drill-through must still parse: %v", err)
	}
}

func TestParseImportAllowsWrappedSelectorList(t *testing.T) {
	nodes := parse(t, `import std/calendar.{
  DateParts,
  Days,
  Error
}
`)
	imp, ok := nodes[0].(*ast.ImportStmt)
	if !ok {
		t.Fatalf("expected *ast.ImportStmt, got %T", nodes[0])
	}
	if got := importNodeNames(imp.ModulePath); !equalStringSlices(got, []string{"std", "calendar"}) {
		t.Errorf("ModulePath = %v, want [std calendar]", got)
	}
	if got := importNodeNames(imp.Names); !equalStringSlices(got, []string{"DateParts", "Days", "Error"}) {
		t.Errorf("Names = %v, want [DateParts Days Error]", got)
	}
}

func TestParseImportAllowsDottedSelectorInsideOwnerBrace(t *testing.T) {
	nodes := parse(t, `import std/json: Json.{self, Case.Camel}`)
	imp, ok := nodes[0].(*ast.ImportStmt)
	if !ok {
		t.Fatalf("expected *ast.ImportStmt, got %T", nodes[0])
	}
	gotPath := make([]string, len(imp.ModulePath))
	for i, n := range imp.ModulePath {
		gotPath[i] = ast.ImportNodeName(n)
	}
	if !equalStringSlices(gotPath, []string{"std", "json", "Json"}) {
		t.Errorf("ModulePath = %v, want [std json Json]", gotPath)
	}
	if !imp.IncludeParent {
		t.Fatal("expected IncludeParent from self")
	}
	if len(imp.Names) != 1 || ast.ImportNodeName(imp.Names[0]) != "Case.Camel" {
		t.Fatalf("Names = %v, want [Case.Camel]", importNodeNames(imp.Names))
	}
}

// TestParseImportAllowsDottedSingleSelective confirms `.User` shorthand for
// `.{User}` after a slash-separated path still parses.
func TestParseImportAllowsDottedSingleSelective(t *testing.T) {
	if _, err := Parse(lexer.Lex("import models.User\n")); err != nil {
		t.Fatalf("single-selective `.User` must still parse: %v", err)
	}
}

func TestParseImportAllowsKeywordPathSegment(t *testing.T) {
	nodes, err := Parse(lexer.Lex("import std/defer: Defer\n"))
	if err != nil {
		t.Fatalf("keyword path segment must parse after slash: %v", err)
	}
	imp := nodes[0].(*ast.ImportStmt)
	if got := ast.ImportNodeName(imp.ModulePath[1]); got != "defer" {
		t.Fatalf("expected defer path segment, got %q", got)
	}
}

func TestParseImportRejectsDottedKeywordPathSegment(t *testing.T) {
	_, err := Parse(lexer.Lex("import std.defer: Defer\n"))
	if err == nil {
		t.Fatal("expected error rejecting keyword as dotted import selector")
	}
	if !strings.Contains(err.Error(), "expected import name") {
		t.Errorf("error should mention import name; got: %v", err)
	}
}

func TestParseImportColonSelectorList(t *testing.T) {
	nodes := parse(t, "import api: PublicApi.{self, some_child}, InternalApi.{self, another_child}\n")
	blk, ok := nodes[0].(*ast.ImportBlock)
	if !ok {
		t.Fatalf("expected selector list to lower to ImportBlock, got %T", nodes[0])
	}
	if len(blk.Entries) != 2 {
		t.Fatalf("expected 2 selector entries, got %d", len(blk.Entries))
	}
	wantPaths := [][]string{
		{"api", "PublicApi"},
		{"api", "InternalApi"},
	}
	wantNames := []string{"some_child", "another_child"}
	for i, entry := range blk.Entries {
		gotPath := make([]string, len(entry.ModulePath))
		for j, n := range entry.ModulePath {
			gotPath[j] = ast.ImportNodeName(n)
		}
		if !equalStringSlices(gotPath, wantPaths[i]) {
			t.Errorf("entry %d ModulePath = %v, want %v", i, gotPath, wantPaths[i])
		}
		if !entry.IncludeParent {
			t.Errorf("entry %d IncludeParent = false, want true", i)
		}
		if len(entry.Names) != 1 || ast.ImportNodeName(entry.Names[0]) != wantNames[i] {
			t.Errorf("entry %d Names = %v, want [%s]", i, entry.Names, wantNames[i])
		}
	}
}

func TestNamedArgs(t *testing.T) {
	nodes := parse(t, `connect("localhost", port: 3000)`)
	call := nodes[0].(*ast.ExprStmt).Expr.(*ast.Call)
	if len(call.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(call.Args))
	}
	if _, ok := call.Args[0].(*ast.StringLit); !ok {
		t.Errorf("expected first arg to be StringLit, got %T", call.Args[0])
	}
	na, ok := call.Args[1].(*ast.NamedArg)
	if !ok {
		t.Fatalf("expected second arg to be NamedArg, got %T", call.Args[1])
	}
	if na.Name != "port" {
		t.Errorf("expected name 'port', got %q", na.Name)
	}
}

func TestAllNamedArgs(t *testing.T) {
	nodes := parse(t, `connect(host: "localhost", port: 3000)`)
	call := nodes[0].(*ast.ExprStmt).Expr.(*ast.Call)
	if len(call.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(call.Args))
	}
	na0 := call.Args[0].(*ast.NamedArg)
	na1 := call.Args[1].(*ast.NamedArg)
	if na0.Name != "host" || na1.Name != "port" {
		t.Errorf("expected host and port, got %s and %s", na0.Name, na1.Name)
	}
}

// A single trailing positional after named arguments is the
// options-then-callback shape — `each(items, max: 8, handler)`. It parses,
// because with one argument left there is exactly one slot it can mean.
// Anything further back in the list is ambiguous and stays an error.
func TestTrailingPositionalAfterNamed(t *testing.T) {
	ok := []string{
		`connect(port: 3000, "localhost")`,
		`each_with([1, 2], max: 8, |n| n * 3)`,
		`each_with([1, 2], max: 8, double)`,
	}
	for _, src := range ok {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("%s: unexpected error: %v", src, err)
		}
	}

	bad := []string{
		// Two positionals after the named arg: the second is trailing, but
		// the first sits in the middle with nothing to anchor it.
		`connect(port: 3000, "localhost", 30)`,
		`each_with(max: 8, [1, 2], double)`,
	}
	for _, src := range bad {
		if _, err := Parse(lexer.Lex(src)); err == nil {
			t.Errorf("%s: expected error for a non-trailing positional after named", src)
		}
	}
}

func TestGenericTypeAnnotation(t *testing.T) {
	// Simple generic: x: List<Int> = [1]
	nodes := parse(t, `fn foo(x: List<Int>): Map<String, Int> { x }`)
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected FuncDef, got %T", nodes[0])
	}
	if fd.Params[0].TypeAnnotation == nil || fd.Params[0].TypeAnnotation.TypeString() != "List<Int>" {
		t.Errorf("param type = %v, want %q", fd.Params[0].TypeAnnotation, "List<Int>")
	}
	if fd.ReturnTypeExpr == nil || fd.ReturnTypeExpr.TypeString() != "Map<String, Int>" {
		t.Errorf("return type = %v, want %q", fd.ReturnTypeExpr, "Map<String, Int>")
	}
}

func TestNestedGenericTypeAnnotation(t *testing.T) {
	nodes := parse(t, `fn foo(x: Map<String, List<Int>>): Result<List<String>, Error> { x }`)
	fd := nodes[0].(*ast.FuncDef)
	if fd.Params[0].TypeAnnotation == nil || fd.Params[0].TypeAnnotation.TypeString() != "Map<String, List<Int>>" {
		t.Errorf("param type = %v, want %q", fd.Params[0].TypeAnnotation, "Map<String, List<Int>>")
	}
	if fd.ReturnTypeExpr == nil || fd.ReturnTypeExpr.TypeString() != "Result<List<String>, Error>" {
		t.Errorf("return type = %v, want %q", fd.ReturnTypeExpr, "Result<List<String>, Error>")
	}
}

func TestFuncTypeParams(t *testing.T) {
	nodes := parse(t, `fn first<A>(list: List<A>): A { list }`)
	fd := nodes[0].(*ast.FuncDef)
	if len(fd.TypeParams) != 1 || fd.TypeParams[0].Name != "A" {
		t.Errorf("TypeParams = %v, want ['A]", fd.TypeParams)
	}
	if fd.Params[0].TypeAnnotation == nil || fd.Params[0].TypeAnnotation.TypeString() != "List<A>" {
		t.Errorf("param type = %v, want %q", fd.Params[0].TypeAnnotation, "List<A>")
	}
}

func TestInlineGenericBoundsRejected(t *testing.T) {
	tokens := lexer.Lex(`fn first<T: Display>(value: T): T { value }`)
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected inline generic bound to be rejected")
	}
	if !strings.Contains(err.Error(), "inline generic bounds are not supported") {
		t.Fatalf("expected where-only diagnostic, got %v", err)
	}
}

func TestStructTypeParams(t *testing.T) {
	nodes := parse(t, "struct Pair<A, B> { first: A; second: B }")
	sd := nodes[0].(*ast.StructDef)
	if len(sd.TypeParams) != 2 || sd.TypeParams[0].Name != "A" || sd.TypeParams[1].Name != "B" {
		t.Errorf("TypeParams = %v, want [A B]", sd.TypeParams)
	}
	if sd.Fields[0].TypeAnnotation == nil || sd.Fields[0].TypeAnnotation.TypeString() != "A" || sd.Fields[1].TypeAnnotation == nil || sd.Fields[1].TypeAnnotation.TypeString() != "B" {
		t.Errorf("field types = [%v, %v], want [A, B]", sd.Fields[0].TypeAnnotation, sd.Fields[1].TypeAnnotation)
	}
}

func TestEnumTypeParams(t *testing.T) {
	nodes := parse(t, "enum Result<T, E> { Ok T; Err E }")
	ed := nodes[0].(*ast.EnumDef)
	if len(ed.TypeParams) != 2 || ed.TypeParams[0].Name != "T" || ed.TypeParams[1].Name != "E" {
		t.Errorf("TypeParams = %v, want [T E]", ed.TypeParams)
	}
}

func TestInterfaceTypeParams(t *testing.T) {
	nodes := parse(t, "interface Container<T> {\n  fn get(key: String): T\n}")
	id := nodes[0].(*ast.InterfaceDef)
	if len(id.TypeParams) != 1 || id.TypeParams[0].Name != "T" {
		t.Errorf("TypeParams = %v, want ['T]", id.TypeParams)
	}
}

func TestParseEnumStructVariant(t *testing.T) {
	src := `enum Shape {
    Rectangle{width: Float, height: Float}
    Point
}`
	tokens := lexer.Lex(src)
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	enumDef := nodes[0].(*ast.EnumDef)
	if len(enumDef.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(enumDef.Variants))
	}
	rect := enumDef.Variants[0]
	if rect.Kind != "struct" {
		t.Errorf("expected kind 'struct', got %q", rect.Kind)
	}
	if len(rect.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(rect.Fields))
	}
	if rect.Fields[0].Name != "width" || rect.Fields[0].TypeAnnotation == nil || rect.Fields[0].TypeAnnotation.TypeString() != "Float" {
		t.Errorf("expected field width: Float, got %s: %v", rect.Fields[0].Name, rect.Fields[0].TypeAnnotation)
	}
}

func TestParseEnumStructVariantWithDefaults(t *testing.T) {
	src := `enum Error {
    HttpError{status: Int, message: String, retryable: Bool = false}
    Timeout
}`
	tokens := lexer.Lex(src)
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	enumDef := nodes[0].(*ast.EnumDef)
	httpErr := enumDef.Variants[0]
	if httpErr.Kind != "struct" {
		t.Errorf("expected kind 'struct', got %q", httpErr.Kind)
	}
	if len(httpErr.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(httpErr.Fields))
	}
	if httpErr.Fields[2].Default == nil {
		t.Error("expected default value for 'retryable' field")
	}
}

// TestParser_Enum_CommaRejected verifies that a comma separator between
// variants is rejected — enum variants are newline-separated.
func TestParser_Enum_CommaRejected(t *testing.T) {
	src := "enum Shape { Circle, Square }"
	tokens := lexer.Lex(src)
	if _, err := Parse(tokens); err == nil {
		t.Fatal("expected parse error for comma-separated enum variants, got none")
	}
}

// TestParser_Struct_TypeIdentFieldRejected verifies that struct fields
// must be IDENT, not TYPE_IDENT. The source `struct Shape { Circle | Square }`
// looks like someone reaching for an enum body inside a struct keyword;
// the parser rejects it because struct fields require lowercase names.
func TestParser_Struct_TypeIdentFieldRejected(t *testing.T) {
	src := `struct Shape { Circle | Square }`
	tokens := lexer.Lex(src)
	if _, err := Parse(tokens); err == nil {
		t.Fatal("expected parse error for struct field with TYPE_IDENT name, got none")
	}
}

// TestParser_Enum_PipeAlternationRejected verifies the retired `V1 | V2`
// alternation form is a parse error.
func TestParser_Enum_PipeAlternationRejected(t *testing.T) {
	src := "enum Shape { Circle | Square }"
	tokens := lexer.Lex(src)
	if _, err := Parse(tokens); err == nil {
		t.Fatal("expected parse error for `|` enum alternation, got none")
	}
}

// TestParser_Enum_AllVariantKinds covers bare, positional, struct-payload,
// and embed variants — one variant per line.
func TestParser_Enum_AllVariantKinds(t *testing.T) {
	src := `enum Shape {
  Circle Float
  Rectangle {width: Float, height: Float}
  Point
  embeds Polygon
}`
	tokens := lexer.Lex(src)
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	ed, ok := nodes[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("expected *ast.EnumDef, got %T", nodes[0])
	}
	if len(ed.Variants) != 4 {
		t.Fatalf("expected 4 variants, got %d", len(ed.Variants))
	}
	want := []struct {
		name string
		kind string
	}{
		{"Circle", "positional"},
		{"Rectangle", "struct"},
		{"Point", "bare"},
		{"Polygon", "embedded"},
	}
	for i, w := range want {
		if ed.Variants[i].Name != w.name || ed.Variants[i].Kind != w.kind {
			t.Errorf("variant %d = %s/%s, want %s/%s", i, ed.Variants[i].Name, ed.Variants[i].Kind, w.name, w.kind)
		}
	}
}

// TestParser_Variant_PayloadTypeExpr_Primitive verifies that the new
// space-separated payload syntax — `| Number Int` — parses to a
// positional variant whose payload type is Int.
func TestParser_Variant_PayloadTypeExpr_Primitive(t *testing.T) {
	src := "enum Token { Number Int }"
	nodes := parse(t, src)
	ed := nodes[0].(*ast.EnumDef)
	if len(ed.Variants) != 1 || ed.Variants[0].Name != "Number" {
		t.Fatalf("expected one variant Number, got %+v", ed.Variants)
	}
	if ed.Variants[0].Kind != "positional" {
		t.Errorf("kind = %q, want positional", ed.Variants[0].Kind)
	}
	st, ok := ed.Variants[0].DataTypeExpr.(*ast.SimpleType)
	if !ok || st.Name != "Int" {
		t.Errorf("payload = %#v, want SimpleType{Int}", ed.Variants[0].DataTypeExpr)
	}
}

// TestParser_Variant_PayloadTypeExpr_Tuple verifies the new syntax
// for tuple payloads: `| Position (Int, Int)`.
func TestParser_Variant_PayloadTypeExpr_Tuple(t *testing.T) {
	src := "enum Token { Position (Int, Int) }"
	nodes := parse(t, src)
	ed := nodes[0].(*ast.EnumDef)
	if len(ed.Variants) != 1 {
		t.Fatalf("expected 1 variant, got %d", len(ed.Variants))
	}
	v := ed.Variants[0]
	if v.Name != "Position" || v.Kind != "positional" {
		t.Fatalf("want Position/positional, got %s/%s", v.Name, v.Kind)
	}
	ft, ok := v.DataTypeExpr.(*ast.FuncType)
	if !ok || ft.Return != nil || len(ft.Params) != 2 {
		t.Fatalf("expected tuple-shaped FuncType (2 params, no return), got %#v", v.DataTypeExpr)
	}
}

// TestParser_Variant_PayloadTypeExpr_Function verifies a function-typed
// variant payload parses: `| Computation (Int) -> Int`.
func TestParser_Variant_PayloadTypeExpr_Function(t *testing.T) {
	src := "enum Token { Computation (Int) -> Int }"
	nodes := parse(t, src)
	ed := nodes[0].(*ast.EnumDef)
	v := ed.Variants[0]
	ft, ok := v.DataTypeExpr.(*ast.FuncType)
	if !ok || ft.Return == nil || len(ft.Params) != 1 {
		t.Fatalf("expected FuncType with 1 param and return, got %#v", v.DataTypeExpr)
	}
}

// TestParser_Variant_PayloadTypeExpr_Generic verifies a generic payload
// parses: `| Inner Maybe<Int>`.
func TestParser_Variant_PayloadTypeExpr_Generic(t *testing.T) {
	src := "enum Token { Inner Maybe<Int> }"
	nodes := parse(t, src)
	ed := nodes[0].(*ast.EnumDef)
	v := ed.Variants[0]
	gt, ok := v.DataTypeExpr.(*ast.GenericType)
	if !ok || gt.Name != "Maybe" || len(gt.Params) != 1 {
		t.Fatalf("expected GenericType{Maybe<Int>}, got %#v", v.DataTypeExpr)
	}
}

// TestParser_Variant_PayloadTypeExpr_Map verifies a multi-param generic
// payload (Map<K, V>) parses.
func TestParser_Variant_PayloadTypeExpr_Map(t *testing.T) {
	src := "enum Token { Bag Map<String, Int> }"
	nodes := parse(t, src)
	ed := nodes[0].(*ast.EnumDef)
	v := ed.Variants[0]
	gt, ok := v.DataTypeExpr.(*ast.GenericType)
	if !ok || gt.Name != "Map" || len(gt.Params) != 2 {
		t.Fatalf("expected GenericType{Map<String, Int>}, got %#v", v.DataTypeExpr)
	}
}

// TestParser_Variant_StructPayload_HasSpace verifies a struct-payload
// variant with a leading space before `{` parses cleanly.
func TestParser_Variant_StructPayload_HasSpace(t *testing.T) {
	src := "enum Token { Card {rank: Int, suit: String} }"
	nodes := parse(t, src)
	ed := nodes[0].(*ast.EnumDef)
	v := ed.Variants[0]
	if v.Kind != "struct" || len(v.Fields) != 2 {
		t.Fatalf("expected struct variant with 2 fields, got %s with %d fields", v.Kind, len(v.Fields))
	}
}

// TestParser_Variant_BareUnchanged verifies bare variants (no payload)
// still parse correctly.
func TestParser_Variant_BareUnchanged(t *testing.T) {
	src := "enum Token { Eof }"
	nodes := parse(t, src)
	ed := nodes[0].(*ast.EnumDef)
	if ed.Variants[0].Kind != "bare" {
		t.Errorf("expected bare, got %s", ed.Variants[0].Kind)
	}
}

// TestParser_Variant_EmbedUnchanged verifies `embeds X` survives the
// new payload syntax.
func TestParser_Variant_EmbedUnchanged(t *testing.T) {
	src := "enum Token { embeds Rect }"
	nodes := parse(t, src)
	ed := nodes[0].(*ast.EnumDef)
	if ed.Variants[0].Kind != "embedded" || ed.Variants[0].Name != "Rect" {
		t.Errorf("expected embedded Rect, got %s/%s", ed.Variants[0].Kind, ed.Variants[0].Name)
	}
}

// TestParser_Variant_OldParenForm_StillParses documents that the legacy
// paren-wrapped form `| Foo(Int)` continues to parse — its tokens are
// indistinguishable from a parenthesized type expression `(Int)`, which
// per the no-1-tuples rule collapses to `Int`. Migration is formatter-
// driven; the AST is identical to the new form.
func TestParser_Variant_OldParenForm_StillParses(t *testing.T) {
	src := "enum Token { Number(Int) }"
	nodes := parse(t, src)
	ed := nodes[0].(*ast.EnumDef)
	v := ed.Variants[0]
	st, ok := v.DataTypeExpr.(*ast.SimpleType)
	if !ok || st.Name != "Int" {
		t.Fatalf("expected SimpleType{Int} after 1-tuple unwrap, got %#v", v.DataTypeExpr)
	}
}

// TestParser_Variant_OldDoubleParens_StillParses documents that the
// legacy double-paren tuple-payload form `| Position((Int, Int))` still
// parses — under the unified type-expression rule, the outer parens
// collapse (no 1-tuples) and the AST matches the new `| Position (Int, Int)`.
func TestParser_Variant_OldDoubleParens_StillParses(t *testing.T) {
	src := "enum Token { Position((Int, Int)) }"
	nodes := parse(t, src)
	ed := nodes[0].(*ast.EnumDef)
	v := ed.Variants[0]
	ft, ok := v.DataTypeExpr.(*ast.FuncType)
	if !ok || ft.Return != nil || len(ft.Params) != 2 {
		t.Fatalf("expected tuple-shaped FuncType (2 params, no return) after unwrap, got %#v", v.DataTypeExpr)
	}
}

// TestParser_Variant_OldMultiArgParens_NowParses verifies what was
// previously rejected as "multi-positional" now parses as a tuple-typed
// payload (the unified type-expression rule reads `(Int, Int)` as a
// tuple type).
func TestParser_Variant_OldMultiArgParens_NowParses(t *testing.T) {
	src := "enum Token { Position(Int, Int) }"
	nodes := parse(t, src)
	ed := nodes[0].(*ast.EnumDef)
	v := ed.Variants[0]
	ft, ok := v.DataTypeExpr.(*ast.FuncType)
	if !ok || ft.Return != nil || len(ft.Params) != 2 {
		t.Fatalf("expected tuple-shaped FuncType (2 params, no return), got %#v", v.DataTypeExpr)
	}
}

// TestParser_Variant_NoMultiPositionalViaSpace verifies that the
// space-separated form does NOT support multiple positional types:
// `| Foo Int Int` errors after parsing the first type expression
// (the second `Int` is junk where `|` or `}` was expected).
func TestParser_Variant_NoMultiPositionalViaSpace(t *testing.T) {
	src := "enum Token { Foo Int Int }"
	tokens := lexer.Lex(src)
	if _, err := Parse(tokens); err == nil {
		t.Fatal("expected parse error for `| Foo Int Int`, got none")
	}
}

func TestIdentColumnTracking(t *testing.T) {
	tokens := lexer.Lex("x = 42")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	binding := nodes[0].(*ast.Binding)
	if binding.Col != 1 {
		t.Errorf("expected binding col 1, got %d", binding.Col)
	}
}

func TestTypeIdentColumnTracking(t *testing.T) {
	tokens := lexer.Lex("struct User { name: String }")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	def := nodes[0].(*ast.StructDef)
	if def.Col != 8 {
		t.Errorf("expected struct name col 8, got %d", def.Col)
	}
}

func TestFuncDefColumnTracking(t *testing.T) {
	tokens := lexer.Lex("fn Add(x: Int): Int { x }")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	fd := nodes[0].(*ast.FuncDef)
	if fd.Col != 4 {
		t.Errorf("expected fn name col 4, got %d", fd.Col)
	}
}

func TestParseWithRecovery_MultipleFunctions(t *testing.T) {
	input := `fn Add(x: Int, y: Int): Int {
    x +
}

fn Sub(x: Int, y: Int): Int {
    x - y
}`
	tokens := lexer.Lex(input)
	nodes, errs := ParseWithRecovery(tokens)

	if len(errs) == 0 {
		t.Fatal("expected at least one error")
	}
	found := false
	for _, n := range nodes {
		if fd, ok := n.(*ast.FuncDef); ok && fd.Name == "Sub" {
			found = true
		}
	}
	if !found {
		t.Error("expected Sub function to be parsed despite earlier error")
	}
}

func TestParseWithRecovery_NoErrors(t *testing.T) {
	input := `fn Add(x: Int, y: Int): Int { x + y }`
	tokens := lexer.Lex(input)
	nodes, errs := ParseWithRecovery(tokens)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %d", len(errs))
	}
	if len(nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(nodes))
	}
}

func TestParseWithRecovery_SkipsToNextDeclaration(t *testing.T) {
	input := `type { invalid }

enum Color {
    Red
    Green
    Blue
}`
	tokens := lexer.Lex(input)
	nodes, errs := ParseWithRecovery(tokens)

	if len(errs) == 0 {
		t.Fatal("expected parse errors")
	}
	// Should recover and parse the enum
	found := false
	for _, n := range nodes {
		if ed, ok := n.(*ast.EnumDef); ok && ed.Name == "Color" {
			found = true
		}
	}
	if !found {
		t.Error("expected Color enum to be parsed after recovery")
	}
}

func TestExternFunc(t *testing.T) {
	nodes := parse(t, "pub host fn print(value: String): Unit")
	var ef *ast.ExternFunc
	for _, n := range nodes {
		if v, ok := n.(*ast.ExternFunc); ok {
			ef = v
			break
		}
	}
	if ef == nil {
		t.Fatal("expected *ast.ExternFunc among nodes")
	}
	if ef.Name != "print" {
		t.Errorf("expected name print, got %s", ef.Name)
	}
	if len(ef.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(ef.Params))
	}
	if ef.Params[0].Name != "value" {
		t.Errorf("expected param name 'value', got %s", ef.Params[0].Name)
	}
	if ef.Params[0].TypeAnnotation == nil || ef.Params[0].TypeAnnotation.TypeString() != "String" {
		t.Errorf("expected param type String, got %v", ef.Params[0].TypeAnnotation)
	}
	if ef.ReturnTypeExpr == nil || ef.ReturnTypeExpr.TypeString() != "Unit" {
		t.Errorf("expected return type Unit, got %v", ef.ReturnTypeExpr)
	}
	if !ef.Public {
		t.Error("expected print to be public (pub modifier)")
	}
}

func TestExternType(t *testing.T) {
	nodes := parse(t, "pub host type Int")
	var et *ast.ExternType
	for _, n := range nodes {
		if v, ok := n.(*ast.ExternType); ok {
			et = v
			break
		}
	}
	if et == nil {
		t.Fatal("expected *ast.ExternType among nodes")
	}
	if et.Name != "Int" {
		t.Errorf("expected name Int, got %s", et.Name)
	}
	if !et.Public {
		t.Error("expected Int to be public (pub modifier)")
	}
}

func TestExternFuncWithDocComment(t *testing.T) {
	source := "/// Prints a value.\n/// Returns Unit.\nhost fn Print(value: String): Unit"
	nodes := parse(t, source)
	ef, ok := nodes[0].(*ast.ExternFunc)
	if !ok {
		t.Fatalf("expected *ast.ExternFunc, got %T", nodes[0])
	}
	if ef.Doc != "Prints a value.\nReturns Unit." {
		t.Errorf("expected doc comment, got %q", ef.Doc)
	}
}

func TestFuncDefWithDocComment(t *testing.T) {
	source := "/// Adds two numbers.\nfn Add(x: Int, y: Int): Int { x + y }"
	nodes := parse(t, source)
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected *ast.FuncDef, got %T", nodes[0])
	}
	if fd.Doc != "Adds two numbers." {
		t.Errorf("expected doc comment, got %q", fd.Doc)
	}
}

// Doc-comment lines preserve relative indentation past the conventional
// single space after `///`. That space is the rustdoc-style separator
// between marker and body; anything beyond it is content and survives
// to the AST. Critical for nomi-fenced code examples whose readability
// depends on indented case arms, lambda bodies, etc.
func TestFuncDefDocCommentPreservesIndent(t *testing.T) {
	source := "/// case x {\n" +
		"///   Some(v) -> v\n" +
		"///   None -> 0\n" +
		"/// }\n" +
		"fn Probe(): Int { 0 }"
	nodes := parse(t, source)
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected *ast.FuncDef, got %T", nodes[0])
	}
	want := "case x {\n  Some(v) -> v\n  None -> 0\n}"
	if fd.Doc != want {
		t.Errorf("doc indent not preserved:\ngot  %q\nwant %q", fd.Doc, want)
	}
}

// A blank `///` line round-trips as an empty string in the joined doc;
// trailing whitespace on a `///` line is dropped (it'd otherwise produce
// a phantom-space line that the formatter would normalise away anyway).
func TestFuncDefDocCommentBlankAndTrailingSpace(t *testing.T) {
	source := "/// First paragraph.\n" +
		"///   \n" + // trailing whitespace on a logically-blank line
		"/// Second paragraph.\n" +
		"fn Probe(): Int { 0 }"
	nodes := parse(t, source)
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected *ast.FuncDef, got %T", nodes[0])
	}
	want := "First paragraph.\n\nSecond paragraph."
	if fd.Doc != want {
		t.Errorf("blank-line normalisation wrong:\ngot  %q\nwant %q", fd.Doc, want)
	}
}

func TestExternFuncNoParams(t *testing.T) {
	nodes := parse(t, "host fn ReadLine(): String")
	ef := nodes[0].(*ast.ExternFunc)
	if len(ef.Params) != 0 {
		t.Errorf("expected 0 params, got %d", len(ef.Params))
	}
	if ef.ReturnTypeExpr == nil || ef.ReturnTypeExpr.TypeString() != "String" {
		t.Errorf("expected return type String, got %v", ef.ReturnTypeExpr)
	}
}

func TestParseStdlibDeclarations(t *testing.T) {
	source := `/// 64-bit integer.
host type Int

/// A value that may or may not be present.
enum Maybe<T> {
    Some T
    None
}
`
	nodes := parse(t, source)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	et, ok := nodes[0].(*ast.ExternType)
	if !ok {
		t.Fatalf("expected ExternType, got %T", nodes[0])
	}
	if et.Doc != "64-bit integer." {
		t.Errorf("expected doc, got %q", et.Doc)
	}
}

func TestParseTypeExpr_Simple(t *testing.T) {
	tokens := lexer.Lex("fn foo(x: Int): Int { x }")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	fn := nodes[0].(*ast.FuncDef)
	if fn.Params[0].TypeAnnotation == nil {
		t.Fatal("expected param type annotation")
	}
	simple, ok := fn.Params[0].TypeAnnotation.(*ast.SimpleType)
	if !ok {
		t.Fatalf("expected SimpleType, got %T", fn.Params[0].TypeAnnotation)
	}
	if simple.Name != "Int" {
		t.Errorf("expected Int, got %s", simple.Name)
	}
	ret, ok := fn.ReturnTypeExpr.(*ast.SimpleType)
	if !ok {
		t.Fatalf("expected SimpleType return, got %T", fn.ReturnTypeExpr)
	}
	if ret.Name != "Int" {
		t.Errorf("expected Int return, got %s", ret.Name)
	}
}

func TestParseTypeExpr_Generic(t *testing.T) {
	tokens := lexer.Lex("fn foo(x: List<Int>): String { x }")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	fn := nodes[0].(*ast.FuncDef)
	generic, ok := fn.Params[0].TypeAnnotation.(*ast.GenericType)
	if !ok {
		t.Fatalf("expected GenericType, got %T", fn.Params[0].TypeAnnotation)
	}
	if generic.Name != "List" {
		t.Errorf("expected List, got %s", generic.Name)
	}
	if len(generic.Params) != 1 {
		t.Fatalf("expected 1 type param, got %d", len(generic.Params))
	}
	inner, ok := generic.Params[0].(*ast.SimpleType)
	if !ok || inner.Name != "Int" {
		t.Errorf("expected SimpleType Int, got %v", generic.Params[0])
	}
}

func TestParseTypeExpr_FuncType(t *testing.T) {
	tokens := lexer.Lex("fn apply(f: (Int) -> String): String { f(1) }")
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	fn := nodes[0].(*ast.FuncDef)
	funcType, ok := fn.Params[0].TypeAnnotation.(*ast.FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", fn.Params[0].TypeAnnotation)
	}
	if len(funcType.Params) != 1 {
		t.Fatalf("expected 1 fn param, got %d", len(funcType.Params))
	}
	if funcType.Return == nil {
		t.Fatal("expected return type")
	}
}

// `once` is the language's only "set once, never changes" declaration.
// It accepts an optional type annotation; when omitted the type is
// inferred from the right-hand side. Parser tests below cover both
// shapes (with and without annotation).

func TestParseOnce_NoAnnotation(t *testing.T) {
	src := "once max_retries = 3"
	tokens := lexer.Lex(src)
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	ob, ok := nodes[0].(*ast.OnceBinding)
	if !ok {
		t.Fatalf("expected *ast.OnceBinding, got %T", nodes[0])
	}
	if ob.Name != "max_retries" {
		t.Errorf("expected name max_retries, got %s", ob.Name)
	}
	if ob.TypeAnnotation != nil {
		t.Errorf("expected no annotation, got %v", ob.TypeAnnotation)
	}
	lit, ok := ob.Value.(*ast.IntLit)
	if !ok || lit.Value != 3 {
		t.Errorf("expected IntLit(3), got %T %v", ob.Value, ob.Value)
	}
}

func TestParseOnce_WithAnnotation(t *testing.T) {
	src := "once default_port: Int = 8080"
	tokens := lexer.Lex(src)
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	ob := nodes[0].(*ast.OnceBinding)
	if ob.Name != "default_port" {
		t.Errorf("expected name default_port, got %s", ob.Name)
	}
	if ob.TypeAnnotation == nil {
		t.Error("expected type annotation, got nil")
	}
}

func TestQualifiedTypeAnnotation(t *testing.T) {
	nodes := parse(t, `fn foo(r: io.Reader): io.Writer { r }`)
	fd := nodes[0].(*ast.FuncDef)
	paramType := fd.Params[0].TypeAnnotation
	qt, ok := paramType.(*ast.QualifiedType)
	if !ok {
		t.Fatalf("expected QualifiedType, got %T", paramType)
	}
	if qt.Module != "io" {
		t.Errorf("expected module 'io', got %q", qt.Module)
	}
	member, ok := qt.Member.(*ast.SimpleType)
	if !ok {
		t.Fatalf("expected SimpleType member, got %T", qt.Member)
	}
	if member.Name != "Reader" {
		t.Errorf("expected member 'Reader', got %q", member.Name)
	}
	if qt.ModuleCol >= member.Col {
		t.Errorf("module col %d should be before member col %d", qt.ModuleCol, member.Col)
	}
}

func TestQualifiedGenericType(t *testing.T) {
	nodes := parse(t, `fn foo(xs: io.List<Int>) { xs }`)
	fd := nodes[0].(*ast.FuncDef)
	paramType := fd.Params[0].TypeAnnotation
	qt, ok := paramType.(*ast.QualifiedType)
	if !ok {
		t.Fatalf("expected QualifiedType, got %T", paramType)
	}
	if qt.Module != "io" {
		t.Errorf("expected module 'io', got %q", qt.Module)
	}
	member, ok := qt.Member.(*ast.GenericType)
	if !ok {
		t.Fatalf("expected GenericType member, got %T", qt.Member)
	}
	if member.Name != "List" {
		t.Errorf("expected member 'List', got %q", member.Name)
	}
}

func TestDistinctDestructure(t *testing.T) {
	tokens := lexer.Lex(`Id(x) = expr`)
	nodes, errs := ParseWithRecovery(tokens)
	if len(errs) > 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	dd, ok := nodes[0].(*ast.DistinctDestructure)
	if !ok {
		t.Fatalf("expected DistinctDestructure, got %T", nodes[0])
	}
	if dd.TypeName != "Id" {
		t.Errorf("expected TypeName 'Id', got %q", dd.TypeName)
	}
	if dd.Binding == nil || dd.Binding.Name != "x" {
		t.Errorf("expected binding 'x', got %v", dd.Binding)
	}
}

func TestDistinctDestructureWildcard(t *testing.T) {
	tokens := lexer.Lex(`Id(_) = expr`)
	nodes, errs := ParseWithRecovery(tokens)
	if len(errs) > 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	dd, ok := nodes[0].(*ast.DistinctDestructure)
	if !ok {
		t.Fatalf("expected DistinctDestructure, got %T", nodes[0])
	}
	if dd.Binding != nil {
		t.Errorf("expected nil binding for wildcard, got %v", dd.Binding)
	}
}

func TestDistinctDestructureNotBinding(t *testing.T) {
	// Id(42) should NOT parse as a destructure — it's a call expression
	tokens := lexer.Lex(`Id(42)`)
	nodes, errs := ParseWithRecovery(tokens)
	if len(errs) > 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if _, ok := nodes[0].(*ast.DistinctDestructure); ok {
		t.Error("Id(42) should not parse as DistinctDestructure")
	}
}

func TestInterface_RequiresFnKeyword(t *testing.T) {
	src := `
interface Speaker {
    speak(value: self): String
}
`
	tokens := lexer.Lex(src)
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected parse error for bare method name in interface, got nil")
	}
	if !strings.Contains(err.Error(), "fn") {
		t.Errorf("expected error mentioning 'fn', got: %v", err)
	}
}

func TestInterface_AcceptsFnKeyword(t *testing.T) {
	src := `
interface Speaker {
    fn speak(value: self): String
}
`
	nodes := parse(t, src)
	var iface *ast.InterfaceDef
	for _, n := range nodes {
		if id, ok := n.(*ast.InterfaceDef); ok {
			iface = id
			break
		}
	}
	if iface == nil {
		t.Fatal("expected InterfaceDef")
	}
	if len(iface.Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(iface.Methods))
	}
	if iface.Methods[0].Name != "speak" {
		t.Errorf("expected method name 'speak', got %s", iface.Methods[0].Name)
	}
}

func TestParse_IgnoresInlineComment(t *testing.T) {
	src := "x = 1 // ignored\n"
	toks := lexer.Lex(src)
	nodes, err := Parse(toks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
}

// --- Range literals ---

func parseExprOnly(t *testing.T, src string) ast.Node {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error for %q: %v", src, err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node for %q, got %d", src, len(nodes))
	}
	if stmt, ok := nodes[0].(*ast.ExprStmt); ok {
		return stmt.Expr
	}
	return nodes[0]
}

func TestParseRange_HalfOpenBounded(t *testing.T) {
	r, ok := parseExprOnly(t, "1..5").(*ast.RangeLit)
	if !ok {
		t.Fatalf("expected RangeLit, got %T", parseExprOnly(t, "1..5"))
	}
	if r.Start == nil || r.End == nil {
		t.Errorf("expected both Start and End set, got Start=%v End=%v", r.Start, r.End)
	}
	if r.Inclusive {
		t.Errorf("expected Inclusive=false for `..`")
	}
}

func TestParseRange_ClosedBounded(t *testing.T) {
	r := parseExprOnly(t, "1..=5").(*ast.RangeLit)
	if r.Start == nil || r.End == nil {
		t.Errorf("expected both Start and End set")
	}
	if !r.Inclusive {
		t.Errorf("expected Inclusive=true for `..=`")
	}
}

func TestParseRange_NoStartRejected(t *testing.T) {
	// `..5` — open-ended ranges have been removed. Expect a clear migration
	// error pointing at the `Range.from` / `Range.naturals` replacements.
	tokens := lexer.Lex("..5")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatalf("expected parse error for `..5`, got nil")
	}
	if !strings.Contains(err.Error(), "open-ended ranges have been removed") {
		t.Errorf("expected migration message, got: %v", err)
	}
}

func TestParseRange_NoStartInclusiveRejected(t *testing.T) {
	tokens := lexer.Lex("..=5")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatalf("expected parse error for `..=5`, got nil")
	}
}

func TestParseRange_NoEndRejected(t *testing.T) {
	tokens := lexer.Lex("(1..)")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatalf("expected parse error for `(1..)`, got nil")
	}
	if !strings.Contains(err.Error(), "right-hand operand") {
		t.Errorf("expected message about required RHS, got: %v", err)
	}
}

func TestParseRange_FullFormRejected(t *testing.T) {
	tokens := lexer.Lex("(..)")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatalf("expected parse error for `(..)`, got nil")
	}
}

func TestParseRange_PrecedenceLowerThanArith(t *testing.T) {
	// `1+2..5+6` should parse as `(1+2)..(5+6)`. Both the Start and End
	// expressions should be Binary nodes.
	r := parseExprOnly(t, "1+2..5+6").(*ast.RangeLit)
	if _, ok := r.Start.(*ast.Binary); !ok {
		t.Errorf("expected Binary in Start, got %T", r.Start)
	}
	if _, ok := r.End.(*ast.Binary); !ok {
		t.Errorf("expected Binary in End, got %T", r.End)
	}
}

func TestParseRange_PrecedenceHigherThanComparison(t *testing.T) {
	// `1..5 == 1..5` should parse as `(1..5) == (1..5)`. The top-level
	// node should be Binary with op `==`, and both sides RangeLit.
	r := parseExprOnly(t, "1..5 == 1..5").(*ast.Binary)
	if r.Op != "==" {
		t.Errorf("expected top-level Op `==`, got %q", r.Op)
	}
	if _, ok := r.Left.(*ast.RangeLit); !ok {
		t.Errorf("expected Left RangeLit, got %T", r.Left)
	}
	if _, ok := r.Right.(*ast.RangeLit); !ok {
		t.Errorf("expected Right RangeLit, got %T", r.Right)
	}
}

// Top-level `export { ... }` blocks have been removed. Visibility is now
// declared inline with `pub`. The parser must surface a clear migration
// message when it encounters the legacy syntax rather than silently
// continuing or producing a cryptic dispatcher error.
func TestParseExportBlockRejected(t *testing.T) {
	tokens := lexer.Lex("export { foo }\nfn foo(): Int { 0 }")
	if _, err := Parse(tokens); err == nil {
		t.Fatal("expected parse error for legacy `export` block, got none")
	} else if !strings.Contains(err.Error(), "export") {
		t.Errorf("expected error to mention 'export', got: %v", err)
	}
}

func TestParseExportStatementRejected(t *testing.T) {
	// Per-statement form `export name` is also gone.
	tokens := lexer.Lex("export foo\nfn foo(): Int { 0 }")
	if _, err := Parse(tokens); err == nil {
		t.Fatal("expected parse error for legacy `export name` statement, got none")
	} else if !strings.Contains(err.Error(), "export") {
		t.Errorf("expected error to mention 'export', got: %v", err)
	}
}

// Regression: the EXPORT-rejection path in parseStmt must advance past the
// EXPORT token and synchronize, otherwise ParseWithRecovery (the LSP path)
// would loop forever on the same token. The existing rejection tests above
// exercise the non-recovering Parse path, which returns on the first error
// and so wouldn't catch a regression of the infinite-loop fix. If this test
// hangs instead of failing, the synchronize step has been lost.
func TestParseWithRecovery_ExportBlockRecovers(t *testing.T) {
	src := "export { foo }\nfn helper(): Int { 1 }"
	tokens := lexer.Lex(src)
	nodes, errs := ParseWithRecovery(tokens)

	if len(errs) == 0 {
		t.Fatal("expected at least one parse error for legacy `export` block")
	}
	mentionsExport := false
	for _, e := range errs {
		if strings.Contains(e.Message, "export") {
			mentionsExport = true
			break
		}
	}
	if !mentionsExport {
		t.Errorf("expected an error mentioning 'export', got: %v", errs)
	}

	found := false
	for _, n := range nodes {
		if fd, ok := n.(*ast.FuncDef); ok && fd.Name == "helper" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected ParseWithRecovery to recover and parse `helper` after the rejected export block")
	}
}

// --- Typed literals ---

// taggedRHS extracts the RHS of a `q = <expr>` binding-like construct
// from a parse result. Typed literals show up at the expression layer
// the same way StringInterp does — as the value of an ExprStmt'd
// expression — so the simplest path is to wrap them in `q = <expr>`
// and inspect the binding's Value.
func taggedRHS(t *testing.T, src string) *ast.TaggedString {
	t.Helper()
	nodes := parse(t, src)
	if len(nodes) != 1 {
		t.Fatalf("Parse(%q): expected 1 statement, got %d", src, len(nodes))
	}
	bind, ok := nodes[0].(*ast.Binding)
	if !ok {
		t.Fatalf("Parse(%q): expected *ast.Binding, got %T", src, nodes[0])
	}
	ts, ok := bind.Value.(*ast.TaggedString)
	if !ok {
		t.Fatalf("Parse(%q): expected RHS *ast.TaggedString, got %T", src, bind.Value)
	}
	return ts
}

func TestParseTaggedSingleLine(t *testing.T) {
	ts := taggedRHS(t, `q = Sql"SELECT * FROM users"`)
	if ts.Tag != "Sql" {
		t.Errorf("expected Tag=Sql, got %q", ts.Tag)
	}
	if ts.Raw || ts.Triple {
		t.Errorf("expected Raw=false Triple=false, got Raw=%v Triple=%v", ts.Raw, ts.Triple)
	}
	if len(ts.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(ts.Parts))
	}
	text, ok := ts.Parts[0].(ast.StringText)
	if !ok || text.Value != "SELECT * FROM users" {
		t.Errorf("expected StringText{SELECT * FROM users}, got %T %v", ts.Parts[0], ts.Parts[0])
	}
}

func TestParseTaggedSingleLineWithInterp(t *testing.T) {
	ts := taggedRHS(t, `q = Sql"id = ${id}"`)
	if ts.Tag != "Sql" {
		t.Errorf("expected Tag=Sql, got %q", ts.Tag)
	}
	if ts.Raw || ts.Triple {
		t.Errorf("expected Raw=false Triple=false, got Raw=%v Triple=%v", ts.Raw, ts.Triple)
	}
	// Parts: [StringText("id = "), StringExpr(Ident{id})]
	if len(ts.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d (%v)", len(ts.Parts), ts.Parts)
	}
	text, ok := ts.Parts[0].(ast.StringText)
	if !ok || text.Value != "id = " {
		t.Errorf("part[0]: expected StringText{\"id = \"}, got %T %v", ts.Parts[0], ts.Parts[0])
	}
	se, ok := ts.Parts[1].(ast.StringExpr)
	if !ok {
		t.Fatalf("part[1]: expected StringExpr, got %T", ts.Parts[1])
	}
	ident, ok := se.Expr.(*ast.Ident)
	if !ok || ident.Name != "id" {
		t.Errorf("part[1]: expected Ident{id}, got %T %v", se.Expr, se.Expr)
	}
}

func TestParseTaggedTriple(t *testing.T) {
	ts := taggedRHS(t, "q = Sql\"\"\"\n    SELECT 1\n    \"\"\"")
	if ts.Tag != "Sql" {
		t.Errorf("expected Tag=Sql, got %q", ts.Tag)
	}
	if ts.Raw {
		t.Errorf("expected Raw=false, got %v", ts.Raw)
	}
	if !ts.Triple {
		t.Errorf("expected Triple=true, got %v", ts.Triple)
	}
	if len(ts.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(ts.Parts))
	}
	text, ok := ts.Parts[0].(ast.StringText)
	if !ok || text.Value != "SELECT 1" {
		t.Errorf("expected StringText{SELECT 1}, got %T %v", ts.Parts[0], ts.Parts[0])
	}
}

func TestParseTaggedTripleWithInterp(t *testing.T) {
	ts := taggedRHS(t, "q = Sql\"\"\"\n    WHERE id = ${id}\n    \"\"\"")
	if ts.Tag != "Sql" {
		t.Errorf("expected Tag=Sql, got %q", ts.Tag)
	}
	if ts.Raw {
		t.Errorf("expected Raw=false, got %v", ts.Raw)
	}
	if !ts.Triple {
		t.Errorf("expected Triple=true, got %v", ts.Triple)
	}
	if len(ts.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(ts.Parts))
	}
	text, ok := ts.Parts[0].(ast.StringText)
	if !ok || text.Value != "WHERE id = " {
		t.Errorf("part[0]: expected StringText{WHERE id = }, got %T %v", ts.Parts[0], ts.Parts[0])
	}
	se, ok := ts.Parts[1].(ast.StringExpr)
	if !ok {
		t.Fatalf("part[1]: expected StringExpr, got %T", ts.Parts[1])
	}
	ident, ok := se.Expr.(*ast.Ident)
	if !ok || ident.Name != "id" {
		t.Errorf("part[1]: expected Ident{id}, got %T %v", se.Expr, se.Expr)
	}
}

func TestParseRawTagged(t *testing.T) {
	ts := taggedRHS(t, "r = Regex`\\d+`")
	if ts.Tag != "Regex" {
		t.Errorf("expected Tag=Regex, got %q", ts.Tag)
	}
	if !ts.Raw {
		t.Errorf("expected Raw=true, got %v", ts.Raw)
	}
	if ts.Triple {
		t.Errorf("expected Triple=false, got %v", ts.Triple)
	}
	if len(ts.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(ts.Parts))
	}
	text, ok := ts.Parts[0].(ast.StringText)
	if !ok || text.Value != `\d+` {
		t.Errorf("expected StringText{\\d+}, got %T %v", ts.Parts[0], ts.Parts[0])
	}
}

func TestParseRawTaggedTriple(t *testing.T) {
	// `${HOME}` survives as literal text — Raw=true means lexer didn't
	// interpolate.
	ts := taggedRHS(t, "r = Bash`\n    echo ${HOME}\n    `")
	if ts.Tag != "Bash" {
		t.Errorf("expected Tag=Bash, got %q", ts.Tag)
	}
	if !ts.Raw {
		t.Errorf("expected Raw=true, got %v", ts.Raw)
	}
	if !ts.Triple {
		t.Errorf("expected Triple=true, got %v", ts.Triple)
	}
	if len(ts.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(ts.Parts))
	}
	text, ok := ts.Parts[0].(ast.StringText)
	if !ok || text.Value != "echo ${HOME}" {
		t.Errorf("expected StringText{echo ${HOME}}, got %T %v", ts.Parts[0], ts.Parts[0])
	}
}

// TestParser_AnonStructTypeInTuple verifies that an anonymous struct type
// can appear as an element of a distinct-tuple type:
// `type Tagged (String, {name: String, age: Int})`. The InnerTypeExpr is a
// tuple-form FuncType (Return == nil) whose second element is an
// AnonStructType with two StructField entries, no defaults.
func TestParser_AnonStructTypeInTuple(t *testing.T) {
	nodes := parse(t, "type Tagged (String, {name: String, age: Int})\n")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("expected *ast.TypeDef, got %T", nodes[0])
	}
	if td.Name != "Tagged" {
		t.Errorf("expected name Tagged, got %q", td.Name)
	}
	ft, ok := td.InnerTypeExpr.(*ast.FuncType)
	if !ok {
		t.Fatalf("expected InnerTypeExpr to be *ast.FuncType (tuple form), got %T", td.InnerTypeExpr)
	}
	if ft.Return != nil {
		t.Errorf("expected tuple form (Return == nil), got Return=%v", ft.Return)
	}
	if len(ft.Params) != 2 {
		t.Fatalf("expected 2 tuple elements, got %d", len(ft.Params))
	}
	if s, ok := ft.Params[0].(*ast.SimpleType); !ok || s.Name != "String" {
		t.Errorf("expected first element SimpleType{String}, got %T %v", ft.Params[0], ft.Params[0])
	}
	ast0, ok := ft.Params[1].(*ast.AnonStructType)
	if !ok {
		t.Fatalf("expected second element *ast.AnonStructType, got %T", ft.Params[1])
	}
	if len(ast0.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(ast0.Fields))
	}
	if ast0.Fields[0].Name != "name" || ast0.Fields[0].TypeAnnotation.TypeString() != "String" || ast0.Fields[0].Default != nil {
		t.Errorf("field 0 = %+v", ast0.Fields[0])
	}
	if ast0.Fields[1].Name != "age" || ast0.Fields[1].TypeAnnotation.TypeString() != "Int" || ast0.Fields[1].Default != nil {
		t.Errorf("field 1 = %+v", ast0.Fields[1])
	}
}

// TestParser_AnonStructTypeInFunctionParam verifies an anon struct type as
// a function parameter annotation.
func TestParser_AnonStructTypeInFunctionParam(t *testing.T) {
	nodes := parse(t, "fn greet(p: {name: String, age: Int}): String { p.name }\n")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected *ast.FuncDef, got %T", nodes[0])
	}
	if len(fd.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(fd.Params))
	}
	ast0, ok := fd.Params[0].TypeAnnotation.(*ast.AnonStructType)
	if !ok {
		t.Fatalf("expected param TypeAnnotation *ast.AnonStructType, got %T", fd.Params[0].TypeAnnotation)
	}
	if len(ast0.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(ast0.Fields))
	}
	if ast0.Fields[0].Name != "name" || ast0.Fields[0].TypeAnnotation.TypeString() != "String" {
		t.Errorf("field 0 = %+v", ast0.Fields[0])
	}
	if ast0.Fields[1].Name != "age" || ast0.Fields[1].TypeAnnotation.TypeString() != "Int" {
		t.Errorf("field 1 = %+v", ast0.Fields[1])
	}
}

// TestParser_AnonStructTypeInGenericArg verifies an anon struct type as a
// generic type argument: `List<{x: Int, y: Int}>`. The function param's
// TypeAnnotation is a GenericType whose Params[0] is the AnonStructType.
func TestParser_AnonStructTypeInGenericArg(t *testing.T) {
	nodes := parse(t, "fn first(_xs: List<{x: Int, y: Int}>): Int { 0 }\n")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(nodes))
	}
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected *ast.FuncDef, got %T", nodes[0])
	}
	if len(fd.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(fd.Params))
	}
	gt, ok := fd.Params[0].TypeAnnotation.(*ast.GenericType)
	if !ok {
		t.Fatalf("expected param TypeAnnotation *ast.GenericType, got %T", fd.Params[0].TypeAnnotation)
	}
	if gt.Name != "List" {
		t.Errorf("expected GenericType.Name=List, got %q", gt.Name)
	}
	if len(gt.Params) != 1 {
		t.Fatalf("expected 1 generic arg, got %d", len(gt.Params))
	}
	ast0, ok := gt.Params[0].(*ast.AnonStructType)
	if !ok {
		t.Fatalf("expected generic arg *ast.AnonStructType, got %T", gt.Params[0])
	}
	if len(ast0.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(ast0.Fields))
	}
	if ast0.Fields[0].Name != "x" || ast0.Fields[0].TypeAnnotation.TypeString() != "Int" {
		t.Errorf("field 0 = %+v", ast0.Fields[0])
	}
	if ast0.Fields[1].Name != "y" || ast0.Fields[1].TypeAnnotation.TypeString() != "Int" {
		t.Errorf("field 1 = %+v", ast0.Fields[1])
	}
}

// TestParser_AnonStructType_RejectedAtTopLevelType pins the carve-out: an
// anon struct as the *bare RHS* of a top-level `type` decl is rejected
// (the `struct` keyword is the only way to declare a top-level record).
// The rejection at parser.go's parseTypeDef LBRACE check fires *before*
// parseTypeAnnotation is called, so adding LBRACE handling there cannot
// open this loophole.
func TestParser_AnonStructType_RejectedAtTopLevelType(t *testing.T) {
	tokens := lexer.Lex("type MyStruct {x: Int}\n")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "distinct `type` declarations do not take bodies") {
		t.Errorf("expected type-body rejection, got %q", err.Error())
	}
}

// TestParser_AnonStructType_RequiresSeparatorBetweenFields pins the rule
// that adjacent fields must be separated by a comma or a newline. Run-on
// fields like `{a: Int b: Int}` are rejected — they're indistinguishable
// from a field type that happens to start with another identifier, and
// reading them is unpleasant.
func TestParser_AnonStructType_RequiresSeparatorBetweenFields(t *testing.T) {
	tokens := lexer.Lex("fn f(p: {a: Int b: Int}): Int { p.a }\n")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "expected ',' or newline between fields") {
		t.Errorf("expected error containing %q, got %q", "expected ',' or newline between fields", err.Error())
	}
}

// findFuncDef scans top-level parse output for a *ast.FuncDef with the
// given name. Used by the attached-test tests below to pluck
// the function out of a multi-statement parse.
func findFuncDef(t *testing.T, nodes []ast.Node, name string) *ast.FuncDef {
	t.Helper()
	for _, n := range nodes {
		if fn, ok := n.(*ast.FuncDef); ok && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("FuncDef %q not found in parse output", name)
	return nil
}

func TestParseAttachedTestOnFunction(t *testing.T) {
	nodes := parse(t, `//! assert answer() == 42
fn answer(): Int { 42 }
`)
	fn := findFuncDef(t, nodes, "answer")
	if len(fn.AttachedTests) != 1 {
		t.Fatalf("expected 1 attached test, got %d", len(fn.AttachedTests))
	}
	if len(fn.AttachedTests[0].Body.Stmts) != 1 {
		t.Fatalf("expected attached test body with 1 stmt, got %d", len(fn.AttachedTests[0].Body.Stmts))
	}
	if _, ok := fn.AttachedTests[0].Body.Stmts[0].(*ast.Assertion); !ok {
		t.Fatalf("expected attached test assertion, got %T", fn.AttachedTests[0].Body.Stmts[0])
	}
	if fn.AttachedTests[0].Kind != "test" {
		t.Fatalf("expected test attached test kind, got %q", fn.AttachedTests[0].Kind)
	}
}

func TestParseAttachedTestMergesSurroundingDocComments(t *testing.T) {
	nodes := parse(t, `/// Computes the answer.
//! assert answer() == 42
/// Used by the examples.
fn answer(): Int { 42 }
`)
	fn := findFuncDef(t, nodes, "answer")
	if fn.Doc != "Computes the answer.\nUsed by the examples." {
		t.Fatalf("unexpected doc comment: %q", fn.Doc)
	}
	if len(fn.AttachedTests) != 1 {
		t.Fatalf("expected 1 attached test, got %d", len(fn.AttachedTests))
	}
	if fn.AttachedTests[0].DocAfter != "Used by the examples." {
		t.Fatalf("expected attached test to remember following doc comment, got %q", fn.AttachedTests[0].DocAfter)
	}
}

func TestParseAttachedBlockTestOnFunction(t *testing.T) {
	nodes := parse(t, `//! value = answer()
//! assert value == 42
//! refute value == 41
fn answer(): Int { 42 }
`)
	fn := findFuncDef(t, nodes, "answer")
	if len(fn.AttachedTests) != 1 {
		t.Fatalf("expected 1 attached test, got %d", len(fn.AttachedTests))
	}
	test := fn.AttachedTests[0]
	if test.Kind != "test" {
		t.Fatalf("expected test attached test kind, got %q", test.Kind)
	}
	if test.Inline {
		t.Fatal("expected block test to not be inline")
	}
	if len(test.Body.Stmts) != 3 {
		t.Fatalf("expected attached test body with 3 stmts, got %d", len(test.Body.Stmts))
	}
	if _, ok := test.Body.Stmts[1].(*ast.Assertion); !ok {
		t.Fatalf("expected attached test assertion, got %T", test.Body.Stmts[1])
	}
	if _, ok := test.Body.Stmts[2].(*ast.Assertion); !ok {
		t.Fatalf("expected attached test refutation, got %T", test.Body.Stmts[2])
	}
}

func TestParseAttachedCommentBlockTestOnFunction(t *testing.T) {
	nodes := parse(t, `/// Computes the answer.
//! value = answer()
//! assert value == 42
//! refute value == 41
/// Used by the examples.
fn answer(): Int { 42 }
`)
	fn := findFuncDef(t, nodes, "answer")
	if fn.Doc != "Computes the answer.\nUsed by the examples." {
		t.Fatalf("unexpected doc comment: %q", fn.Doc)
	}
	if len(fn.AttachedTests) != 1 {
		t.Fatalf("expected 1 attached test, got %d", len(fn.AttachedTests))
	}
	test := fn.AttachedTests[0]
	if test.Kind != "test" {
		t.Fatalf("expected test attached test kind, got %q", test.Kind)
	}
	if test.Inline {
		t.Fatal("expected doc block test to not be inline")
	}
	if len(test.Body.Stmts) != 3 {
		t.Fatalf("expected attached test body with 3 stmts, got %d", len(test.Body.Stmts))
	}
	if test.DocAfter != "Used by the examples." {
		t.Fatalf("expected attached test to remember following doc comment, got %q", test.DocAfter)
	}
}

func TestParseStripsBlankLineBetweenAttachedTestAndDeclaration(t *testing.T) {
	nodes := parse(t, `//! assert answer() == 42

fn answer(): Int { 42 }
`)
	fn := findFuncDef(t, nodes, "answer")
	if len(fn.AttachedTests) != 1 {
		t.Fatalf("expected 1 attached test, got %d", len(fn.AttachedTests))
	}
	if len(fn.AttachedTests[0].After) != 0 {
		t.Fatalf("expected blank line to be stripped from attached test prelude, got %#v", fn.AttachedTests[0].After)
	}
}

func TestParseAttachedTestPreservesTrailingPromptBlank(t *testing.T) {
	nodes := parse(t, `//! assert answer() == 42
//!
fn answer(): Int { 42 }
`)
	fn := findFuncDef(t, nodes, "answer")
	if len(fn.AttachedTests) != 1 {
		t.Fatalf("expected 1 attached test, got %d", len(fn.AttachedTests))
	}
	if fn.AttachedTests[0].TrailingPromptBlanks != 1 {
		t.Fatalf("expected trailing prompt blank to be preserved, got %d", fn.AttachedTests[0].TrailingPromptBlanks)
	}
}

func TestParseStripsBlankLineBetweenAttachedTestBlocks(t *testing.T) {
	nodes := parse(t, `//! assert answer() == 42

//! refute answer() == 41
//!
fn answer(): Int { 42 }
`)
	fn := findFuncDef(t, nodes, "answer")
	if len(fn.AttachedTests) != 2 {
		t.Fatalf("expected 2 attached tests, got %d", len(fn.AttachedTests))
	}
	if len(fn.AttachedTests[0].After) != 0 {
		t.Fatalf("expected blank separator to be stripped, got %#v", fn.AttachedTests[0].After)
	}
	if fn.AttachedTests[1].Line != 3 {
		t.Fatalf("expected second attached test on line 3, got %d", fn.AttachedTests[1].Line)
	}
}

func TestParseCommentSeparatedAttachedTestBlocksOnFunction(t *testing.T) {
	for _, separator := range []string{"///", "//"} {
		t.Run(separator, func(t *testing.T) {
			nodes := parse(t, `//! assert answer() == 42
`+separator+`
//! refute answer() == 41
fn answer(): Int { 42 }
`)
			fn := findFuncDef(t, nodes, "answer")
			if len(fn.AttachedTests) != 2 {
				t.Fatalf("expected 2 attached tests, got %d", len(fn.AttachedTests))
			}
			if len(fn.AttachedTests[0].After) != 1 {
				t.Fatalf("expected separator trivia on first attached test, got %#v", fn.AttachedTests[0].After)
			}
			if fn.AttachedTests[1].Line != 3 {
				t.Fatalf("expected second attached test on line 3, got %d", fn.AttachedTests[1].Line)
			}
		})
	}
}

func TestParseAttachedTestPreservesBlankDocAfter(t *testing.T) {
	nodes := parse(t, `/// Computes the answer.
//! assert answer() == 42
/// Used by examples.
///
fn answer(): Int { 42 }
`)
	fn := findFuncDef(t, nodes, "answer")
	if fn.Doc != "Computes the answer.\nUsed by examples.\n" {
		t.Fatalf("unexpected doc comment: %q", fn.Doc)
	}
	if len(fn.AttachedTests) != 1 {
		t.Fatalf("expected 1 attached test, got %d", len(fn.AttachedTests))
	}
	test := fn.AttachedTests[0]
	if test.DocAfter != "Used by examples.\n" {
		t.Fatalf("expected attached test to remember trailing blank doc line, got %q", test.DocAfter)
	}
	if len(test.After) != 2 {
		t.Fatalf("expected 2 post-attached items, got %#v", test.After)
	}
	if !test.After[0].IsDoc || test.After[0].Doc != "Used by examples." {
		t.Fatalf("expected first post-attached item to be doc text, got %#v", test.After[0])
	}
	if !test.After[1].IsDoc || test.After[1].Doc != "" {
		t.Fatalf("expected second post-attached item to be blank doc line, got %#v", test.After[1])
	}
}

func TestParseRejectsTestWithoutAssertion(t *testing.T) {
	_, err := Parse(lexer.Lex(`test "only setup" {
  value = 42
}
`))
	if err == nil {
		t.Fatal("expected test without assertion to be rejected")
	}
	if !strings.Contains(err.Error(), "test block must contain at least one assert or refute") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsTestsGroupWithoutAssertion(t *testing.T) {
	_, err := Parse(lexer.Lex(`tests "group" {
  test "only setup" {
    value = 42
  }
}
`))
	if err == nil {
		t.Fatal("expected tests group without assertion to be rejected")
	}
	if !strings.Contains(err.Error(), "test block must contain at least one assert or refute") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A group's `boot` line holds the call to an entry's boot, as written.
func TestParseTestsBootExpression(t *testing.T) {
	nodes, err := Parse(lexer.Lex(`tests "env" {
  clock Clock.Virtual
  boot server.boot(startup())
  setup Config{name: "Ada"}

  test "reads active app" {
    assert AppEnv.label == "test"
  }
}
`))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	decl, ok := nodes[0].(*ast.TestDecl)
	if !ok {
		t.Fatalf("expected TestDecl, got %T", nodes[0])
	}
	call, ok := decl.Boot.(*ast.Call)
	if !ok {
		t.Fatalf("expected the boot line to be a call, got %T", decl.Boot)
	}
	fa, ok := call.Func.(*ast.FieldAccess)
	if !ok || fa.Field.Name != "boot" {
		t.Fatalf("expected `server.boot` as the callee, got %#v", call.Func)
	}
	if len(call.Args) != 1 {
		t.Fatalf("expected one argument, got %d", len(call.Args))
	}
	if decl.Clock == nil || decl.Setup == nil {
		t.Fatalf("clock %v, setup %v", decl.Clock, decl.Setup)
	}
}

// The group's declarations are each at most once, and a group neither
// defines a boot function nor nests another group.
func TestParseTestsGroupRejects(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"nested group": {
			"tests \"outer\" {\n  tests \"inner\" {\n    test \"t\" { assert true }\n  }\n}\n",
			"write a sibling group instead",
		},
		"fn boot": {
			"tests \"g\" {\n  fn boot(startup: Startup): App { App{} }\n  test \"t\" { assert true }\n}\n",
			"a `tests` group does not define `fn boot`",
		},
		"duplicate clock": {
			"tests \"g\" {\n  clock Clock.Virtual\n  clock Clock.System\n  test \"t\" { assert true }\n}\n",
			"a `tests` group has at most one `clock` line",
		},
		"duplicate boot": {
			"tests \"g\" {\n  boot server.boot(s)\n  boot server.boot(s)\n  test \"t\" { assert true }\n}\n",
			"a `tests` group has at most one `boot` line",
		},
		"duplicate setup": {
			"tests \"g\" {\n  setup 1\n  setup 2\n  test \"t\" { assert true }\n}\n",
			"a `tests` group has at most one `setup` line",
		},
		"duplicate setup separated by a test": {
			"tests \"g\" {\n  setup 1\n  test \"t\" { assert true }\n  setup 2\n}\n",
			"a `tests` group has at most one `setup` line",
		},
		"duplicate clock after boot and setup": {
			"tests \"g\" {\n  clock Clock.Virtual\n  boot server.boot(s)\n  setup 1\n  clock Clock.Virtual\n  test \"t\" { assert true }\n}\n",
			"a `tests` group has at most one `clock` line",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(lexer.Lex(tc.src))
			if err == nil {
				t.Fatalf("parsed; want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// A group's clock, boot and setup lines may appear in any order, before,
// between or after its tests: they run in a fixed order whatever their
// position.
func TestParseTestsGroupLinesInAnyOrder(t *testing.T) {
	for name, src := range map[string]string{
		"setup, boot, clock": "tests \"g\" {\n  setup 1\n  boot server.boot(s)\n  clock Clock.Virtual\n  test \"t\" { assert true }\n}\n",
		"between tests":      "tests \"g\" {\n  test \"a\" { assert true }\n  boot server.boot(s)\n  test \"b\" { assert true }\n  setup 1\n  test \"c\" { assert true }\n  clock Clock.Virtual\n}\n",
		"after tests":        "tests \"g\" {\n  test \"a\" { assert true }\n  clock Clock.Virtual\n  setup 1\n  boot server.boot(s)\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			nodes, err := Parse(lexer.Lex(src))
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			decl, ok := nodes[0].(*ast.TestDecl)
			if !ok {
				t.Fatalf("expected TestDecl, got %T", nodes[0])
			}
			if decl.Clock == nil || decl.Boot == nil || decl.Setup == nil {
				t.Fatalf("clock %v, boot %v, setup %v", decl.Clock, decl.Boot, decl.Setup)
			}
			for _, stmt := range decl.Body.Stmts {
				if _, ok := stmt.(*ast.TestDecl); !ok {
					t.Fatalf("group body holds %T; want only its tests", stmt)
				}
			}
		})
	}
}

func TestParseTestsSetupExpression(t *testing.T) {
	nodes, err := Parse(lexer.Lex(`tests "fixture" {
  setup Config{name: "Ada"}

  test "reads setup", {name} {
    assert name == "Ada"
  }
}
`))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	decl, ok := nodes[0].(*ast.TestDecl)
	if !ok {
		t.Fatalf("expected TestDecl, got %T", nodes[0])
	}
	if decl.Setup == nil {
		t.Fatal("expected setup expression")
	}
	block, ok := decl.Setup.(*ast.Block)
	if !ok {
		t.Fatalf("expected setup expression to be stored as a block body, got %T", decl.Setup)
	}
	if len(block.Stmts) != 1 {
		t.Fatalf("expected single setup stmt, got %d", len(block.Stmts))
	}
	es, ok := block.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected setup expr stmt, got %T", block.Stmts[0])
	}
	if _, ok := es.Expr.(*ast.StructLit); !ok {
		t.Fatalf("expected setup struct literal, got %T", es.Expr)
	}
}

func TestParseRejectsAttachedTestWithoutAssertion(t *testing.T) {
	_, err := Parse(lexer.Lex(`//! value = answer()
fn answer(): Int { 42 }
`))
	if err == nil {
		t.Fatal("expected attached test without assertion to be rejected")
	}
	if !strings.Contains(err.Error(), "attached test must contain at least one assert or refute") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseAttachedTestOnTypeBodyExternFunction(t *testing.T) {
	nodes := parse(t, `host type Int

impl Display for Int {
  /// Converts an int.
  //! assert Int.to_string(42) == "42"
  /// Matches interpolation.
  // Host implementation lives in Go.
  host fn to_string(n: self): String
}

`)
	if len(nodes) != 2 {
		t.Fatalf("expected host type + impl block, got %d nodes", len(nodes))
	}
	ext, ok := nodes[0].(*ast.ExternType)
	if !ok {
		t.Fatalf("expected ExternType, got %T", nodes[0])
	}
	if len(ext.Items) != 0 {
		t.Fatalf("expected no host type items, got %d", len(ext.Items))
	}
	impl, ok := nodes[1].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("expected ImplBlock, got %T", nodes[1])
	}
	if len(impl.Items) != 1 {
		t.Fatalf("expected 1 impl item, got %d", len(impl.Items))
	}
	fn, ok := impl.Items[0].(*ast.ExternFunc)
	if !ok {
		t.Fatalf("expected ExternFunc, got %T", impl.Items[0])
	}
	if len(fn.AttachedTests) != 1 {
		t.Fatalf("expected 1 attached test on host fn, got %d", len(fn.AttachedTests))
	}
	if fn.Doc != "Converts an int.\nMatches interpolation." {
		t.Fatalf("unexpected host fn doc comment: %q", fn.Doc)
	}
	after := fn.AttachedTests[0].After
	if len(after) != 2 {
		t.Fatalf("expected 2 post-attached items, got %#v", after)
	}
	if after[0].Doc != "Matches interpolation." {
		t.Fatalf("expected post-attached doc first, got %#v", after[0])
	}
	if after[1].Trivia.Text != "// Host implementation lives in Go." {
		t.Fatalf("expected ordinary comment after attached test, got %#v", after[1])
	}
}

func TestParseAttachedTestAllowsAnnotatedBindingInBlock(t *testing.T) {
	nodes := parse(t, `//! value: Maybe<Int> = None
//! assert value == None
fn ready(): Bool { True }
`)
	fn := findFuncDef(t, nodes, "ready")
	if len(fn.AttachedTests) != 1 {
		t.Fatalf("expected 1 attached test, got %d", len(fn.AttachedTests))
	}
	block := fn.AttachedTests[0].Body
	if len(block.Stmts) != 2 {
		t.Fatalf("expected attached test block with 2 stmts, got %d", len(block.Stmts))
	}
	binding, ok := block.Stmts[0].(*ast.Binding)
	if !ok {
		t.Fatalf("expected annotated binding in test block, got %T", block.Stmts[0])
	}
	if binding.TypeAnnotation == nil || binding.TypeAnnotation.TypeString() != "Maybe<Int>" {
		t.Fatalf("expected Maybe<Int> annotation, got %v", binding.TypeAnnotation)
	}
}

// findStructDef / findEnumDef / findTypeDef are the type-decl analogues of
// findFuncDef: scan top-level parse output for a node of the given kind
// with the given name and fail the test if it isn't there.
func findStructDef(t *testing.T, nodes []ast.Node, name string) *ast.StructDef {
	t.Helper()
	for _, n := range nodes {
		if s, ok := n.(*ast.StructDef); ok && s.Name == name {
			return s
		}
	}
	t.Fatalf("StructDef %q not found in parse output", name)
	return nil
}

func findEnumDef(t *testing.T, nodes []ast.Node, name string) *ast.EnumDef {
	t.Helper()
	for _, n := range nodes {
		if e, ok := n.(*ast.EnumDef); ok && e.Name == name {
			return e
		}
	}
	t.Fatalf("EnumDef %q not found in parse output", name)
	return nil
}

func findTypeDef(t *testing.T, nodes []ast.Node, name string) *ast.TypeDef {
	t.Helper()
	for _, n := range nodes {
		if td, ok := n.(*ast.TypeDef); ok && td.Name == name {
			return td
		}
	}
	t.Fatalf("TypeDef %q not found in parse output", name)
	return nil
}

// `@derive` from source is not a decorator — it is rejected at the
// decorator-name stage for every declaration kind (struct, enum, type) with
// a targeted message pointing at the `derive Iface` line.
func TestParseSourceDeriveRejectedOnTypeDecls(t *testing.T) {
	for _, src := range []string{
		"@derive Equatable, Hashable\nstruct Point { x: Int; y: Int }",
		"@derive Debug\nenum Color { Red; Green; Blue }",
		"@derive Hashable\ntype Id Int",
	} {
		_, err := Parse(lexer.Lex(src))
		if err == nil {
			t.Fatalf("Parse(%q): expected `@derive` rejection, got none", src)
		}
		if !strings.Contains(err.Error(), "`@derive` is not supported") {
			t.Errorf("Parse(%q): expected `@derive` rejection, got %q", src, err.Error())
		}
	}
}

// Dot-leading variant resolution: parser tests.
//
// `.Variant` at expression position resolves to a contextual enum's variant.
// The parser emits a *ast.DotVariant for the bare and call-form cases, and
// a literal node (StructLit / ListLit / MapLit) with a *ast.DotVariantType
// TypeName for the literal-attach forms. See ast/ast.go.

func TestParseDotVariant_Bare(t *testing.T) {
	expr := parseExpr(t, ".Red")
	dv, ok := expr.(*ast.DotVariant)
	if !ok {
		t.Fatalf("expected *ast.DotVariant, got %T", expr)
	}
	if dv.Name != "Red" {
		t.Errorf("expected Name 'Red', got %q", dv.Name)
	}
}

func TestParseDotVariant_Call(t *testing.T) {
	expr := parseExpr(t, ".Circle(1.0)")
	call, ok := expr.(*ast.Call)
	if !ok {
		t.Fatalf("expected *ast.Call, got %T", expr)
	}
	dv, ok := call.Func.(*ast.DotVariant)
	if !ok {
		t.Fatalf("expected Call.Func *ast.DotVariant, got %T", call.Func)
	}
	if dv.Name != "Circle" {
		t.Errorf("expected Name 'Circle', got %q", dv.Name)
	}
	if len(call.Args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(call.Args))
	}
	if _, ok := call.Args[0].(*ast.FloatLit); !ok {
		t.Errorf("expected FloatLit arg, got %T", call.Args[0])
	}
}

func TestParseDotVariant_StructLit(t *testing.T) {
	expr := parseExpr(t, ".Rect{w: 4.0, h: 3.0}")
	sl, ok := expr.(*ast.StructLit)
	if !ok {
		t.Fatalf("expected *ast.StructLit, got %T", expr)
	}
	dvt, ok := sl.TypeName.(*ast.DotVariantType)
	if !ok {
		t.Fatalf("expected TypeName *ast.DotVariantType, got %T", sl.TypeName)
	}
	if dvt.Name != "Rect" {
		t.Errorf("expected Name 'Rect', got %q", dvt.Name)
	}
	if len(sl.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(sl.Fields))
	}
}

func TestParseDotVariant_MapLit(t *testing.T) {
	expr := parseExpr(t, `.Obj{"k" => 1}`)
	ml, ok := expr.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected *ast.MapLit, got %T", expr)
	}
	dvt, ok := ml.TypeName.(*ast.DotVariantType)
	if !ok {
		t.Fatalf("expected TypeName *ast.DotVariantType, got %T", ml.TypeName)
	}
	if dvt.Name != "Obj" {
		t.Errorf("expected Name 'Obj', got %q", dvt.Name)
	}
}

func TestParseDotVariant_ListLit(t *testing.T) {
	expr := parseExpr(t, ".Arr[1, 2, 3]")
	ll, ok := expr.(*ast.ListLit)
	if !ok {
		t.Fatalf("expected *ast.ListLit, got %T", expr)
	}
	dvt, ok := ll.TypeName.(*ast.DotVariantType)
	if !ok {
		t.Fatalf("expected TypeName *ast.DotVariantType, got %T", ll.TypeName)
	}
	if dvt.Name != "Arr" {
		t.Errorf("expected Name 'Arr', got %q", dvt.Name)
	}
	if len(ll.Items) != 3 {
		t.Errorf("expected 3 items, got %d", len(ll.Items))
	}
}

func TestParseDotVariant_BareInPattern(t *testing.T) {
	// case x { .Red -> 1 }
	src := `case x { .Red -> 1 }`
	expr := parseExpr(t, src)
	cs, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	if len(cs.Branches) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(cs.Branches))
	}
	ep, ok := cs.Branches[0].Pattern.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("expected EnumPattern, got %T", cs.Branches[0].Pattern)
	}
	dvt, ok := ep.Variant.(*ast.DotVariantType)
	if !ok {
		t.Fatalf("expected Variant *ast.DotVariantType, got %T", ep.Variant)
	}
	if dvt.Name != "Red" {
		t.Errorf("expected Name 'Red', got %q", dvt.Name)
	}
}

func TestParseDotVariant_CallInPattern(t *testing.T) {
	// case x { .Circle(r) -> r }
	src := `case x { .Circle(r) -> r }`
	expr := parseExpr(t, src)
	cs, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	ep, ok := cs.Branches[0].Pattern.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("expected EnumPattern, got %T", cs.Branches[0].Pattern)
	}
	dvt, ok := ep.Variant.(*ast.DotVariantType)
	if !ok {
		t.Fatalf("expected Variant *ast.DotVariantType, got %T", ep.Variant)
	}
	if dvt.Name != "Circle" {
		t.Errorf("expected Name 'Circle', got %q", dvt.Name)
	}
	if ep.Binding != "r" {
		t.Errorf("expected binding 'r', got %q", ep.Binding)
	}
}

func TestParseDotVariant_StructPatternInPattern(t *testing.T) {
	// case x { .Rect{w, h} -> w * h }
	src := `case x { .Rect{w, h} -> w * h }`
	expr := parseExpr(t, src)
	cs, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	sp, ok := cs.Branches[0].Pattern.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected StructPattern, got %T", cs.Branches[0].Pattern)
	}
	dvt, ok := sp.TypeName.(*ast.DotVariantType)
	if !ok {
		t.Fatalf("expected TypeName *ast.DotVariantType, got %T", sp.TypeName)
	}
	if dvt.Name != "Rect" {
		t.Errorf("expected Name 'Rect', got %q", dvt.Name)
	}
}

func TestParseDotVariant_MapPatternInPattern(t *testing.T) {
	// case x { .Obj{"k" => v} -> v }
	src := `case x { .Obj{"k" => v} -> v }`
	expr := parseExpr(t, src)
	cs, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	mp, ok := cs.Branches[0].Pattern.(*ast.MapPattern)
	if !ok {
		t.Fatalf("expected MapPattern, got %T", cs.Branches[0].Pattern)
	}
	dvt, ok := mp.TypeName.(*ast.DotVariantType)
	if !ok {
		t.Fatalf("expected TypeName *ast.DotVariantType, got %T", mp.TypeName)
	}
	if dvt.Name != "Obj" {
		t.Errorf("expected Name 'Obj', got %q", dvt.Name)
	}
}

func TestParseDotVariant_ListPatternInPattern(t *testing.T) {
	// case x { .Arr[a, b] -> a + b }
	src := `case x { .Arr[a, b] -> a + b }`
	expr := parseExpr(t, src)
	cs, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	lp, ok := cs.Branches[0].Pattern.(*ast.ListPattern)
	if !ok {
		t.Fatalf("expected ListPattern, got %T", cs.Branches[0].Pattern)
	}
	dvt, ok := lp.TypeName.(*ast.DotVariantType)
	if !ok {
		t.Fatalf("expected TypeName *ast.DotVariantType, got %T", lp.TypeName)
	}
	if dvt.Name != "Arr" {
		t.Errorf("expected Name 'Arr', got %q", dvt.Name)
	}
}

// A broken list pattern carries a trailing comma, as nomi fmt writes it, in
// both the plain and the type-prefixed spelling.
func TestParseListPattern_TrailingComma(t *testing.T) {
	for _, src := range []string{
		"case x {\n    [\n        a,\n        b,\n    ] -> a + b\n}",
		"case x {\n    .Arr[\n        .Num(n),\n    ] -> n\n}",
	} {
		expr := parseExpr(t, src)
		cs, ok := expr.(*ast.Case)
		if !ok {
			t.Fatalf("%q: expected *ast.Case, got %T", src, expr)
		}
		lp, ok := cs.Branches[0].Pattern.(*ast.ListPattern)
		if !ok {
			t.Fatalf("%q: expected ListPattern, got %T", src, cs.Branches[0].Pattern)
		}
		if lp.TailSpread != nil || len(lp.Heads) == 0 {
			t.Fatalf("%q: heads %v, tail %v", src, lp.Heads, lp.TailSpread)
		}
	}
}

func TestParseTopLevelInherentImplAccepted(t *testing.T) {
	src := `impl List<T> {
  pub once Empty = []
  pub fn first(_xs: self): T { panic("x") }
}`
	nodes := parse(t, src)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	blk, ok := nodes[0].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("expected *ast.ImplBlock, got %T", nodes[0])
	}
	if blk.Interface != nil || blk.Receiver.TypeString() != "List<T>" || len(blk.Items) != 2 {
		t.Fatalf("expected inherent impl for List<T> with two items, got %+v", blk)
	}
	once, ok := blk.Items[0].(*ast.OnceBinding)
	if !ok || !once.Public || once.Name != "Empty" {
		t.Fatalf("expected public once Empty item, got %T %+v", blk.Items[0], blk.Items[0])
	}
	fn, ok := blk.Items[1].(*ast.FuncDef)
	if !ok || !fn.Public || fn.Name != "first" {
		t.Fatalf("expected public fn first item, got %T %+v", blk.Items[1], blk.Items[1])
	}
}

// `@derive` on an host type — like on any declaration — is rejected: a
// derived conformance uses a `derive Iface` line, not a decorator.
func TestParseDeriveOnExternTypeRejected(t *testing.T) {
	src := "@derive Equatable, Hashable\npub host type Tok\n"
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for @derive on host type, got nil")
	}
	if !strings.Contains(err.Error(), "`@derive` is not supported") {
		t.Errorf("expected `@derive` rejection, got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Interface-header surface form: `derive Iface` synthesized conformances and
// `impl Iface for Type { ... }` blocks. The old `impl` / `derive impl` /
// `derives` type-body spellings are rejected.
// ---------------------------------------------------------------------------

func structDefNamed(t *testing.T, src string) *ast.StructDef {
	t.Helper()
	nodes := parse(t, src)
	for _, n := range nodes {
		if sd, ok := n.(*ast.StructDef); ok {
			return sd
		}
	}
	t.Fatalf("Parse(%q): no *ast.StructDef among nodes", src)
	return nil
}

// A struct followed by a sibling derive declaration and interface impl block.
func TestParseImplHeader_StructBody(t *testing.T) {
	src := `pub struct Point {
  x: Int
  y: Int
}
derive Equatable for Point


impl Display for Point {
  fn to_string(_p: self): String {
    "pt"
  }
}

`
	sd := structDefNamed(t, src)
	if len(sd.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(sd.Fields))
	}
	if len(sd.Items) != 0 {
		t.Fatalf("expected no struct body items, got %d", len(sd.Items))
	}
	nodes := parse(t, src)
	derive, ok := nodes[1].(*ast.ImplConformance)
	if !ok || !derive.Derive || derive.Interface.TypeString() != "Equatable" || derive.Receiver.TypeString() != "Point" {
		t.Fatalf("expected `derive Equatable for Point`, got %T %+v", nodes[1], nodes[1])
	}
	if derive.Line == 0 || derive.Col == 0 {
		t.Errorf("expected real position on derive declaration line")
	}
	block, ok := nodes[2].(*ast.ImplBlock)
	if !ok || block.Interface.TypeString() != "Display" || len(block.Items) != 1 {
		t.Fatalf("expected top-level Display impl block with 1 method, got %T %+v", nodes[2], nodes[2])
	}
	if fn, ok := block.Items[0].(*ast.FuncDef); !ok || fn.Name != "to_string" {
		t.Errorf("expected block method to_string, got %T", block.Items[0])
	}
}

// An impl block may carry host fn methods.
func TestParseImplHeader_HostFn(t *testing.T) {
	src := `pub host type List<T>

impl iter for List<T> {
  host fn known_count(list: self): Maybe<Int>
}

`
	nodes := parse(t, src)
	et, ok := nodes[0].(*ast.ExternType)
	if !ok {
		t.Fatalf("expected *ast.ExternType, got %T", nodes[0])
	}
	if len(et.Items) != 0 {
		t.Fatalf("expected no host type body items, got %d", len(et.Items))
	}
	block, ok := nodes[1].(*ast.ImplBlock)
	if !ok || block.Interface.TypeString() != "iter" || len(block.Items) != 1 {
		t.Fatalf("expected iter impl block, got %T %+v", nodes[1], nodes[1])
	}
	ef, ok := block.Items[0].(*ast.ExternFunc)
	if !ok || ef.Name != "known_count" {
		t.Fatalf("expected host fn known_count, got %T %+v", block.Items[0], block.Items[0])
	}
}

// `impl Iface for Type { ... }` block — parses DIRECTLY into
// an *ast.ImplBlock with Interface + Receiver + plain-method Items.
func TestParseImplBlock_Simple(t *testing.T) {
	src := `impl Display for Money {
  fn to_string(_m: self): String { "$" }
}`
	nodes := parse(t, src)
	blk, ok := nodes[0].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("expected *ast.ImplBlock, got %T", nodes[0])
	}
	if blk.Interface == nil || blk.Interface.TypeString() != "Display" {
		t.Fatalf("expected interface Display, got %v", blk.Interface)
	}
	if blk.Receiver == nil || blk.Receiver.TypeString() != "Money" {
		t.Fatalf("expected receiver Money, got %v", blk.Receiver)
	}
	if len(blk.Generics) != 0 {
		t.Errorf("expected no generics, got %d", len(blk.Generics))
	}
	if blk.Line == 0 || blk.Col == 0 {
		t.Errorf("expected real position on impl block, got line=%d col=%d", blk.Line, blk.Col)
	}
	if blk.EndLine == 0 {
		t.Errorf("expected real EndLine on impl block")
	}
	if len(blk.Items) != 1 {
		t.Fatalf("expected 1 method item, got %d", len(blk.Items))
	}
	fn, ok := blk.Items[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected method to be *ast.FuncDef, got %T", blk.Items[0])
	}
	if fn.Name != "to_string" {
		t.Errorf("expected method to_string, got %q", fn.Name)
	}
	// Methods inside an `impl ... for` block are plain — no interface qualifier (the
	// header names the single interface).
	if fn.ImplIface != nil {
		t.Errorf("expected plain method (nil ImplIface), got %v", fn.ImplIface)
	}
}

// Two interfaces on one foreign type = two independent `impl ... for`
// blocks.
func TestParseImplBlock_TwoBlocks(t *testing.T) {
	src := `impl Display for Money {
  fn to_string(_m: self): String { "$" }
}

impl Comparable for Money {
  fn compare(_a: Money, _b: Money): Ordering { Ordering.Eq }
}`
	nodes := parse(t, src)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 top-level nodes, got %d", len(nodes))
	}
	for i, want := range []string{"Display", "Comparable"} {
		blk, ok := nodes[i].(*ast.ImplBlock)
		if !ok {
			t.Fatalf("node %d: expected *ast.ImplBlock, got %T", i, nodes[i])
		}
		if blk.Interface == nil || blk.Interface.TypeString() != want {
			t.Errorf("node %d: expected interface %s, got %v", i, want, blk.Interface)
		}
		if blk.Receiver == nil || blk.Receiver.TypeString() != "Money" {
			t.Errorf("node %d: expected receiver Money, got %v", i, blk.Receiver)
		}
	}
}

// `impl Iter for Box<T> { ... }` — receiver generics are named by the receiver.
func TestParseImplBlock_Generic(t *testing.T) {
	src := `impl Iter for Box<T> {
  fn next(_b: self): Maybe<(T, self)> { None }
}`
	nodes := parse(t, src)
	blk, ok := nodes[0].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("expected *ast.ImplBlock, got %T", nodes[0])
	}
	if blk.Interface == nil || blk.Interface.TypeString() != "Iter" {
		t.Fatalf("expected interface Iter, got %v", blk.Interface)
	}
	if blk.Receiver == nil || blk.Receiver.TypeString() != "Box<T>" {
		t.Fatalf("expected receiver Box<T>, got %v", blk.Receiver)
	}
	if len(blk.Generics) != 0 {
		t.Fatalf("expected no opener generics, got %#v", blk.Generics)
	}
}

func TestParseImplBlock_WhereClause(t *testing.T) {
	src := `impl iter for Box<T> where T: Display and Debug {
  fn next(_b: self): Maybe<(T, self)> { None }
}`
	nodes := parse(t, src)
	blk, ok := nodes[0].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("expected *ast.ImplBlock, got %T", nodes[0])
	}
	if len(blk.Generics) != 0 {
		t.Fatalf("expected no opener generics, got %#v", blk.Generics)
	}
	if len(blk.WhereClauses) != 1 {
		t.Fatalf("expected one where clause, got %#v", blk.WhereClauses)
	}
	if blk.WhereClauses[0].Name != "T" {
		t.Fatalf("expected where T, got %#v", blk.WhereClauses[0])
	}
	if got := len(blk.WhereClauses[0].Bounds); got != 2 {
		t.Fatalf("expected two where bounds, got %d", got)
	}
}

func TestParseTypeDeclarations_WhereClause(t *testing.T) {
	src := `struct Box<T> where T: Display {
  value: T
}

enum MaybeBox<T> where T: Display {
  Empty
  Full Box<T>
}

interface Renderable<T> where T: Display {
  fn render(value: self): String
}

host type Handle<T> where T: Display
`
	nodes := parse(t, src)
	if got := len(nodes); got != 4 {
		t.Fatalf("expected 4 nodes, got %d", got)
	}
	st, ok := nodes[0].(*ast.StructDef)
	if !ok || len(st.WhereClauses) != 1 || st.WhereClauses[0].Name != "T" {
		t.Fatalf("expected struct where T, got %T %#v", nodes[0], nodes[0])
	}
	en, ok := nodes[1].(*ast.EnumDef)
	if !ok || len(en.WhereClauses) != 1 || en.WhereClauses[0].Name != "T" {
		t.Fatalf("expected enum where T, got %T %#v", nodes[1], nodes[1])
	}
	iface, ok := nodes[2].(*ast.InterfaceDef)
	if !ok || len(iface.WhereClauses) != 1 || iface.WhereClauses[0].Name != "T" {
		t.Fatalf("expected interface where T, got %T %#v", nodes[2], nodes[2])
	}
	ext, ok := nodes[3].(*ast.ExternType)
	if !ok || len(ext.WhereClauses) != 1 || ext.WhereClauses[0].Name != "T" {
		t.Fatalf("expected host type where T, got %T %#v", nodes[3], nodes[3])
	}
}

func TestParseDeriveWhereClause(t *testing.T) {
	nodes := parse(t, `derive Display for Box<T> where T: Display`)
	conf, ok := nodes[0].(*ast.ImplConformance)
	if !ok {
		t.Fatalf("expected ImplConformance, got %T", nodes[0])
	}
	if !conf.Derive || conf.Interface.TypeString() != "Display" || conf.Receiver.TypeString() != "Box<T>" {
		t.Fatalf("unexpected derive conformance: %+v", conf)
	}
	if len(conf.WhereClauses) != 1 || conf.WhereClauses[0].Name != "T" {
		t.Fatalf("expected derive where T, got %#v", conf.WhereClauses)
	}
}

func TestParseDeriveOptions(t *testing.T) {
	nodes := parse(t, `derive ToJson for User with ToJson.Options{rename_all: Json.Case.Camel}`)
	conf, ok := nodes[0].(*ast.ImplConformance)
	if !ok {
		t.Fatalf("expected ImplConformance, got %T", nodes[0])
	}
	if !conf.Derive || conf.Interface.TypeString() != "ToJson" || conf.Receiver.TypeString() != "User" {
		t.Fatalf("unexpected derive conformance: %+v", conf)
	}
	options, ok := conf.Options.(*ast.StructLit)
	if !ok {
		t.Fatalf("expected struct-literal options, got %T", conf.Options)
	}
	if options.TypeName == nil || options.TypeName.TypeString() != "ToJson.Options" {
		t.Fatalf("expected ToJson.Options type name, got %v", options.TypeName)
	}
	if len(options.Fields) != 1 || options.Fields[0].Name != "rename_all" {
		t.Fatalf("expected rename_all option field, got %#v", options.Fields)
	}
}

func TestParseDeriveOptionsBeforeWhereClause(t *testing.T) {
	nodes := parse(t, `derive ToJson for Box<T> with ToJson.Options{rename_all: Json.Case.Camel} where T: ToJson`)
	conf, ok := nodes[0].(*ast.ImplConformance)
	if !ok {
		t.Fatalf("expected ImplConformance, got %T", nodes[0])
	}
	if conf.Options == nil {
		t.Fatalf("expected derive options")
	}
	if len(conf.WhereClauses) != 1 || conf.WhereClauses[0].Name != "T" {
		t.Fatalf("expected derive where T, got %#v", conf.WhereClauses)
	}
}

// `host fn` methods are admitted inside an `impl ... for` block.
func TestParseImplBlock_ExternFn(t *testing.T) {
	src := `impl iter for List<T> {
  host fn known_count(list: self): Maybe<Int>
}`
	nodes := parse(t, src)
	blk, ok := nodes[0].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("expected *ast.ImplBlock, got %T", nodes[0])
	}
	if len(blk.Items) != 1 {
		t.Fatalf("expected 1 method item, got %d", len(blk.Items))
	}
	ef, ok := blk.Items[0].(*ast.ExternFunc)
	if !ok {
		t.Fatalf("expected method to be *ast.ExternFunc, got %T", blk.Items[0])
	}
	if ef.Name != "known_count" {
		t.Errorf("expected host fn known_count, got %q", ef.Name)
	}
	if ef.ImplIface != nil {
		t.Errorf("expected plain extern method (nil ImplIface), got %v", ef.ImplIface)
	}
}

func TestParseImplBlock_PubMethodRejected(t *testing.T) {
	cases := []string{
		`impl Display for Money {
  pub fn to_string(m: self): String { "$" }
}`,
		`impl iter for List<T> {
  pub host fn known_count(list: self): Maybe<Int>
}`,
	}
	for _, src := range cases {
		tokens := lexer.Lex(src)
		_, err := Parse(tokens)
		if err == nil {
			t.Fatalf("expected parse error for pub method in impl block, got nil")
		}
		if !strings.Contains(err.Error(), "do not take `pub`") || !strings.Contains(err.Error(), "visibility is defined by the interface") {
			t.Errorf("expected pub visibility rejection, got %q", err.Error())
		}
	}
}

func TestParseImplBlock_InterfaceTypeArgsAccepted(t *testing.T) {
	src := `impl iter<T> for Box<T> {
  fn next(_b: self): Maybe<Int> { None }
}`
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("unexpected parse error for `impl iter<T> for ...`: %v", err)
	}
	blk, ok := nodes[0].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("expected an impl block, got %T", nodes[0])
	}
	if got := blk.Interface.TypeString(); got != "iter<T>" {
		t.Fatalf("expected interface type args to be preserved, got %q", got)
	}
}

// `@impl` is removed syntax and is rejected even where old code commonly used it.
func TestParseImplBlock_AtImplRejected(t *testing.T) {
	src := `impl Display for Money {
  @impl Display
  fn to_string(m: self): String { "$" }
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for `@impl` inside an impl block, got nil")
	}
	if !strings.Contains(err.Error(), "@impl") {
		t.Errorf("expected an '@impl' rejection, got %q", err.Error())
	}
}

// A nested conformance line inside an `impl ... for` block is rejected —
// the header already names the single interface.
func TestParseImplBlock_NestedConformanceRejected(t *testing.T) {
	src := `impl Display for Money {
  impl Comparable
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for a nested conformance line, got nil")
	}
	if !strings.Contains(err.Error(), "conformance") {
		t.Errorf("expected a conformance-line rejection, got %q", err.Error())
	}
}

// `impl Display Money` has neither a `for` clause nor an opening body.
func TestParseImplBlock_InvalidHeaderRejected(t *testing.T) {
	src := `impl Display Money {
  fn to_string(m: self): String { "$" }
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for a missing `for`, got nil")
	}
	if !strings.Contains(err.Error(), "expected `{` for an inherent impl block or `for Type`") {
		t.Errorf("expected an invalid-header rejection, got %q", err.Error())
	}
}

// --- Removed surface forms: the old spellings are rejected. ---

// `implements Display { ... }` is not the conformance keyword.
func TestParseImplHeader_InlineImplementsBodyRejected(t *testing.T) {
	src := `pub struct Point {
  x: Int

  implements Display {
    fn to_string(p: self): String { "pt" }
  }
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for an `implements Display` entry, got nil")
	}
	if !strings.Contains(err.Error(), "uses `impl`, not `implements`") {
		t.Errorf("expected `implements` rejection, got %q", err.Error())
	}
}

// A top-level `implements Iface for Type { ... }` block is not a surface
// form — use `impl Iface for Type { ... }`.
func TestParseImplHeader_TopLevelImplementsForRejected(t *testing.T) {
	src := `implements Display for Money {
  fn to_string(m: self): String { "$" }
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for a top-level `implements ... for ...` block, got nil")
	}
	if !strings.Contains(err.Error(), "uses `impl`, not `implements`") {
		t.Errorf("expected implements rejection, got %q", err.Error())
	}
}

// The `@derive` decorator is not a surface form — it is rejected at parse
// time, pointing at the `derive Iface` line instead.
func TestParseImplHeader_DeriveDecoratorRejected(t *testing.T) {
	src := "@derive Equatable, Hashable\npub struct Point {\n  x: Int\n}"
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for an `@derive` decorator, got nil")
	}
	if !strings.Contains(err.Error(), "`@derive` is not supported") {
		t.Errorf("expected `@derive` rejection, got %q", err.Error())
	}
}

// --- Cheap parse-time rejections. ---

func TestParseImplHeader_ConformanceTypeArgsRejected(t *testing.T) {
	src := `pub struct Foo {
  impl iter<T>
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for `impl iter<T>` conformance line, got nil")
	}
	if !strings.Contains(err.Error(), "interface implementations are written as `impl Iface for Type { ... }` blocks outside the type body") {
		t.Errorf("expected type-body impl rejection, got %q", err.Error())
	}
}

func TestParseImplHeader_AtImplRejected(t *testing.T) {
	src := `pub struct Foo {
  @impl
  fn to_string(f: self): String { "x" }
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for removed `@impl` syntax, got nil")
	}
	if !strings.Contains(err.Error(), "@impl") {
		t.Errorf("expected an '@impl' rejection, got %q", err.Error())
	}
}

func TestParseImplBlock_FieldItemRejected(t *testing.T) {
	src := `impl Display for Money {
  field cents: Int
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for `field` item inside an impl block, got nil")
	}
	if !strings.Contains(err.Error(), "field") {
		t.Errorf("expected a 'field' rejection, got %q", err.Error())
	}
}

// A module-qualified implementation header parses as a top-level impl block.
func TestParseImplHeader_QualifiedImplBlock(t *testing.T) {
	src := `pub struct Point {}

impl render.Display for Point {
  fn to_string(_p: self): String {
    "pt"
  }
}

`
	nodes := parse(t, src)
	block, ok := nodes[1].(*ast.ImplBlock)
	if !ok || block.Interface == nil || block.Interface.TypeString() != "render.Display" || len(block.Items) != 1 {
		t.Fatalf("expected `impl render.Display for Point { ... }`, got %T %+v", nodes[1], nodes[1])
	}
	if _, ok := block.Interface.(*ast.QualifiedType); !ok {
		t.Errorf("expected block interface to be *ast.QualifiedType, got %T", block.Interface)
	}
}

func TestParseImplHeader_PlainImplMethodNames(t *testing.T) {
	src := `pub struct Point {}

impl render.Display for Point {

  fn to_string(_p: self): String {
    "pt"
  }
}

impl Debug for Point
`
	nodes := parse(t, src)
	if len(nodes) != 3 {
		t.Fatalf("expected struct + 2 impl blocks, got %d", len(nodes))
	}
	block, ok := nodes[1].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("expected impl block, got %T", nodes[1])
	}
	fn, ok := block.Items[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected function item, got %T", block.Items[0])
	}
	if fn.Name != "to_string" || fn.ImplFunction || fn.ImplIface != nil || fn.ImplIfaceSourceQualified {
		t.Fatalf("expected plain to_string function, got %+v", fn)
	}
}

func TestParseImplHeader_QualifiedPlainFunctionRejected(t *testing.T) {
	src := `pub struct Point {
  impl Display

  fn Display.to_string(p: self): String {
    "pt"
  }
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for qualified plain fn, got nil")
	}
	if !strings.Contains(err.Error(), "interface implementations are written as `impl Iface for Type { ... }` blocks outside the type body") {
		t.Errorf("expected type-body impl rejection, got %q", err.Error())
	}
}

// @impl is removed syntax, not a type-body annotation.
func TestParseImplHeader_TagBeforeStructFieldRejected(t *testing.T) {
	src := `pub struct Foo {
  @impl Display
  x: Int
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for `@impl` before a struct field, got nil")
	}
	if !strings.Contains(err.Error(), "`@impl` is not supported") {
		t.Errorf("expected an @impl rejection, got %q", err.Error())
	}
}

// @impl before an enum `variant` item is rejected as removed syntax.
func TestParseImplHeader_TagBeforeEnumVariantRejected(t *testing.T) {
	src := `pub enum Color {
  @impl Display
  Red
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for `@impl` before an enum variant, got nil")
	}
	if !strings.Contains(err.Error(), "`@impl` is not supported") {
		t.Errorf("expected an @impl rejection, got %q", err.Error())
	}
}

// @impl before a non-method item is rejected as removed syntax.
func TestParseImplHeader_TagBeforeConformanceRejected(t *testing.T) {
	src := `pub struct Money {
  cents: Int

  @impl Display
  impl Comparable
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for `@impl` before a conformance line, got nil")
	}
	if !strings.Contains(err.Error(), "@impl") {
		t.Errorf("expected an '@impl' rejection, got %q", err.Error())
	}
}

// `derive Foo { ... }` in a type body is rejected; derives are sibling
// declarations and the impl is synthesized structurally.
func TestParseImplHeader_DeriveConformanceBodyRejected(t *testing.T) {
	src := `pub struct Foo {
  derive Equatable {
    fn equals(a: Foo, b: Foo): Bool { True }
  }
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for `derive Foo { ... }` with a body, got nil")
	}
	if !strings.Contains(err.Error(), "outside the type body") {
		t.Errorf("expected an outside-the-body rejection, got %q", err.Error())
	}
}

// `impl Iface for Type` written INSIDE a type body is rejected; manual
// interface implementations live as sibling top-level blocks.
func TestParseImplHeader_TypeBodyImplRejected(t *testing.T) {
	src := `pub struct Foo {
  impl Display for Bar {
    fn to_string(f: Bar): String { "x" }
  }
}`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for an impl block inside a type body, got nil")
	}
	if !strings.Contains(err.Error(), "outside the type body") {
		t.Errorf("expected a type-body impl rejection, got %q", err.Error())
	}
}

// `impl Display for Money` with no `{}` is a bodyless interface impl,
// equivalent to an empty implementation body.
func TestParseImplBlock_BodylessInterfaceImplAccepted(t *testing.T) {
	src := `impl Display for Money`
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("unexpected parse error for bodyless impl: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected one node, got %d", len(nodes))
	}
	impl, ok := nodes[0].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("expected ImplBlock, got %T", nodes[0])
	}
	if impl.Interface == nil || impl.Receiver == nil || len(impl.Items) != 0 {
		t.Fatalf("expected empty interface impl for Money, got %+v", impl)
	}
}

func TestParseImplBlock_BodylessInterfaceImplWhereAccepted(t *testing.T) {
	src := `impl Marker for Box<T> where T: Hashable`
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("unexpected parse error for bodyless impl with where: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected one node, got %d", len(nodes))
	}
	impl, ok := nodes[0].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("expected ImplBlock, got %T", nodes[0])
	}
	if impl.Interface == nil || impl.Receiver == nil || len(impl.Items) != 0 {
		t.Fatalf("expected empty interface impl for Box<T>, got %+v", impl)
	}
	if len(impl.WhereClauses) != 1 || impl.WhereClauses[0].Name != "T" {
		t.Fatalf("expected where T on bodyless impl, got %#v", impl.WhereClauses)
	}
}

// Bodyless inherent impl has no meaning.
func TestParseImplBlock_BodylessInherentImplRejected(t *testing.T) {
	src := `impl Money`
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for bodyless inherent impl, got nil")
	}
	if !strings.Contains(err.Error(), "expected `{` for an inherent impl block or `for Type`") {
		t.Errorf("expected a missing-`{` inherent impl rejection, got %q", err.Error())
	}
}
