package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"testing"
)

func TestParse_AttachesLeadingComment(t *testing.T) {
	src := "// a note\nx = 1\n"
	toks := lexer.Lex(src)
	nodes, err := Parse(toks)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	n, ok := nodes[0].(ast.HasTrivia)
	if !ok {
		t.Fatalf("node does not carry trivia")
	}
	leading := n.GetLeading()
	if len(leading) != 1 || leading[0].Text != "// a note" {
		t.Fatalf("leading = %v", leading)
	}
	if leading[0].Kind != ast.TriviaComment {
		t.Fatalf("want comment kind, got %v", leading[0].Kind)
	}
}

func TestParse_AttachesTrailingComment(t *testing.T) {
	src := "x = 1 // inline\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	n, ok := nodes[0].(ast.HasTrivia)
	if !ok {
		t.Fatalf("node does not carry trivia")
	}
	trailing := n.GetTrailing()
	if len(trailing) != 1 || trailing[0].Text != "// inline" {
		t.Fatalf("trailing = %v", trailing)
	}
}

func TestParse_AttachesBlankLineAsLeadingOnNextNode(t *testing.T) {
	src := "x = 1\n\ny = 2\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(nodes))
	}
	n, ok := nodes[1].(ast.HasTrivia)
	if !ok {
		t.Fatalf("node does not carry trivia")
	}
	leading := n.GetLeading()
	if len(leading) != 1 || leading[0].Kind != ast.TriviaBlankLine {
		t.Fatalf("leading = %v", leading)
	}
}

// Trivia inside blocks.
func TestParse_AttachesLeadingCommentInsideBlock(t *testing.T) {
	src := "fn f() {\n  // inner\n  x = 1\n  x\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("want FuncDef, got %T", nodes[0])
	}
	if fn.Body == nil || len(fn.Body.Stmts) == 0 {
		t.Fatalf("function body empty")
	}
	first, ok := fn.Body.Stmts[0].(ast.HasTrivia)
	if !ok {
		t.Fatalf("inner stmt does not carry trivia: %T", fn.Body.Stmts[0])
	}
	leading := first.GetLeading()
	found := false
	for _, tr := range leading {
		if tr.Kind == ast.TriviaComment && tr.Text == "// inner" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected leading comment '// inner', got %v", leading)
	}
}

// Trivia inside case-arm bodies.
func TestParse_AttachesLeadingCommentInsideCaseArms(t *testing.T) {
	src := "fn f() {\n  case 1 {\n    // first arm\n    1 -> 1\n    // second arm\n    _ -> 0\n  }\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("want FuncDef, got %T", nodes[0])
	}
	if fn.Body == nil || len(fn.Body.Stmts) == 0 {
		t.Fatalf("function body empty")
	}
	stmt, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("want ExprStmt, got %T", fn.Body.Stmts[0])
	}
	caseExpr, ok := stmt.Expr.(*ast.Case)
	if !ok {
		t.Fatalf("want *ast.Case, got %T", stmt.Expr)
	}
	if len(caseExpr.Branches) != 2 {
		t.Fatalf("want 2 branches, got %d", len(caseExpr.Branches))
	}
	// First arm should have "// first arm" as leading trivia on the branch itself.
	first := caseExpr.Branches[0].GetLeading()
	foundFirst := false
	for _, tr := range first {
		if tr.Kind == ast.TriviaComment && tr.Text == "// first arm" {
			foundFirst = true
		}
	}
	if !foundFirst {
		t.Errorf("branch 0 leading missing '// first arm': %v", first)
	}
	// Second arm should have "// second arm" as leading trivia.
	second := caseExpr.Branches[1].GetLeading()
	foundSecond := false
	for _, tr := range second {
		if tr.Kind == ast.TriviaComment && tr.Text == "// second arm" {
			foundSecond = true
		}
	}
	if !foundSecond {
		t.Errorf("branch 1 leading missing '// second arm': %v", second)
	}
}

// Fix 2 pinning test: mid-expression COMMENT tokens between infix operators
// are currently dropped. This pins current behavior; if a future change attaches
// them to one side, delete or update this test.
func TestParse_MidExpressionCommentIsDropped(t *testing.T) {
	// Known limitation: a trailing comment on a pipe-chain intermediate line
	// is dropped, not attached. This test pins the current behavior; if the
	// formatter grows attachment for this case, delete or update this test.
	src := "x // why we pipe\n  |> f\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	n := nodes[0].(ast.HasTrivia)
	// No trivia should reach the top-level node from the mid-expression comment
	if len(n.GetLeading()) != 0 {
		t.Errorf("unexpected leading trivia: %v", n.GetLeading())
	}
	// The trailing comment path also shouldn't pick this up because the
	// comment isn't on the same line as the node's end — it's mid-expr.
	// (May be empty or not depending on how LineNum works; just check
	// that we're documenting current behavior.)
}

func TestParse_StandaloneCommentBetweenPipeStages(t *testing.T) {
	src := "5\n|> Int.to_float()\n// a comment\n|> then |value| value == 5.0\n|> dbg\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	stmt, ok := nodes[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("want ExprStmt, got %T", nodes[0])
	}
	finalPipe, ok := stmt.Expr.(*ast.Binary)
	if !ok || finalPipe.Op != "|>" {
		t.Fatalf("want final pipe, got %T %#v", stmt.Expr, stmt.Expr)
	}
	middlePipe, ok := finalPipe.Left.(*ast.Binary)
	if !ok || middlePipe.Op != "|>" {
		t.Fatalf("want middle pipe, got %T %#v", finalPipe.Left, finalPipe.Left)
	}
	then, ok := middlePipe.Right.(*ast.Then)
	if !ok {
		t.Fatalf("want then pipe stage, got %T", middlePipe.Right)
	}
	leading := then.GetLeading()
	if len(leading) != 1 || leading[0].Text != "// a comment" {
		t.Fatalf("want leading comment on then stage, got %#v", leading)
	}
}

// Fix 3 pinning test: regular nonblank // comments interleaved with /// doc
// comments are not doc comments and should not disappear silently.
func TestParse_RegularCommentBetweenDocComments_Rejected(t *testing.T) {
	src := "/// first\n// regular\n/// second\nfn f() {\n  1\n}\n"
	_, err := Parse(lexer.Lex(src))
	if err == nil {
		t.Fatal("expected parse error for nonblank comment between doc comments")
	}
}

func TestParse_BlankCommentBetweenDocAndAttachedTest(t *testing.T) {
	src := "/// first\n//\n//! assert f() == 1\nfn f(): Int { 1 }\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	fn := nodes[0].(*ast.FuncDef)
	if fn.Doc != "first\n" {
		t.Fatalf("Doc = %q, want blank separator retained in doc boundary", fn.Doc)
	}
	if len(fn.AttachedTests) != 1 {
		t.Fatalf("attached tests = %d, want 1", len(fn.AttachedTests))
	}
}

func TestParse_BlankCommentBetweenDocAndDeclaration(t *testing.T) {
	src := "/// first\n//\nfn f(): Int { 1 }\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	fn := nodes[0].(*ast.FuncDef)
	if fn.Doc != "first\n" {
		t.Fatalf("Doc = %q, want blank separator retained in doc boundary", fn.Doc)
	}
}

// Fix 4: a bare comment line between statements attaches as leading trivia
// to the next statement.
func TestParse_BareCommentLineIsLeadingOnNextNode(t *testing.T) {
	// A standalone comment on its own line between two statements
	// should attach as LEADING to the next statement.
	src := "x = 1\n// note\ny = 2\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(nodes))
	}
	n := nodes[1].(ast.HasTrivia)
	leading := n.GetLeading()
	if len(leading) != 1 || leading[0].Text != "// note" {
		t.Fatalf("leading = %v", leading)
	}
}

// ParseFile preserves comments that sit after the last top-level
// declaration through EOF — Parse drops them (back-compat with
// non-formatter callers); the formatter uses ParseFile. Regression
// test for a data-loss bug where `nomi fmt -w` silently deleted
// trailing top-level comments.
func TestParseFile_PreservesTrailingTopLevelComments(t *testing.T) {
	src := "fn main() {}\n\n// trailing one\n// trailing two\n"
	nodes, fileEnd, err := ParseFile(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	// fileEnd: blank-line + "// trailing one" + "// trailing two" (3 entries).
	if len(fileEnd) != 3 {
		t.Fatalf("want 3 file-end trivia, got %d: %v", len(fileEnd), fileEnd)
	}
	if fileEnd[0].Kind != ast.TriviaBlankLine {
		t.Errorf("want first trivia to be BlankLine, got %v", fileEnd[0].Kind)
	}
	if fileEnd[1].Kind != ast.TriviaComment || fileEnd[1].Text != "// trailing one" {
		t.Errorf("want second = '// trailing one' comment, got %v", fileEnd[1])
	}
	if fileEnd[2].Kind != ast.TriviaComment || fileEnd[2].Text != "// trailing two" {
		t.Errorf("want third = '// trailing two' comment, got %v", fileEnd[2])
	}
}

// Parse (the back-compat entry point) drops end-of-file trivia, mirroring
// pre-fix behavior. This guards the documented contract — if Parse ever
// starts preserving it, the formatter would double-emit.
func TestParse_DropsTrailingTopLevelComments(t *testing.T) {
	src := "fn main() {}\n// trailing dropped\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	// Verify the trailing comment isn't smuggled onto the node.
	n := nodes[0].(ast.HasTrivia)
	for _, tr := range n.GetTrailing() {
		if tr.Kind == ast.TriviaComment && tr.Text == "// trailing dropped" {
			t.Errorf("trailing top-level comment unexpectedly attached to last node: %v", tr)
		}
	}
}

// parseStructBody captures trailing in-body comments on
// StructDef.EndTrivia rather than rejecting them with "expected field name",
// as file-end trivia is captured for a whole file.
func TestParse_StructBody_TrailingComment_Preserved(t *testing.T) {
	src := "pub struct Point {\n  x: Int\n  y: Int\n  // trailing comment\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("want *ast.StructDef, got %T", nodes[0])
	}
	if len(sd.EndTrivia) == 0 {
		t.Fatalf("want non-empty EndTrivia, got nil")
	}
	found := false
	for _, tr := range sd.EndTrivia {
		if tr.Kind == ast.TriviaComment && tr.Text == "// trailing comment" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected '// trailing comment' in EndTrivia, got %v", sd.EndTrivia)
	}
}

// parseEnumBody now captures trailing in-body comments on
// EnumDef.EndTrivia (previously rejected with "expected `|` between
// enum variants").
func TestParse_EnumBody_TrailingComment_Preserved(t *testing.T) {
	src := "pub enum Color {\n  Red\n  Green\n  Blue\n  // trailing comment\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	ed, ok := nodes[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("want *ast.EnumDef, got %T", nodes[0])
	}
	if len(ed.EndTrivia) == 0 {
		t.Fatalf("want non-empty EndTrivia, got nil")
	}
	found := false
	for _, tr := range ed.EndTrivia {
		if tr.Kind == ast.TriviaComment && tr.Text == "// trailing comment" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected '// trailing comment' in EndTrivia, got %v", ed.EndTrivia)
	}
}

// parseEnumVariant (struct-shape) now captures trailing in-body
// comments on EnumVariant.EndTrivia (previously rejected with
// "expected field name in struct variant").
func TestParse_StructShapedVariant_TrailingComment_Preserved(t *testing.T) {
	src := "pub enum Shape {\n  Circle Float\n  Rect {\n    width: Float,\n    height: Float,\n    // trailing comment\n  }\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	ed, ok := nodes[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("want *ast.EnumDef, got %T", nodes[0])
	}
	if len(ed.Variants) != 2 {
		t.Fatalf("want 2 variants, got %d", len(ed.Variants))
	}
	rect := ed.Variants[1]
	if rect.Kind != "struct" {
		t.Fatalf("variant 1 should be struct-shaped, got kind=%q", rect.Kind)
	}
	if len(rect.EndTrivia) == 0 {
		t.Fatalf("want non-empty EndTrivia on struct-shaped variant, got nil")
	}
	found := false
	for _, tr := range rect.EndTrivia {
		if tr.Kind == ast.TriviaComment && tr.Text == "// trailing comment" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected '// trailing comment' in EndTrivia, got %v", rect.EndTrivia)
	}
}

// parseInterfaceDef previously swallowed trailing in-body comments via
// skipNewlines (silent data loss in fmt). Now captured on
// InterfaceDef.EndTrivia.
func TestParse_InterfaceBody_TrailingComment_Preserved(t *testing.T) {
	src := "pub interface Speech {\n  fn speak(s: Int): Int\n  // trailing comment\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	id, ok := nodes[0].(*ast.InterfaceDef)
	if !ok {
		t.Fatalf("want *ast.InterfaceDef, got %T", nodes[0])
	}
	if len(id.EndTrivia) == 0 {
		t.Fatalf("want non-empty EndTrivia, got nil")
	}
	found := false
	for _, tr := range id.EndTrivia {
		if tr.Kind == ast.TriviaComment && tr.Text == "// trailing comment" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected '// trailing comment' in EndTrivia, got %v", id.EndTrivia)
	}
}

// parseAnonStructType previously consumed trailing comments via its
// separator loop (silent data loss in fmt). Now captured on
// AnonStructType.EndTrivia.
func TestParse_AnonStructType_TrailingComment_Preserved(t *testing.T) {
	src := "fn greet(p: {\n  name: String,\n  age: Int,\n  // trailing comment\n}): String {\n  p.name\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("want *ast.FuncDef, got %T", nodes[0])
	}
	if len(fn.Params) != 1 {
		t.Fatalf("want 1 param, got %d", len(fn.Params))
	}
	ast_, ok := fn.Params[0].TypeAnnotation.(*ast.AnonStructType)
	if !ok {
		t.Fatalf("want *ast.AnonStructType, got %T", fn.Params[0].TypeAnnotation)
	}
	if len(ast_.EndTrivia) == 0 {
		t.Fatalf("want non-empty EndTrivia, got nil")
	}
	found := false
	for _, tr := range ast_.EndTrivia {
		if tr.Kind == ast.TriviaComment && tr.Text == "// trailing comment" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected '// trailing comment' in EndTrivia, got %v", ast_.EndTrivia)
	}
}

// parseStructLit (nominal) previously consumed trailing in-body
// comments via skipNewlines (silent data loss in fmt). Now captured on
// StructLit.EndTrivia.
func TestParse_StructLit_TrailingComment_Preserved(t *testing.T) {
	src := "fn main() {\n  p = Point{\n    x: 3,\n    y: 4,\n    // trailing comment\n  }\n  p\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("want *ast.FuncDef, got %T", nodes[0])
	}
	bind, ok := fn.Body.Stmts[0].(*ast.Binding)
	if !ok {
		t.Fatalf("want *ast.Binding, got %T", fn.Body.Stmts[0])
	}
	lit, ok := bind.Value.(*ast.StructLit)
	if !ok {
		t.Fatalf("want *ast.StructLit, got %T", bind.Value)
	}
	if len(lit.EndTrivia) == 0 {
		t.Fatalf("want non-empty EndTrivia, got nil")
	}
	found := false
	for _, tr := range lit.EndTrivia {
		if tr.Kind == ast.TriviaComment && tr.Text == "// trailing comment" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected '// trailing comment' in EndTrivia, got %v", lit.EndTrivia)
	}
}

// parseAnonStructLit previously rejected trailing in-body comments
// with "expected field name in struct literal". Now captured on the
// (shared) StructLit.EndTrivia field.
func TestParse_AnonStructLit_TrailingComment_Preserved(t *testing.T) {
	src := "fn main() {\n  p = {\n    name: \"alice\",\n    age: 30,\n    // trailing comment\n  }\n  p\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("want *ast.FuncDef, got %T", nodes[0])
	}
	bind, ok := fn.Body.Stmts[0].(*ast.Binding)
	if !ok {
		t.Fatalf("want *ast.Binding, got %T", fn.Body.Stmts[0])
	}
	lit, ok := bind.Value.(*ast.StructLit)
	if !ok {
		t.Fatalf("want *ast.StructLit, got %T", bind.Value)
	}
	if lit.TypeName != nil {
		t.Fatalf("want anonymous StructLit (TypeName==nil), got %v", lit.TypeName)
	}
	if len(lit.EndTrivia) == 0 {
		t.Fatalf("want non-empty EndTrivia, got nil")
	}
	found := false
	for _, tr := range lit.EndTrivia {
		if tr.Kind == ast.TriviaComment && tr.Text == "// trailing comment" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected '// trailing comment' in EndTrivia, got %v", lit.EndTrivia)
	}
}

// parseMapLitEntries previously errored at the COMMENT token between
// the last entry and the closing `}` ("expected `}` to close map
// literal"). Now captured on MapLit.EndTrivia.
func TestParse_MapLit_TrailingComment_Preserved(t *testing.T) {
	src := "fn main() {\n  m = {\n    \"a\" => 1,\n    // trailing comment\n  }\n  m\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("want *ast.FuncDef, got %T", nodes[0])
	}
	bind, ok := fn.Body.Stmts[0].(*ast.Binding)
	if !ok {
		t.Fatalf("want *ast.Binding, got %T", fn.Body.Stmts[0])
	}
	lit, ok := bind.Value.(*ast.MapLit)
	if !ok {
		t.Fatalf("want *ast.MapLit, got %T", bind.Value)
	}
	if len(lit.EndTrivia) == 0 {
		t.Fatalf("want non-empty EndTrivia, got nil")
	}
	found := false
	for _, tr := range lit.EndTrivia {
		if tr.Kind == ast.TriviaComment && tr.Text == "// trailing comment" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected '// trailing comment' in EndTrivia, got %v", lit.EndTrivia)
	}
}

// parseListLit previously silently dropped trailing in-body comments
// via its post-element skipNewlines (data loss in fmt). Now captured on
// ListLit.EndTrivia.
func TestParse_ListLit_TrailingComment_Preserved(t *testing.T) {
	src := "fn main() {\n  l = [\n    1,\n    2,\n    // trailing comment\n  ]\n  l\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("want *ast.FuncDef, got %T", nodes[0])
	}
	bind, ok := fn.Body.Stmts[0].(*ast.Binding)
	if !ok {
		t.Fatalf("want *ast.Binding, got %T", fn.Body.Stmts[0])
	}
	lit, ok := bind.Value.(*ast.ListLit)
	if !ok {
		t.Fatalf("want *ast.ListLit, got %T", bind.Value)
	}
	if len(lit.EndTrivia) == 0 {
		t.Fatalf("want non-empty EndTrivia, got nil")
	}
	found := false
	for _, tr := range lit.EndTrivia {
		if tr.Kind == ast.TriviaComment && tr.Text == "// trailing comment" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected '// trailing comment' in EndTrivia, got %v", lit.EndTrivia)
	}
}

// parseMapPatternEntries previously errored on a trailing comment
// inside a case-branch map pattern body. Now captured on
// MapPattern.EndTrivia.
func TestParse_MapPattern_TrailingComment_Preserved(t *testing.T) {
	src := "fn main() {\n  m = {\"a\" => 1}\n  case m {\n    {\n      \"a\" => x,\n      // trailing comment\n    } -> x\n    _ -> 0\n  }\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("want *ast.FuncDef, got %T", nodes[0])
	}
	// case expression is the second stmt's value
	es, ok := fn.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("want *ast.ExprStmt for case, got %T", fn.Body.Stmts[1])
	}
	mt, ok := es.Expr.(*ast.Case)
	if !ok {
		t.Fatalf("want *ast.Case, got %T", es.Expr)
	}
	if len(mt.Branches) < 1 {
		t.Fatalf("want at least one branch, got %d", len(mt.Branches))
	}
	mp, ok := mt.Branches[0].Pattern.(*ast.MapPattern)
	if !ok {
		t.Fatalf("want *ast.MapPattern, got %T", mt.Branches[0].Pattern)
	}
	if len(mp.EndTrivia) == 0 {
		t.Fatalf("want non-empty EndTrivia on map pattern, got nil")
	}
	found := false
	for _, tr := range mp.EndTrivia {
		if tr.Kind == ast.TriviaComment && tr.Text == "// trailing comment" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected '// trailing comment' in EndTrivia, got %v", mp.EndTrivia)
	}
}

// parseListPatternBody previously silently dropped trailing in-body
// comments via skipNewlines. Now captured on ListPattern.EndTrivia.
func TestParse_ListPattern_TrailingComment_Preserved(t *testing.T) {
	src := "fn main() {\n  l = [1, 2]\n  case l {\n    [\n      a,\n      b,\n      // trailing comment\n    ] -> a + b\n    _ -> 0\n  }\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("want *ast.FuncDef, got %T", nodes[0])
	}
	es, ok := fn.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("want *ast.ExprStmt for case, got %T", fn.Body.Stmts[1])
	}
	mt, ok := es.Expr.(*ast.Case)
	if !ok {
		t.Fatalf("want *ast.Case, got %T", es.Expr)
	}
	if len(mt.Branches) < 1 {
		t.Fatalf("want at least one branch, got %d", len(mt.Branches))
	}
	lp, ok := mt.Branches[0].Pattern.(*ast.ListPattern)
	if !ok {
		t.Fatalf("want *ast.ListPattern, got %T", mt.Branches[0].Pattern)
	}
	if len(lp.EndTrivia) == 0 {
		t.Fatalf("want non-empty EndTrivia on list pattern, got nil")
	}
	found := false
	for _, tr := range lp.EndTrivia {
		if tr.Kind == ast.TriviaComment && tr.Text == "// trailing comment" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected '// trailing comment' in EndTrivia, got %v", lp.EndTrivia)
	}
}

// parseStructBody now captures inter-field comments on
// StructField.LeadingComments (previously rejected with "expected
// field name in struct definition").
func TestParse_StructBody_InterFieldComment_Preserved(t *testing.T) {
	src := "pub struct Point {\n  x: Int\n  // a comment between two fields\n  y: Int\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("want *ast.StructDef, got %T", nodes[0])
	}
	if len(sd.Fields) != 2 {
		t.Fatalf("want 2 fields, got %d", len(sd.Fields))
	}
	if len(sd.Fields[1].LeadingComments) == 0 {
		t.Fatalf("want non-empty LeadingComments on field 1, got nil")
	}
	found := false
	for _, tr := range sd.Fields[1].LeadingComments {
		if tr.Kind == ast.TriviaComment && tr.Text == "// a comment between two fields" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected inter-field comment in LeadingComments, got %v", sd.Fields[1].LeadingComments)
	}
}

// parseEnumBody now captures inter-variant comments on
// EnumVariant.LeadingComments (previously rejected with "expected `|`
// between enum variants").
func TestParse_EnumBody_InterVariantComment_Preserved(t *testing.T) {
	src := "pub enum Color {\n  Red\n  // a comment between two variants\n  Green\n  Blue\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	ed, ok := nodes[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("want *ast.EnumDef, got %T", nodes[0])
	}
	if len(ed.Variants) != 3 {
		t.Fatalf("want 3 variants, got %d", len(ed.Variants))
	}
	// The comment sits before the `|` of variant 1 (Green) — so it
	// attaches to variant 1's LeadingComments.
	if len(ed.Variants[1].LeadingComments) == 0 {
		t.Fatalf("want non-empty LeadingComments on variant 1, got nil")
	}
	found := false
	for _, tr := range ed.Variants[1].LeadingComments {
		if tr.Kind == ast.TriviaComment && tr.Text == "// a comment between two variants" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected inter-variant comment, got %v", ed.Variants[1].LeadingComments)
	}
}

// parseInterfaceDef now captures inter-member comments on the next
// member's LeadingComments (InterfaceField.LeadingComments or
// InterfaceMethod.GetLeading via its TriviaCarrier). Previously
// dropped via skipNewlines.
func TestParse_InterfaceBody_InterMemberComment_Preserved(t *testing.T) {
	src := "pub interface Speech {\n  fn speak(s: Int): Int\n  // a comment between two methods\n  fn shout(s: Int): Int\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	id, ok := nodes[0].(*ast.InterfaceDef)
	if !ok {
		t.Fatalf("want *ast.InterfaceDef, got %T", nodes[0])
	}
	if len(id.Methods) != 2 {
		t.Fatalf("want 2 methods, got %d", len(id.Methods))
	}
	// Inter-member comment attaches to method 1 (shout) via its
	// TriviaCarrier.Leading slot.
	leading := id.Methods[1].GetLeading()
	if len(leading) == 0 {
		t.Fatalf("want non-empty Leading on method 1, got nil")
	}
	found := false
	for _, tr := range leading {
		if tr.Kind == ast.TriviaComment && tr.Text == "// a comment between two methods" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected inter-method comment, got %v", leading)
	}
}

// parseInterfaceDef captures inter-field comments between `field`
// requirements on InterfaceField.LeadingComments.
func TestParse_InterfaceBody_InterFieldComment_Preserved(t *testing.T) {
	src := "pub interface Boxed {\n  field width: Int\n  // a comment between two fields\n  field height: Int\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	id, ok := nodes[0].(*ast.InterfaceDef)
	if !ok {
		t.Fatalf("want *ast.InterfaceDef, got %T", nodes[0])
	}
	if len(id.Fields) != 2 {
		t.Fatalf("want 2 fields, got %d", len(id.Fields))
	}
	if len(id.Fields[1].LeadingComments) == 0 {
		t.Fatalf("want non-empty LeadingComments on field 1, got nil")
	}
	found := false
	for _, tr := range id.Fields[1].LeadingComments {
		if tr.Kind == ast.TriviaComment && tr.Text == "// a comment between two fields" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected inter-field comment, got %v", id.Fields[1].LeadingComments)
	}
}

// parseAnonStructType now captures inter-field comments on
// StructField.LeadingComments inside the anon-struct-type body.
func TestParse_AnonStructType_InterFieldComment_Preserved(t *testing.T) {
	src := "fn greet(p: {\n  name: String,\n  // a comment between two anon-type fields\n  age: Int,\n}): String {\n  p.name\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("want *ast.FuncDef, got %T", nodes[0])
	}
	at, ok := fn.Params[0].TypeAnnotation.(*ast.AnonStructType)
	if !ok {
		t.Fatalf("want *ast.AnonStructType, got %T", fn.Params[0].TypeAnnotation)
	}
	if len(at.Fields) != 2 {
		t.Fatalf("want 2 fields, got %d", len(at.Fields))
	}
	if len(at.Fields[1].LeadingComments) == 0 {
		t.Fatalf("want non-empty LeadingComments on field 1, got nil")
	}
	found := false
	for _, tr := range at.Fields[1].LeadingComments {
		if tr.Kind == ast.TriviaComment && tr.Text == "// a comment between two anon-type fields" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected inter-field comment, got %v", at.Fields[1].LeadingComments)
	}
}

// parseStructLit captures inter-field comments on
// StructFieldVal.LeadingComments — previously rejected with "expected
// field name in struct literal".
func TestParse_StructLit_InterFieldComment_Preserved(t *testing.T) {
	src := "fn main() {\n  p = Point{\n    x: 3,\n    // a comment between two literal fields\n    y: 4,\n  }\n  p\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("want *ast.FuncDef, got %T", nodes[0])
	}
	bind, ok := fn.Body.Stmts[0].(*ast.Binding)
	if !ok {
		t.Fatalf("want *ast.Binding, got %T", fn.Body.Stmts[0])
	}
	lit, ok := bind.Value.(*ast.StructLit)
	if !ok {
		t.Fatalf("want *ast.StructLit, got %T", bind.Value)
	}
	if len(lit.Fields) != 2 {
		t.Fatalf("want 2 fields, got %d", len(lit.Fields))
	}
	if len(lit.Fields[1].LeadingComments) == 0 {
		t.Fatalf("want non-empty LeadingComments on field 1, got nil")
	}
	found := false
	for _, tr := range lit.Fields[1].LeadingComments {
		if tr.Kind == ast.TriviaComment && tr.Text == "// a comment between two literal fields" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected inter-field comment, got %v", lit.Fields[1].LeadingComments)
	}
}

// parseEnumVariant's struct-payload arm also captures inter-field
// comments on StructField.LeadingComments — previously rejected with
// "expected field name in struct variant".
func TestParse_StructShapedVariant_InterFieldComment_Preserved(t *testing.T) {
	src := "pub enum Shape {\n  Rect {\n    width: Float,\n    // a comment between fields\n    height: Float,\n  }\n}\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	ed, ok := nodes[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("want *ast.EnumDef, got %T", nodes[0])
	}
	if len(ed.Variants) != 1 {
		t.Fatalf("want 1 variant, got %d", len(ed.Variants))
	}
	rect := ed.Variants[0]
	if rect.Kind != "struct" {
		t.Fatalf("variant should be struct-shaped, got kind=%q", rect.Kind)
	}
	if len(rect.Fields) != 2 {
		t.Fatalf("want 2 fields, got %d", len(rect.Fields))
	}
	if len(rect.Fields[1].LeadingComments) == 0 {
		t.Fatalf("want non-empty LeadingComments on field 1, got nil")
	}
	found := false
	for _, tr := range rect.Fields[1].LeadingComments {
		if tr.Kind == ast.TriviaComment && tr.Text == "// a comment between fields" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected inter-field comment, got %v", rect.Fields[1].LeadingComments)
	}
}

// Top-level trailing trivia with a following blank line works.
func TestParse_TrailingCommentThenBlankLine(t *testing.T) {
	src := "x = 1 // inline\n\ny = 2\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(nodes))
	}
	n0 := nodes[0].(ast.HasTrivia)
	trailing := n0.GetTrailing()
	if len(trailing) != 1 || trailing[0].Text != "// inline" {
		t.Fatalf("trailing = %v", trailing)
	}
	n1 := nodes[1].(ast.HasTrivia)
	leading := n1.GetLeading()
	if len(leading) != 1 || leading[0].Kind != ast.TriviaBlankLine {
		t.Fatalf("leading = %v", leading)
	}
}
