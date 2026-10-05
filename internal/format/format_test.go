package format

import (
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormat_EmptyProgram(t *testing.T) {
	got, err := Format("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFormat_JustNewlines(t *testing.T) {
	got, _ := Format("\n\n\n")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFormat_ParseErrorReturnsOriginal(t *testing.T) {
	src := "fn ("
	got, err := Format(src)
	if err == nil {
		t.Fatal("expected error")
	}
	if got != src {
		t.Errorf("got %q, want original %q", got, src)
	}
}

// A top-of-file header comment above the first import stays on top even when
// sorting reorders the imports beneath it (here std/int sorts before std/io,
// moving the originally-first std/io down). The header is positional — it
// belongs to the head of the group, not to whichever import happened to be
// first in source order.
func TestFormat_SeparateImports_LeadingCommentStaysOnTop(t *testing.T) {
	src := "// header line one\n" +
		"// header line two\n" +
		"import std/io\n" +
		"import std/int: Int\n" +
		"\n" +
		"fn main() {\n" +
		"    io.print(Int.to_string(42))\n" +
		"}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	want := "// header line one\n// header line two\nimport {\n    std/int.Int\n    std/io\n}\n"
	if !strings.Contains(got, want) {
		t.Errorf("expected header to stay above the combined+sorted block, got:\n%s", got)
	}
}

func TestFormat_IntLiteral(t *testing.T) {
	got, _ := Format("42\n")
	if got != "42\n" {
		t.Errorf("got %q, want %q", got, "42\n")
	}
}

func TestFormat_FloatLiteral(t *testing.T) {
	got, _ := Format("3.14\n")
	if got != "3.14\n" {
		t.Errorf("got %q, want %q", got, "3.14\n")
	}
}

// Numeric literals round-trip with the original source form intact: digit
// separators, hex/binary/octal radix, and scientific notation all survive
// because the formatter emits the parser-captured lexeme directly.
func TestFormat_NumericLiteralPreservation(t *testing.T) {
	cases := []string{
		"1_000_000",
		"1_000_000_000",
		"1_000.123_456",
		"0xFF",
		"0xDEAD_BEEF",
		"0b1010",
		"0b1010_0101",
		"0o77",
		"1.0e10",
		"2.5e-3",
		"42",
		"3.14",
		"1000000000", // bare form also preserved — no auto-grouping
	}
	for _, lit := range cases {
		src := lit + "\n"
		got, err := Format(src)
		if err != nil {
			t.Errorf("Format(%q) error: %v", src, err)
			continue
		}
		if got != src {
			t.Errorf("Format(%q) = %q, want %q", src, got, src)
		}
	}
}

func TestFormat_NegativeNumericPatternPreservation(t *testing.T) {
	// Negation in a struct-field pattern is a separate parser path
	// (the MINUS is consumed before the literal token); make sure the
	// stored lexeme keeps the sign so the formatter can round-trip it.
	src := "case p { Point{x: -1_000, y: -3.5} -> 1, _ -> 0 }\n"
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "-1_000") {
		t.Errorf("expected -1_000 preserved, got %q", got)
	}
	if !strings.Contains(got, "-3.5") {
		t.Errorf("expected -3.5 preserved, got %q", got)
	}
}

func TestFormat_StringLiteral(t *testing.T) {
	got, _ := Format(`"hello"` + "\n")
	if got != `"hello"`+"\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Identifier(t *testing.T) {
	got, _ := Format("x\n")
	if got != "x\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_BooleanTrue(t *testing.T) {
	got, _ := Format("True\n")
	if got != "True\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_BooleanFalse(t *testing.T) {
	got, _ := Format("False\n")
	if got != "False\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Binding(t *testing.T) {
	got, _ := Format("x=1\n")
	if got != "x = 1\n" {
		t.Errorf("got %q, want %q", got, "x = 1\n")
	}
}

func TestFormat_Binding_WithString(t *testing.T) {
	got, _ := Format(`name = "Alice"` + "\n")
	if got != `name = "Alice"`+"\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Binding_WithTypeAnnotation(t *testing.T) {
	src := "x: Int = 5\n"
	got, _ := Format(src)
	want := "x: Int = 5\n"
	if got != want {
		t.Errorf("\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_Binding_WithGenericTypeAnnotation(t *testing.T) {
	src := "m: Map<String, Int> = Map.empty()\n"
	got, _ := Format(src)
	want := "m: Map<String, Int> = Map.empty()\n"
	if got != want {
		t.Errorf("\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_Block_SingleExprFitsOnOneLine(t *testing.T) {
	// A block whose single expression is short enough stays on one line.
	src := "{ 1 }\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q, want %q", got, src)
	}
}

func TestFormat_Block_MultiStmtBreaks(t *testing.T) {
	// A block with multiple statements formats with one stmt per line,
	// { and } on their own lines, body indented by 2.
	src := "{\n    x = 1\n    x\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Block_MultilineAssertionsAreBlankSeparated(t *testing.T) {
	src := `test "durations support arithmetic" {
    assert duration.add(
        Duration.seconds(1),
        Duration.milliseconds(500),
        Duration.microseconds(250),
    ) == Duration.microseconds(1500250)
    assert duration.subtract(
        Duration.seconds(1),
        Duration.seconds(3),
        Duration.milliseconds(250),
    ) == Duration.milliseconds(-2250)
    assert duration.multiply(Duration.seconds(2), 3) == Duration.seconds(6)
}
`
	want := `test "durations support arithmetic" {
    assert duration.add(
        Duration.seconds(1),
        Duration.milliseconds(500),
        Duration.microseconds(250),
    ) == Duration.microseconds(1500250)

    assert duration.subtract(
        Duration.seconds(1),
        Duration.seconds(3),
        Duration.milliseconds(250),
    ) == Duration.milliseconds(-2250)

    assert duration.multiply(Duration.seconds(2), 3) == Duration.seconds(6)
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_Block_NestedImportsSeparatedFromBody(t *testing.T) {
	src := `fn first(): Maybe<Int> {
    import std/maybe: Maybe as Local
    import std/results: Result as Outcome
    Outcome.Ok(Local.Some(1))
}
`
	want := `fn first(): Maybe<Int> {
    import {
        std/maybe.Maybe as Local
        std/results.Result as Outcome
    }

    Outcome.Ok(Local.Some(1))
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_Block_NestedImportsMoveToTop(t *testing.T) {
	src := `fn first(): Maybe<Int> {
    x = 1
    import std/maybe: Maybe as Local
    Local.Some(x)
}
`
	want := `fn first(): Maybe<Int> {
    import std/maybe.Maybe as Local

    x = 1
    Local.Some(x)
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_BinaryArith(t *testing.T) {
	got, _ := Format("1+2\n")
	if got != "1 + 2\n" {
		t.Errorf("got %q, want %q", got, "1 + 2\n")
	}
}

func TestFormat_BinaryCompare(t *testing.T) {
	got, _ := Format("x==y\n")
	if got != "x == y\n" {
		t.Errorf("got %q, want %q", got, "x == y\n")
	}
}

func TestFormat_BinaryPrecedence_NoSpuriousParens(t *testing.T) {
	// Parens are elided by the parser (no Paren AST node), so the formatter
	// relies on the tree's own shape to express precedence. This test
	// confirms the formatter doesn't insert spurious parens for an
	// expression whose natural precedence already matches the tree shape.
	// `1 + 2 * 3` parses as Binary(+, 1, Binary(*, 2, 3)) — flat output
	// `1 + 2 * 3` is correct without parens.
	got, _ := Format("1 + 2 * 3\n")
	if got != "1 + 2 * 3\n" {
		t.Errorf("got %q, want %q", got, "1 + 2 * 3\n")
	}
}

func TestFormat_UnaryNeg(t *testing.T) {
	got, _ := Format("-x\n")
	if got != "-x\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_UnaryNot(t *testing.T) {
	got, _ := Format("!x\n")
	if got != "!x\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_UnaryParensBinary(t *testing.T) {
	// Regression for the bug that motivated this fix: unary applied to
	// a parenthesized binary must preserve the parens. Without synthesized
	// parens, `-(3 + 4)` would format as `-3 + 4` — a different program.
	got, err := Format("-(3 + 4)\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != "-(3 + 4)\n" {
		t.Errorf("got %q, want %q", got, "-(3 + 4)\n")
	}
}

func TestFormat_Precedence_LeftAssocSamePrecedence_NoParens(t *testing.T) {
	// (a - b) - c — left child at same precedence — no parens needed
	// because all Nomi binary operators are left-associative.
	got, _ := Format("1 - 2 - 3\n")
	if got != "1 - 2 - 3\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Precedence_RightAssocShape_AddsParens(t *testing.T) {
	// a - (b - c) — right child at same precedence — parens are needed
	// to preserve the tree shape (left-assoc would otherwise re-parse as
	// `(a - b) - c`).
	got, _ := Format("1 - (2 - 3)\n")
	if got != "1 - (2 - 3)\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Precedence_LowerChildOnLeft_AddsParens(t *testing.T) {
	// (1 + 2) * 3 — left child has lower precedence — parens needed
	// to preserve the tree shape.
	got, _ := Format("(1 + 2) * 3\n")
	if got != "(1 + 2) * 3\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Precedence_HigherChildOnLeft_NoParens(t *testing.T) {
	// 1 * 2 + 3 — left child has higher precedence — the natural tree
	// shape already matches the flat text; no parens synthesized.
	got, _ := Format("1 * 2 + 3\n")
	if got != "1 * 2 + 3\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_NegativeLiteral_NoParens(t *testing.T) {
	// Unary on a bare identifier doesn't need parens.
	got, _ := Format("-x\n")
	if got != "-x\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Pipe_AuthoredInline_StaysInline(t *testing.T) {
	// A pipe written on one line stays on one line (author-preserving, like
	// mix format) — for any stage count, as long as it fits the width.
	for _, src := range []string{
		"x |> f\n",
		"x |> f |> g |> h\n",
	} {
		got, _ := Format(src)
		if got != src {
			t.Errorf("Format(%q) = %q, want unchanged", src, got)
		}
	}
}

func TestFormat_Pipe_AuthoredMultiline_StaysStacked(t *testing.T) {
	// A pipe written across multiple lines stays stacked, even when it would
	// fit on one line (mix format never collapses a multi-line pipe). Applies
	// to single pipes too; the `+2` legacy indent is re-aligned to the source.
	cases := []struct{ src, want string }{
		{"x\n|> f\n", "x\n|> f\n"},                               // multi-line single → preserved
		{"x\n  |> f\n", "x\n|> f\n"},                             // old +2 single → re-aligned
		{"x\n|> f\n|> g\n|> h\n", "x\n|> f\n|> g\n|> h\n"},       // multi-line chain → preserved
		{"x\n  |> f\n  |> g\n  |> h\n", "x\n|> f\n|> g\n|> h\n"}, // old +2 chain → re-aligned
	}
	for _, c := range cases {
		got, _ := Format(c.src)
		if got != c.want {
			t.Errorf("Format(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}

func TestFormat_PipeDbgPrefixCanonicalizesToStage(t *testing.T) {
	src := `fn double(n: Int): Int {
    n * 2
}

fn main(): Int {
    5
    |> dbg double()
}
`
	want := `fn double(n: Int): Int {
    n * 2
}

fn main(): Int {
    5
    |> double()
    |> dbg
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_PipeTryPrefixStaysPrefix(t *testing.T) {
	src := `fn main(m: Maybe<Int>): Result<Int, String> {
    m
    |> try to_result()
    |> Ok()
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

func TestFormat_DbgSubexpressionsStayInsideStage(t *testing.T) {
	src := `fn main(): Int {
    5
    |> then |n| add(dbg f(n), dbg g(n))
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

func TestFormat_Pipe_SingleBreaksWhenTooWide(t *testing.T) {
	// A single `|>` that overflows the 100-column budget breaks onto two lines,
	// the `|>` aligned with the source.
	src := "averyLongPipelineSourceVariableNameThatIsQuiteLong |> someTransformationFunctionWithAnEvenLongerName()\n"
	got, _ := Format(src)
	want := "averyLongPipelineSourceVariableNameThatIsQuiteLong\n|> someTransformationFunctionWithAnEvenLongerName()\n"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_Pipe_BindingRHS_MultilinePipe_BreaksAfterEquals(t *testing.T) {
	// A multi-line pipe RHS breaks after `=`, the pipe block indented one level with
	// source + |> aligned.
	src := "result = [1, 2, 3]\n|> Iter.to_set()\n|> Set.size()\n"
	want := "result =\n    [1, 2, 3]\n    |> Iter.to_set()\n    |> Set.size()\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_Pipe_BindingRHS_TryHeadKeepsPipeAligned(t *testing.T) {
	src := `value = try source
|> parse()
|> normalize()
`
	want := `value =
    try source
    |> parse()
    |> normalize()
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_Assertion_PipeOperandIndentsContinuations(t *testing.T) {
	src := `test "chunking" {
    assert []
    |> Iter.chunks(2)
    |> Iter.to_list()
    |> List.equal?([])

    refute []
    |> Iter.chunks(2)
    |> Iter.to_list()
    |> Iter.not_empty?()
}
`
	want := `test "chunking" {
    assert []
        |> Iter.chunks(2)
        |> Iter.to_list()
        |> List.equal?([])

    refute []
        |> Iter.chunks(2)
        |> Iter.to_list()
        |> Iter.not_empty?()
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_Pipe_BindingRHS_SeparatesFollowingStatement(t *testing.T) {
	src := `fn main() {
    name =
        1
        |> find_name()
        |> assert
    assert name == "Ada"
}
`
	want := `fn main() {
    name =
        assert 1
            |> find_name()

    assert name == "Ada"
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_Pipe_BindingRHS_InlinePipe_StaysInline(t *testing.T) {
	// An inline pipe RHS that fits stays on the `=` line (author-preserving),
	// even with multiple stages — no break after `=`.
	src := "result = [1, 2, 3] |> Iter.to_set() |> Set.size()\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q, want unchanged", got)
	}
}

func TestFormat_Pipe_BindingRHS_SinglePipeStaysInline(t *testing.T) {
	// A single-pipe RHS that fits stays on the `=` line — no break.
	src := "x = source |> transform()\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Pipe_CallArgPipeStacks(t *testing.T) {
	src := `test "render" {
    assert Token.Bag([("a", 1)] |> Iter.to_map())
        |> Debug.inspect()
        |> String.equal?("Bag")
}
`
	want := `test "render" {
    assert Token.Bag(
        [("a", 1)]
        |> Iter.to_map()
    )
        |> Debug.inspect()
        |> String.equal?("Bag")
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_Binding_NonPipeMultilineRHS_NoBreakAfterEquals(t *testing.T) {
	// Only pipe RHS breaks after `=`. A non-pipe multi-line RHS (an if/else
	// past the 50-col cap) keeps `name = if … {` — unchanged.
	src := "x = if cond { branch_value_aaaaaaaaaaaaaaaaaaaaaaaaaaaaa } else { other_value }\n"
	got, _ := Format(src)
	if !strings.HasPrefix(got, "x = if cond {") {
		t.Errorf("expected `x = if cond {` (no break after =), got:\n%s", got)
	}
}

func TestFormat_IfPatternCondition(t *testing.T) {
	src := "result = if Some(name)=maybe_name{name}else{\"none\"}\n"
	want := "result = if Some(name) = maybe_name {\n    name\n} else {\n    \"none\"\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_Lambda_Simple(t *testing.T) {
	got, _ := Format("f = { x -> x + 1 }\n")
	if got != "f = { x -> x + 1 }\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Lambda_TwoParams(t *testing.T) {
	got, _ := Format("f = { x, y -> x + y }\n")
	if got != "f = { x, y -> x + y }\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Lambda_ImplicitIt(t *testing.T) {
	// Implicit `it` param — no `->` prefix. The parser recognizes this form
	// only when the block body actually references `it`; the formatter must
	// preserve that shape (it synthesizes a Param{Name:"it"} whose source
	// position matches the lambda's own opening brace).
	got, _ := Format("f = { it + 1 }\n")
	if got != "f = { it + 1 }\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Lambda_ParamWithDefault(t *testing.T) {
	// Default-value param (used in Iter.reduce).
	got, _ := Format("f = { acc = 0, x -> acc + x }\n")
	if got != "f = { acc = 0, x -> acc + x }\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Lambda_TypedParam(t *testing.T) {
	// Type annotations on lambda params must survive round-trip — dropping
	// them changes semantics now that the checker rejects unannotated
	// local-lambda bindings.
	got, _ := Format("f = { x: Int -> x + 1 }\n")
	if got != "f = { x: Int -> x + 1 }\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Lambda_TypedParamWithDefault(t *testing.T) {
	got, _ := Format("f = { x: Int = 5 -> x + 1 }\n")
	if got != "f = { x: Int = 5 -> x + 1 }\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Lambda_DestructureWithDefault(t *testing.T) {
	// `(a, b) = (0, 1)` — destructure pattern carrying a default value.
	// The default must round-trip; previously emitLambdaParam returned
	// early on the destructure branch and dropped it.
	src := "f = |(a, b) = (0, 1)| a + b\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Call_Simple(t *testing.T) {
	got, _ := Format("f(1, 2)\n")
	if got != "f(1, 2)\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Call_NoArgs(t *testing.T) {
	got, _ := Format("f()\n")
	if got != "f()\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Call_FieldAccessCallee(t *testing.T) {
	// The callee is a FieldAccess expression (Iter.count). Emit is
	// uniform whether the callee is an Ident or FieldAccess.
	got, _ := Format("Iter.count(list)\n")
	if got != "Iter.count(list)\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Call_NamedArg(t *testing.T) {
	// Named args surface as *ast.NamedArg wrappers inside Call.Args.
	src := `connect("host", timeout: 10)` + "\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Call_LongArgs_Breaks(t *testing.T) {
	// When flat form exceeds defaultWidth, the Group breaks and args render
	// one per line with a trailing comma.
	longIdent := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	src := "f(" + longIdent + ", " + longIdent + ", " + longIdent + ", " + longIdent + ")\n"
	got, _ := Format(src)
	if !strings.Contains(got, "f(\n    "+longIdent+",\n") {
		t.Errorf("expected broken form with trailing comma, got %q", got)
	}
	// Confirm the trailing comma after the last arg.
	if !strings.Contains(got, longIdent+",\n)") {
		t.Errorf("expected trailing comma before close paren, got %q", got)
	}
}

func TestFormat_Call_SingleBrokenArg_NoTrailingComma(t *testing.T) {
	src := "fn main() {\n" +
		"    result = compiler.run(\n" +
		"        \"\"\"\n" +
		"            fn main() {\n" +
		"                Unit\n" +
		"            }\n" +
		"            \"\"\",\n" +
		"    )\n" +
		"}\n"
	want := "fn main() {\n" +
		"    result = compiler.run(\n" +
		"        \"\"\"\n" +
		"        fn main() {\n" +
		"            Unit\n" +
		"        }\n" +
		"        \"\"\"\n" +
		"    )\n" +
		"}\n"
	got := formatOnce(t, src)
	if got != want {
		t.Errorf("single broken call arg should not get a trailing comma:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestFormat_LambdaAsSoleArg(t *testing.T) {
	// Lambdas are always inside parens — no trailing sugar.
	src := "Iter.filter(|x| x > 2)\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_LambdaAsLastOfMany(t *testing.T) {
	src := "Iterator.reduce(list, 0, |acc, x| acc + x)\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_LambdaTail_BlockBody_SoleArg(t *testing.T) {
	// When the only call arg is a lambda whose body is a multi-stmt block,
	// the `|params| {` should hug the call's `(` and the `})` should close
	// flush — no extra indent level, no trailing comma.
	src := `lists.map(|x| {
    if x == 3 { return 300 }
    x * 10
})
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

func TestFormat_LambdaTail_BlockBody_MultiArg(t *testing.T) {
	// Same with leading args: they stay inline, the block-bodied lambda
	// hugs the tail.
	src := `Iterator.reduce(list, 0, |acc, x| {
    doubled = x * 2
    acc + doubled
})
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

func TestFormat_LambdaTail_ExprBody_Dangles(t *testing.T) {
	// A single-expression tail lambda whose body breaks: the ) dangles on its
	// own line rather than hugging the body's closing brace (the `else { acc })`
	// pile-up). The if-expr body stays inline on its own indented line.
	src := "Iterator.reduce(list_of_every_item_seen_so_far, |acc = [], item| if f(item) { [item, ..acc] } else { acc })\n"
	want := "Iterator.reduce(list_of_every_item_seen_so_far, |acc = [], item|\n    if f(item) { [item, ..acc] } else { acc }\n)\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_LambdaTail_ExprBody_SoleArg_Dangles(t *testing.T) {
	// Same with no leading args — the lambda is the sole argument.
	src := "some_long_receiver_name.filter_every_item_of_the_receiver(|item| if predicate(item) { keep_it } else { drop_it })\n"
	got, _ := Format(src)
	if strings.Contains(got, "})") {
		t.Errorf("expected dangled ), not a })-hug, got:\n%s", got)
	}
	if !strings.Contains(got, "\n)\n") {
		t.Errorf("expected ) alone on its own line, got:\n%s", got)
	}
}

func TestFormat_LambdaTail_ExprBody_FitsInline(t *testing.T) {
	// Short single-expr tail lambda stays inline — ) hugs, no dangle.
	src := "lists.map(|n| if n > 0 { n } else { 0 })\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_LambdaTail_CaseBody_Dangles(t *testing.T) {
	// A case body is inherently multi-line; the tail lambda's ) dangles
	// instead of hugging the case's closing brace.
	src := "Iter.reduce(frags, |acc = 0, frag| case frag {\n0 -> acc\n1 -> acc + 1\n})\n"
	got, _ := Format(src)
	if strings.Contains(got, "})") {
		t.Errorf("expected dangled ), not a })-hug, got:\n%s", got)
	}
	if !strings.Contains(got, "\n)\n") {
		t.Errorf("expected ) alone on its own line, got:\n%s", got)
	}
}

func TestFormat_Call_FunctionRef_StaysInsideParens(t *testing.T) {
	// A bare function reference (Ident) as the last arg is NOT a lambda
	// and therefore stays inside the parens.
	src := "Iter.map(list, double)\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Case_SimpleArms(t *testing.T) {
	src := `x = case y {
    0 -> "zero"
    _ -> "other"
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Case_Guard(t *testing.T) {
	src := `x = case y {
    n when n > 10 -> "big"
    _ -> "small"
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Case_NestedPattern(t *testing.T) {
	// Note: `Some(Err(_))` and `Some(Err)` produce identical AST — the
	// fast-path `Variant(_)` drops the wildcard — so the canonical form
	// the formatter emits is `Some(Err)`.
	src := `x = case result {
    Some(Ok(n)) -> n
    Some(Err) -> -1
    None -> 0
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Case_VariantWithTuplePattern(t *testing.T) {
	// Tuple-payload variants destructure their tuple explicitly:
	// `Some((n, s))` is one variant pattern wrapping a tuple pattern.
	// The old multi-arg sugar (`Some(n, s)` for a tuple-payload Some)
	// has been removed — the explicit form is the only one accepted.
	src := `x = case pair_opt {
    Some((n, s)) -> s
    None -> ""
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Case_StructPattern(t *testing.T) {
	// Struct patterns with field punning
	src := `x = case user {
    User{name, age} -> name
    _ -> "unknown"
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Case_ListConsPattern(t *testing.T) {
	src := `x = case xs {
    [head, ..tail] -> head
    [] -> 0
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Case_LongArmWraps(t *testing.T) {
	// An arm whose flat form exceeds 100 cols must wrap body to next line
	// two levels in (one for the case body, one for the arm body continuation).
	// The input below is 106 cols flat — comfortably over the default budget.
	src := `x = case user {
    Member{level} when level > 5 -> build_very_very_long_senior_report_name_xyz(user, config, extra_options)
    _ -> default()
}
`
	got, _ := Format(src)
	// Expect: the long arm wraps body to the next line two levels in
	wantSubstr := "Member{level} when level > 5 ->\n        build_very_very_long_senior_report_name_xyz"
	if !strings.Contains(got, wantSubstr) {
		t.Errorf("expected long arm body wrap, got:\n%s", got)
	}
	// The arms break together: the short arm moves its body down too, and a
	// blank line separates the two.
	wantShort := "extra_options)\n\n    _ ->\n        default()\n"
	if !strings.Contains(got, wantShort) {
		t.Errorf("short arm should break with the long one, got:\n%s", got)
	}
}

func TestFormat_Case_MultipleStatementsInArmBody(t *testing.T) {
	// Arm body as an explicit block with multiple statements keeps `-> {` on
	// the pattern's line; the block makes the other arm break too.
	src := `x = case y {
    0 -> {
        z = 1
        z + 1
    }

    _ ->
        0
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Case_NoScrutinee(t *testing.T) {
	// Ad-hoc conditional case: no value between `case` and `{`.
	src := `x = case {
    y > 0 -> "pos"
    _ -> "neg"
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_StructLit_Short_Flat(t *testing.T) {
	src := `u = User{name: "Alice", age: 30}` + "\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_StructLit_Punning(t *testing.T) {
	// User{name, age} — field punning preserved
	src := "u = User{name, age}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_StructLit_Long_Breaks(t *testing.T) {
	// Long struct breaks to one-field-per-line with trailing comma
	long := `u = User{name: "Alice", age: 30, tags: ["new", "unverified"], metadata: "lots_of_stuff_here_to_exceed_budget"}` + "\n"
	got, _ := Format(long)
	if !strings.Contains(got, "User{\n    name") {
		t.Errorf("expected broken form, got:\n%s", got)
	}
	if !strings.Contains(got, ",\n}\n") {
		t.Errorf("expected trailing comma before closing brace, got:\n%s", got)
	}
}

func TestFormat_AnonStructLit(t *testing.T) {
	src := `x = {name: "Alice", age: 30}` + "\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

// TestFormat_StructLit_PreservesUserMultiLine: a named struct literal the
// user wrote across multiple source lines stays multi-line, even when it
// would fit flat. Mirrors the enum preserve-user-multi-line rule.
func TestFormat_StructLit_PreservesUserMultiLine(t *testing.T) {
	src := `x = User{
    name: "Alice",
    age: 30,
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

// TestFormat_AnonStructLit_PreservesUserMultiLine: same rule for an
// anonymous struct literal — multi-line shape is preserved.
func TestFormat_AnonStructLit_PreservesUserMultiLine(t *testing.T) {
	src := `x = {
    name: "Alice",
    age: 30,
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

// TestFormat_AnonStructType_PreservesUserMultiLine: anon struct types
// the user wrote multi-line stay multi-line. Symmetric with the literal
// rule.
func TestFormat_AnonStructType_PreservesUserMultiLine(t *testing.T) {
	src := `fn greet(p: {
    name: String,
    age: Int,
}): String {
    p.name
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

// TestFormat_EnumDef_StructVariantPayload_PreservesUserMultiLine: a
// struct-shaped variant payload the user wrote multi-line stays multi-
// line. Same rule routed through the shared emitBracedStructFieldsWithEndTrivia helper
// (variant payloads keep the comma'd anon-struct form — only the variant
// itself gains the `variant` keyword).
func TestFormat_EnumDef_StructVariantPayload_PreservesUserMultiLine(t *testing.T) {
	src := `enum Error {
    HttpError {
        status: Int,
        message: String,
    }
    Timeout
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

// TestFormat_StructLit_FlatStaysFlat_Regression: short struct literals on
// a single source line keep their flat layout. Regression guard against
// the user-multi-line rule being too aggressive.
func TestFormat_StructLit_FlatStaysFlat_Regression(t *testing.T) {
	src := "x = User{name: \"Alice\", age: 30}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_List_Empty(t *testing.T) {
	got, _ := Format("x = []\n")
	if got != "x = []\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_List_Flat(t *testing.T) {
	got, _ := Format("x = [1, 2, 3]\n")
	if got != "x = [1, 2, 3]\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_List_LongBreaks(t *testing.T) {
	long := "x = [\"aaaaaaaaaaaaaaaa\", \"bbbbbbbbbbbbbbbb\", \"cccccccccccccccc\", \"dddddddddddddddd\", \"eeeeeeeeeeeeeeee\"]\n"
	got, _ := Format(long)
	if !strings.Contains(got, "[\n  ") {
		t.Errorf("expected broken list, got:\n%s", got)
	}
}

func TestFormat_Vector_Empty(t *testing.T) {
	got, _ := Format("x = #[]\n")
	if got != "x = #[]\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Vector_Flat(t *testing.T) {
	got, _ := Format("x = #[1, 2, 3]\n")
	if got != "x = #[1, 2, 3]\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Set_Empty(t *testing.T) {
	got, _ := Format("x = #{}\n")
	if got != "x = #{}\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Set_Flat(t *testing.T) {
	got, _ := Format("x = #{1, 2, 3}\n")
	if got != "x = #{1, 2, 3}\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_ListSpread(t *testing.T) {
	// [head, ..tail] — spread construction.
	src := "x = [1, ..rest]\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_ListSpread_NoHeads(t *testing.T) {
	// [..tail] — spread with no preceding heads. No leading `, `.
	src := "x = [..rest]\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Map_Flat(t *testing.T) {
	src := `x = {"a" => 1, "b" => 2}` + "\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Map_LongBreaks(t *testing.T) {
	long := `x = {"aaaaa" => 1111, "bbbbb" => 2222, "ccccc" => 3333, "ddddd" => 4444, "eeeee" => 5555, "fffff" => 6666}` + "\n"
	got, _ := Format(long)
	if !strings.Contains(got, "{\n  ") {
		t.Errorf("expected broken map, got:\n%s", got)
	}
}

func TestFormat_Tuple(t *testing.T) {
	got, _ := Format("x = (1, 2)\n")
	if got != "x = (1, 2)\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Tuple_Three(t *testing.T) {
	got, _ := Format(`x = (1, "a", True)` + "\n")
	if got != `x = (1, "a", True)`+"\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_FnDef_SingleLine(t *testing.T) {
	// Short fn body still always breaks; round-trip lands on the multi-line
	// canonical form.
	src := "fn add(x: Int, y: Int): Int {\n    x + y\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_FnDef_Pub(t *testing.T) {
	src := "fn add(x: Int, y: Int): Int {\n    x + y\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_FnDef_NoParams(t *testing.T) {
	src := "fn greet(): String {\n    \"hello\"\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_FnDef_LongSig_BreaksParams(t *testing.T) {
	// Signature too long to fit in 100 cols — params break to one-per-line.
	src := "pub fn process_all_the_users(users: List<User>, _config: SomeConfig, _logger: Logger, _retry_count: Int, _more_options: ExtraOpts): Result<List<Report>, Error> { users }\n"
	got, _ := Format(src)
	if !strings.Contains(got, "fn process_all_the_users(\n    users: List<User>,") {
		t.Errorf("expected params broken, got:\n%s", got)
	}
	if !strings.Contains(got, ",\n): ") {
		t.Errorf("expected closing paren on own line before return type, got:\n%s", got)
	}
}

func TestFormat_FnDef_MultiLineBody(t *testing.T) {
	src := `fn f(): Int {
    x = 1
    x
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_FnDef_DocComment(t *testing.T) {
	// /// doc comment should be preserved above the function.
	src := "/// Adds two numbers.\nfn add(x: Int, y: Int): Int {\n    x + y\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_FnDef_DocComment_MultiLine(t *testing.T) {
	// Multi-line /// doc comment: each line preserved with its /// prefix.
	src := "/// Adds two numbers.\n/// Returns the sum.\nfn add(x: Int, y: Int): Int {\n    x + y\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_FnDef_DefaultParam(t *testing.T) {
	// Default values on params.
	src := "fn greet(_name: String = \"World\"): String {\n    \"hello\"\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_FnDef_NoReturnType(t *testing.T) {
	// Functions without explicit return type (Unit-returning).
	src := "fn main() {\n    x = 1\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

// TestFormat_FnDef_AlwaysMultiLine: `fn` bodies with at least one statement
// always break across lines, even when the whole declaration would fit
// flat. Matches the dominant convention for opinionated formatters
// (gofmt, prettier, dart, zig, swift-format, rustfmt default) and removes
// the in-progress-edit churn where saving an in-progress fn collapses the
// open layout the user just made room in.
func TestFormat_FnDef_AlwaysMultiLine(t *testing.T) {
	src := "fn add(x: Int, y: Int): Int { x + y }\n"
	want := "fn add(x: Int, y: Int): Int {\n    x + y\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// TestFormat_FnDef_EmptyBodyStaysFlat: a body with zero statements stays
// flat as `{}` — there's nothing to put on a new line, so the always-
// multi-line rule has no work to do. Matches gofmt: `func f() {}` is left
// alone.
func TestFormat_FnDef_EmptyBodyStaysFlat(t *testing.T) {
	src := "fn noop() {}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

// TestFormat_ImplBlock confirms an `impl Iface for Type { fn ... }` block
// survives a round-trip through the formatter — the header, the `self`
// receiver, and the indented method body are all preserved.
func TestFormat_ImplBlock(t *testing.T) {
	src := `impl Display for Int {
    fn to_string(n: self): String {
        int_to_string(n)
    }
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("formatter changed impl block:\nwant: %q\ngot:  %q", src, got)
	}
}

func TestFormat_AttachedTest(t *testing.T) {
	src := `//! assert answer() == 42
fn answer(): Int { 42 }

//! refute answer() == 41
fn answer_not(): Int { 42 }

//! assert answer() == 42
fn answer_again(): Int { 42 }

//! assert Result.map_err(Result.Err("bad"), |e: String| "error: " + e + ", retried: none") == Result.Err(
//!   "error: bad, retried: none"
//! )
fn answer_lambda(): Int { 42 }

//! value = answer()
//! assert value == 42
//! refute value == 41
fn answer_block(): Int { 42 }
`
	want := `//! assert answer() == 42
fn answer(): Int {
    42
}

//! refute answer() == 41
fn answer_not(): Int {
    42
}

//! assert answer() == 42
fn answer_again(): Int {
    42
}

//! assert Result.map_err(Result.Err("bad"), |e: String|
//!     "error: " + e + ", retried: none"
//! ) == Result.Err("error: bad, retried: none")
fn answer_lambda(): Int {
    42
}

//! value = answer()
//! assert value == 42
//! refute value == 41
fn answer_block(): Int {
    42
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter changed attached test:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_AttachedTestPreservesFollowingDocComment(t *testing.T) {
	src := `/// Computes the answer.
//! assert answer() == 42
// Ordinary note.
/// Used by the examples.
fn answer(): Int { 42 }
`
	want := `/// Computes the answer.
//! assert answer() == 42
// Ordinary note.
/// Used by the examples.
fn answer(): Int {
    42
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter moved attached-test doc comment:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_AttachedTestStripsBlankBeforeDeclaration(t *testing.T) {
	src := `/// Returns the first element.
//! assert first([1, 2, 3]) == Some(1)
//! assert first([]) == None

pub fn first<T>(_source: Iter<T>): Maybe<T> { None }
`
	want := `/// Returns the first element.
//! assert first([1, 2, 3]) == Some(1)
//! assert first([]) == None
pub fn first<T>(_source: Iter<T>): Maybe<T> {
    None
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter kept attached-test blank spacer:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_AttachedTestUsesCommentSeparatorBeforeDeclaration(t *testing.T) {
	src := `/// Returns the first element.
//! assert first([1, 2, 3]) == Some(1)
//! assert first([]) == None
//!
pub fn first<T>(_source: Iter<T>): Maybe<T> { None }
`
	want := `/// Returns the first element.
//! assert first([1, 2, 3]) == Some(1)
//! assert first([]) == None
//
pub fn first<T>(_source: Iter<T>): Maybe<T> {
    None
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter did not canonicalize attached-test prompt spacer:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_AttachedTestUsesCommentSeparatorBeforeLeadingPromptBody(t *testing.T) {
	src := `//!
//! assert answer() == 42
fn answer(): Int { 42 }
`
	want := `//
//! assert answer() == 42
fn answer(): Int {
    42
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter did not canonicalize leading attached-test prompt spacer:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_DocCommentUsesCommentSeparatorBeforeDeclaration(t *testing.T) {
	src := `/// Returns the answer.
///
fn answer(): Int { 42 }
`
	want := `/// Returns the answer.
//
fn answer(): Int {
    42
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter did not canonicalize trailing doc separator:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_AttachedTestUsesCommentSeparatorAfterDocComment(t *testing.T) {
	src := `/// Returns the first element, or None if the iterator is empty.
///
//! assert first([1, 2, 3]) == Some(1)
//! assert first([]) == None
pub fn first<T>(_source: Iter<T>): Maybe<T> { None }
`
	want := `/// Returns the first element, or None if the iterator is empty.
//
//! assert first([1, 2, 3]) == Some(1)
//! assert first([]) == None
pub fn first<T>(_source: Iter<T>): Maybe<T> {
    None
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter did not canonicalize doc/test separator:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_AttachedTestPreservesCommentSeparatorAfterDocComment(t *testing.T) {
	src := `/// Returns the first element, or None if the iterator is empty.
//
//! assert first([1, 2, 3]) == Some(1)
pub fn first<T>(_source: Iter<T>): Maybe<T> { None }
`
	want := `/// Returns the first element, or None if the iterator is empty.
//
//! assert first([1, 2, 3]) == Some(1)
pub fn first<T>(_source: Iter<T>): Maybe<T> {
    None
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter stripped doc/test separator:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_AttachedTestStripsBlankBetweenBlocks(t *testing.T) {
	src := `//! assert answer() == 42

//! refute answer() == 41
fn answer(): Int { 42 }
`
	want := `//! assert answer() == 42
//! refute answer() == 41
fn answer(): Int {
    42
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter kept attached-test block separator:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_AttachedTestCanonicalizesBlankDocBetweenBlocks(t *testing.T) {
	src := `//! assert answer() == 42
///
//! refute answer() == 41
fn answer(): Int { 42 }
`
	want := `//! assert answer() == 42
//
//! refute answer() == 41
fn answer(): Int {
    42
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter did not canonicalize attached-test separator:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_AttachedTestUsesCommentSeparatorForTrailingBlankDocLine(t *testing.T) {
	src := `/// Validates n and wraps it.
//! assert from_int(65) == Some(Codepoint(65))
//! assert from_int(55_296) == None
/// Some docs
/// Other docs
///
pub fn from_int(_n: Int): Maybe<Codepoint> { None }
`
	want := `/// Validates n and wraps it.
//! assert from_int(65) == Some(Codepoint(65))
//! assert from_int(55_296) == None
/// Some docs
/// Other docs
//
pub fn from_int(_n: Int): Maybe<Codepoint> {
    None
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter did not canonicalize trailing blank doc line:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_AttachedTestPreservesInteriorPromptBlank(t *testing.T) {
	src := `//! value = parse("42")
//!
//! assert value == Some(42)
//!
//! refute value == None
fn parse_test(): Unit { Unit }
`
	want := `//! value = parse("42")
//!
//! assert value == Some(42)
//!
//! refute value == None
fn parse_test(): Unit {
    Unit
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter changed attached-test prompt blanks:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_AttachedAssertionBreaksControlFlowLambdaBody(t *testing.T) {
	src := `//! assert loop(|n = 0| if n >= 3 { break n } else { n + 1 }) == 3
pub host fn loop(f: (S) -> S): S
`
	want := `//! assert loop(|n = 0|
//!     if n >= 3 {
//!         break n
//!     } else {
//!         n + 1
//!     }
//! ) == 3
pub host fn loop(f: (S) -> S): S
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter changed attached control-flow lambda:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_AttachedAssertionBreaksWhenPromptedExpressionWraps(t *testing.T) {
	src := `//! assert Codepoint.step_by(try codepoints.from_int(65), 2) == Some(Codepoint(67))
fn step_by(): Unit { Unit }
`
	want := `//! assert Codepoint.step_by(try codepoints.from_int(65), 2) == Some(Codepoint(67))
fn step_by(): Unit {
    Unit
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter changed attached wrapping assertion:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormat_AttachedAssertionPipelineComparisonNeedsNoParens(t *testing.T) {
	src := `//! assert {
//! [1, 2, 3, 4, 5] |> chunks(2) |> to_list() == [[1, 2], [3, 4], [5]]
//! }
fn chunks(): Unit { Unit }
`
	want := `//! assert { [1, 2, 3, 4, 5] |> chunks(2) |> to_list() == [[1, 2], [3, 4], [5]] }
fn chunks(): Unit {
    Unit
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("formatter changed attached pipeline comparison:\nwant: %q\ngot:  %q", want, got)
	}
}

// TestFormat_AtDeriveOnStruct pins that `@derive Iface` on a struct
// survives a formatter round-trip (`nomi fmt -w` must never strip it;
// these cover each type-decl shape). The formatter canonicalises
// multi-field struct bodies onto multiple lines; that's not under test
// here, just baked into the want string.
func TestFormat_AtDeriveOnStruct(t *testing.T) {
	src := `@derive Equatable
struct Point {
  x: Int
  y: Int
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("formatter changed @derive-decorated struct:\nwant: %q\ngot:  %q", src, got)
	}
}

// TestFormat_AtDeriveOnEnum pins @derive round-trip on an enum decl.
func TestFormat_AtDeriveOnEnum(t *testing.T) {
	src := `@derive Equatable
enum Color {
  Red
  Green
  Blue
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("formatter changed @derive-decorated enum:\nwant: %q\ngot:  %q", src, got)
	}
}

// TestFormat_AtDeriveOnTypeDef pins @derive round-trip on a distinct
// type decl — the third (and last) decl shape that may carry @derive.
func TestFormat_AtDeriveOnTypeDef(t *testing.T) {
	src := `@derive Hashable
type Id Int
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("formatter changed @derive-decorated typedef:\nwant: %q\ngot:  %q", src, got)
	}
}

// TestFormat_AtDeriveMultiArg pins that the comma-separated multi-arg
// form preserves arg order and spacing through a round-trip.
func TestFormat_AtDeriveMultiArg(t *testing.T) {
	src := `@derive Equatable, Hashable, Comparable, Debug
struct Point {
  x: Int
  y: Int
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("formatter changed multi-arg @derive:\nwant: %q\ngot:  %q", src, got)
	}
}

// TestFormat_StackedAtDerive pins that multiple `@derive` decorators on
// one decl each render on their own line, in source order, above the
// type header.
func TestFormat_StackedAtDerive(t *testing.T) {
	src := `@derive Equatable
@derive Hashable
struct Point {
  x: Int
  y: Int
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("formatter changed stacked @derive lines:\nwant: %q\ngot:  %q", src, got)
	}
}

// TestFormat_InterfaceFieldRequirement: `field name: T` requirements
// inside an interface body round-trip cleanly. Fields render before
// methods (data shape first, operations on it second).
func TestFormat_InterfaceFieldRequirement(t *testing.T) {
	src := `pub interface AppLike {
    field context: Context
    field port: Int
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

// A group's `boot` line follows the clock's blank line, its call formats as
// any call does, and it keeps the author's spacing to the setup line.
func TestFormat_TestBootLine(t *testing.T) {
	src := `tests "env" {
  clock Clock.Virtual
  boot   server.boot( startup() )
  setup Fixture{port: 3000}
  test "reads active app", {port} {
    assert Config.port == port
  }
}
`
	want := `tests "env" {
    clock Clock.Virtual

    boot server.boot(startup())
    setup Fixture{port: 3000}

    test "reads active app", {port} {
        assert Config.port == port
    }
}
`
	formatWithTwice(t, src, want)
}

// The comments written above and after a group's `boot` and `setup` lines are
// the lines' own, and stay where they were written.
func TestFormat_TestGroupLinesKeepComments(t *testing.T) {
	src := `tests "g" {
    // why this boot
    boot server.boot(startup()) // trailing
    // about setup
    setup Fixture{a: 1} // after setup

    test "t", {a} {
        assert a == 1
    }
}
`
	formatWithTwice(t, src, src)
}

// A group's clock, boot and setup lines may be written anywhere in the group;
// the formatter moves them to the top in the order they run, followed by the
// tests in source order. A blank line written above a moved line no longer
// separates it from its old neighbour, so it is dropped, and the group's
// layout rules space the result.
func TestFormat_TestGroupLinesReordered(t *testing.T) {
	src := `tests "g" {
    test "a" {
        assert true
    }

    setup 1
    boot server.boot(startup())

    test "b", n {
        assert n == 1
    }
    clock Clock.Virtual
}
`
	want := `tests "g" {
    clock Clock.Virtual

    boot server.boot(startup())
    setup 1

    test "a" {
        assert true
    }

    test "b", n {
        assert n == 1
    }
}
`
	formatWithTwice(t, src, want)
}

// The comments above a moved line, and the one after it, move with it.
func TestFormat_TestGroupLinesReorderedKeepComments(t *testing.T) {
	src := `tests "g" {
    test "t", {a} {
        assert a == 1
    }

    // about setup
    setup Fixture{a: 1} // after setup

    // why this boot
    // and its startup
    boot server.boot(startup()) // trailing
    // which clock
    clock Clock.Virtual // virtual
    // end of group
}
`
	want := `tests "g" {
    // which clock
    clock Clock.Virtual // virtual

    // why this boot
    // and its startup
    boot server.boot(startup()) // trailing
    // about setup
    setup Fixture{a: 1} // after setup

    test "t", {a} {
        assert a == 1
    }
    // end of group
}
`
	formatWithTwice(t, src, want)
}

// A written setup block moves whole, with its comments.
func TestFormat_TestGroupSetupBlockReordered(t *testing.T) {
	src := `tests "g" {
    test "t", n {
        assert n == 2
    }
    // build the fixture
    setup {
        x = 1
        x + 1
    }
}
`
	want := `tests "g" {
    // build the fixture
    setup {
        x = 1
        x + 1
    }

    test "t", n {
        assert n == 2
    }
}
`
	formatWithTwice(t, src, want)
}

func TestFormat_TestSetupExpression(t *testing.T) {
	src := `tests "fixture" {
    setup {
        User.fixture()
    }

    test "reads setup", {name} {
        assert name == "Ada"
    }
}
`
	want := `tests "fixture" {
    setup User.fixture()

    test "reads setup", {name} {
        assert name == "Ada"
    }
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_TestSetupStructLiteralExpression(t *testing.T) {
	src := `tests "fixture" {
    setup {
        User{
            name: "Ada",
            role: "admin",
        }
    }

    test "reads setup", {name} {
        assert name == "Ada"
    }
}
`
	want := `tests "fixture" {
    setup User{
        name: "Ada",
        role: "admin",
    }

    test "reads setup", {name} {
        assert name == "Ada"
    }
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A test binds its group's setup value with any irrefutable pattern.
func TestFormat_TestTuplePattern(t *testing.T) {
	src := `tests "fixture" {
    setup (1,   "ready")

    test "reads setup", (n,  child) {
        assert child == "ready"
    }
}
`
	want := `tests "fixture" {
    setup (1, "ready")

    test "reads setup", (n, child) {
        assert child == "ready"
    }
}
`
	formatWithTwice(t, src, want)
}

// TestFormat_InterfaceFieldsBeforeMethods: an interface body emits every field
// requirement first, then every method, regardless of source order.
func TestFormat_InterfaceFieldsBeforeMethods(t *testing.T) {
	src := `interface AppLike {
    fn name(value: self): String
    field context: Context
}
`
	want := `interface AppLike {
    field context: Context
    fn name(value: self): String
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestFormat_InterfaceOpenDefault: the `open` modifier on an
// overridable default method round-trips.
func TestFormat_InterfaceOpenDefault(t *testing.T) {
	src := `interface MyIface {
    fn required(value: self): Int
    open fn extension_point(_value: self): String {
        "default"
    }
}
`
	want := `interface MyIface {
    fn required(value: self): Int

    open fn extension_point(_value: self): String {
        "default"
    }
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestFormat_EmptyConformanceBlock: a field-only-interface conformance
// assertion is a bodyless `impl Iface for Struct` declaration — it renders on a
// single line and round-trips unchanged.
func TestFormat_EmptyConformanceBlock(t *testing.T) {
	src := `pub struct AppEnv {
    context: Context
}

impl AppLike for AppEnv
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

// TestFormat_InterfaceDefaultMethod_AlwaysMultiLine: default-method
// bodies inside an interface declaration are also `fn` bodies and follow
// the always-multi-line rule.
func TestFormat_InterfaceDefaultMethod_AlwaysMultiLine(t *testing.T) {
	src := `interface Greeter {
    fn greet(_g: self): String { "hi" }
}
`
	want := `interface Greeter {
    fn greet(_g: self): String {
        "hi"
    }
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestFormat_If_Short_Flat_StillFlat: the always-multi-line rule is
// scoped to fn bodies only. Inline `if cond { x } else { y }` expressions
// keep their flat form so guards like `if n < 0 { return }` still read
// inline.
func TestFormat_If_Short_Flat_StillFlat(t *testing.T) {
	src := "fn main() {\n    x = if y { 1 } else { 2 }\n    x\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

func TestFormat_If_Short_Flat(t *testing.T) {
	// Short if/else with expression bodies stays on one line.
	src := "x = if y { 1 } else { 2 }\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_If_NoElse_Short(t *testing.T) {
	src := "x = if y { 1 }\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_If_LongBodyBreaks(t *testing.T) {
	// A one-line form well past the 50-column cap force-breaks — and breaks
	// all-or-nothing: BOTH branches go vertical, no partial layout where one
	// stays flat.
	src := `x = if condition_is_really_long { very_long_function_call(arg1, arg2) } else { another_long_function(arg3, arg4) }` + "\n"
	got, _ := Format(src)
	if !strings.Contains(got, "if condition_is_really_long {\n") {
		t.Errorf("expected then-branch to break, got:\n%s", got)
	}
	if !strings.Contains(got, "\n    very_long_function_call(arg1, arg2)\n") {
		t.Errorf("expected indented then body, got:\n%s", got)
	}
	if !strings.Contains(got, "} else {\n") {
		t.Errorf("expected else-branch to break, got:\n%s", got)
	}
	if !strings.Contains(got, "\n    another_long_function(arg3, arg4)\n") {
		t.Errorf("expected indented else body, got:\n%s", got)
	}
}

func TestFormat_IfElseChain_AlwaysBreaks(t *testing.T) {
	// `else if` chains always render in broken form, even when every branch
	// is atomic and the whole thing would fit on one line. Multi-rung
	// conditionals are easier to read with each rung on its own line.
	src := "x = if a { 1 } else if b { 2 } else { 3 }\n"
	want := "x = if a {\n    1\n} else if b {\n    2\n} else {\n    3\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_IfElseChain_Multiline(t *testing.T) {
	// else-if chains break uniformly — no partial-flat layouts where some
	// rungs stay inline and only the final block breaks.
	src := `x = if aaaaaa { first_branch_result_val } else if bbbbbb { second_branch_result_val } else { third_branch_result_val }` + "\n"
	want := "x = if aaaaaa {\n    first_branch_result_val\n} else if bbbbbb {\n    second_branch_result_val\n} else {\n    third_branch_result_val\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_If_MultiStmtBody(t *testing.T) {
	// The `then` branch has multiple statements, which is a non-atomic body.
	// Under Option A, any non-atomic branch forces the whole chain broken,
	// including the `else { 0 }` branch, so both render multi-line.
	src := `x = if cond {
    y = 1
    y
} else {
    0
}
`
	want := "x = if cond {\n    y = 1\n    y\n} else {\n    0\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_If_AtomicBodies_StaysFlat(t *testing.T) {
	// Literal bodies are atomic — inline form is permitted.
	src := "x = if cond { 1 } else { 2 }\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_If_ShortCallBody_StaysInline(t *testing.T) {
	// Model A: branch content no longer matters — a call body stays inline as
	// long as the one-line form fits 50 columns. (The old atomic rule broke
	// this.)
	src := "x = if cond { f(a) } else { g(b) }\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_If_FieldAccess_StaysInline(t *testing.T) {
	// Field-access branch bodies are inline-eligible like any other content;
	// this one fits the 50-col cap (the longer u.admin_perms/u.default_perms
	// variant is 51 cols and is covered by the width-boundary test).
	src := "x = if admin { u.perms } else { u.base }\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_If_ShortMixedBody_StaysInline(t *testing.T) {
	// Model A: a mix of atomic + call bodies stays inline when the one-line
	// form fits 50 columns. (The old atomic rule broke the whole chain.)
	src := "x = if cond { 1 } else { compute() }\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_If_BinaryOpBody_StaysInline(t *testing.T) {
	// The motivating case: a binary-op branch body is no longer force-broken.
	// `e + 1` is ~30 columns inline — well under the 50 cap.
	src := "limit = if inclusive { e + 1 } else { e }\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_If_MultilineCallBody_Collapses(t *testing.T) {
	// Model A is canonical, not authorship-preserving: a short if/else authored
	// across multiple lines collapses back to one line. (Under the old atomic
	// rule the call bodies kept it broken.)
	src := "x = if cond {\n  f(a)\n} else {\n  g(b)\n}\n"
	want := "x = if cond { f(a) } else { g(b) }\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_If_WidthBoundary_50Inline_51Broken(t *testing.T) {
	// Flat if/else width = 18 (fixed structure) + len(cond)+len(then)+len(else).
	// With cond="c", else="e", a 30-char then body hits exactly 50 → inline;
	// 31 → 51 → broken. Locks the singleLineIfElseMaxWidth = 50 cap.
	at50 := "x = if c { " + strings.Repeat("a", 30) + " } else { e }\n"
	got, _ := Format(at50)
	if got != at50 {
		t.Errorf("expected inline at 50 cols, got:\n%s", got)
	}

	at51 := "x = if c { " + strings.Repeat("a", 31) + " } else { e }\n"
	got, _ = Format(at51)
	if !strings.Contains(got, "} else {\n") {
		t.Errorf("expected broken at 51 cols, got:\n%s", got)
	}
}

func TestFormat_If_InlineEligibleButLineTooLong_BreaksAllOrNothing(t *testing.T) {
	// An if/else whose own flat form fits 50 but whose enclosing line exceeds
	// 100 breaks ALL branches — never a partial layout (one branch broken, the
	// other left inline).
	src := "some_really_quite_extremely_long_destination_name_for_the_value = if cond { value_aaaa } else { value_bbbb }\n"
	want := "some_really_quite_extremely_long_destination_name_for_the_value = if cond {\n    value_aaaa\n} else {\n    value_bbbb\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_If_CaseBody_Breaks(t *testing.T) {
	// A `case` is inherently multi-line (emits HardLines), so the flat-fit
	// check rejects it regardless of character count — it can never be crammed
	// onto one line.
	src := "x = if c { case n { 0 -> 1\n_ -> 2 } } else { 0 }\n"
	got, _ := Format(src)
	if !strings.Contains(got, "if c {\n") {
		t.Errorf("expected if with a case body to break, got:\n%s", got)
	}
}

func TestFormat_If_MultilinePipeBody_Breaks(t *testing.T) {
	// A pipe authored multi-line is inherently multi-line (HardLines), so a
	// branch containing one forces the if/else broken.
	src := "x = if c { a\n|> f()\n|> g() } else { b }\n"
	got, _ := Format(src)
	if !strings.Contains(got, "if c {\n") {
		t.Errorf("expected multi-line pipe branch to break the if, got:\n%s", got)
	}
}

func TestFormat_If_InlinePipeBody_StaysInline(t *testing.T) {
	// An *inline* multi-stage pipe no longer forces a break (author-preserving
	// pipes): a short inline-pipe branch keeps the if/else on one line.
	src := "x = if c { a |> f() |> g() } else { b }\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q, want unchanged inline", got)
	}
}

func TestFormat_If_SinglePipeBody_StaysInline(t *testing.T) {
	// A single pipe stays inline (no forced break), so a short branch using one
	// keeps the if/else on a line.
	src := "x = if c { a |> f() } else { b }\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_If_ElseIfChain_AllAtomic_BreaksAnyway(t *testing.T) {
	// `else if` chains always break, even when every branch is atomic and
	// the flat form fits in the width budget.
	src := "x = if a { 1 } else if b { 2 } else { 3 }\n"
	want := "x = if a {\n    1\n} else if b {\n    2\n} else {\n    3\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_If_ElseIfChain_NonAtomic_AllBreak(t *testing.T) {
	// One non-atomic branch (a call in the middle arm) forces the entire
	// else-if chain into broken form — no per-branch mixing.
	src := "x = if a { 1 } else if b { compute() } else { 3 }\n"
	want := "x = if a {\n    1\n} else if b {\n    compute()\n} else {\n    3\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Declarations
// ---------------------------------------------------------------------------

func TestFormat_StructDef(t *testing.T) {
	src := `struct User {
    name: String
    age: Int
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_StructDef_Pub(t *testing.T) {
	src := `pub struct User {
    name: String
    age: Int
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_StructDef_FieldDefault(t *testing.T) {
	src := `struct Response {
    status: Int = 200
    body: String
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_StructDef_Empty(t *testing.T) {
	src := "struct Marker {}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_StructDef_Generic(t *testing.T) {
	src := `struct Pair<T, U> {
    first: T
    second: U
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_StructDef_Doc(t *testing.T) {
	src := `/// A user.
struct User {
    name: String
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_EnumDef(t *testing.T) {
	// Enums are always multi-line variant lists, even when short.
	src := `enum Direction {
    North
    South
    East
    West
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_EnumDef_Positional(t *testing.T) {
	src := `enum Shape {
    Circle Float
    Point
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_EnumDef_StructVariant(t *testing.T) {
	// Struct-payload variant: the payload keeps its comma'd anon-struct
	// braces on the variant row.
	src := `enum Shape {
    Circle Float
    Rectangle {width: Float, height: Float}
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_EnumDef_StructVariantDefault(t *testing.T) {
	src := `enum Error {
    HttpError {status: Int, message: String, retryable: Bool = False}
    Timeout
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

// TestFormat_EnumDef_StructVariant_LongPayload_Breaks: a struct-shaped
// variant payload that doesn't fit on its variant row breaks across lines
// with each field indented and trailing-commaed. Symmetric with the
// AnonStructType width-based rule — both render `Name {a: Int, ...}` and
// share user intent ("brace-bounded fields break when long"), so the
// layout rule is the same even though they're distinct AST nodes (struct
// variants are EnumVariant{Kind: "struct"}, not *ast.AnonStructType).
func TestFormat_EnumDef_StructVariant_LongPayload_Breaks(t *testing.T) {
	src := `pub enum Error {
    HttpError {status: Int, message: String, retryable: Bool = False, some_very_long_field_name: String, attempt: Int}
    Timeout
    ConnectionRefused
}
`
	want := `pub enum Error {
    HttpError {
        status: Int,
        message: String,
        retryable: Bool = False,
        some_very_long_field_name: String,
        attempt: Int,
    }
    Timeout
    ConnectionRefused
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestFormat_Variant_NoParens verifies the formatter emits the
// space-separated payload form, not the legacy paren-wrapped form.
func TestFormat_Variant_NoParens(t *testing.T) {
	src := "enum T { Foo(Int) }"
	want := "enum T {\n    Foo Int\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// TestFormat_Variant_TuplePayload verifies a tuple payload is emitted
// with one set of parens (not the legacy double-paren form).
func TestFormat_Variant_TuplePayload(t *testing.T) {
	src := "enum T { Position((Int, Int)) }"
	want := "enum T {\n    Position (Int, Int)\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// TestFormat_Variant_StructWithSpace verifies struct-payload variants
// gain a leading space before `{`.
func TestFormat_Variant_StructWithSpace(t *testing.T) {
	src := "enum T { Card{a: Int} }"
	want := "enum T {\n    Card {a: Int}\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// TestFormat_Variant_FunctionPayload verifies a function-typed payload
// formats as `Name (Args) -> Ret`.
func TestFormat_Variant_FunctionPayload(t *testing.T) {
	src := "enum T { Computation((Int) -> Int) }"
	want := "enum T {\n    Computation (Int) -> Int\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// TestFormat_EnumDef_Embeds: the embedded form renders as
// `embeds Type`.
func TestFormat_EnumDef_Embeds(t *testing.T) {
	src := `enum Drawable {
    embeds Circle
    Line
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

// TestFormat_EnumDef_Pub: `pub` renders before `enum`.
func TestFormat_EnumDef_Pub(t *testing.T) {
	src := `pub enum Color {
    Red
    Green
    Blue
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

// TestFormat_Enum_AlwaysMultiLine: even a two-variant enum that would fit
// on one line stacks — enums are always multi-line variant lists.
func TestFormat_Enum_AlwaysMultiLine(t *testing.T) {
	src := "enum E { A; B }"
	want := "enum E {\n    A\n    B\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_Interface(t *testing.T) {
	src := `interface Speech {
  speak(animal: self): String
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Interface_Generic(t *testing.T) {
	src := `interface Iter<T> {
  next(collection: self): Maybe<(T, self)>
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Interface_MultipleMethods(t *testing.T) {
	src := `interface Shape {
    fn area(s: self): Float
    fn perimeter(s: self): Float
}
`
	want := `interface Shape {
    fn area(s: self): Float
    fn perimeter(s: self): Float
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_Interface_DefaultMethod(t *testing.T) {
	src := `interface Identity {
    fn name(value: self): String
    fn describe(_value: self): String { "hi" }
}
`
	want := `interface Identity {
    fn name(value: self): String

    fn describe(_value: self): String {
        "hi"
    }
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_TypeAlias(t *testing.T) {
	src := "typealias Handler (String, String) -> Result<String, String>\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_TypeAlias_Pub(t *testing.T) {
	src := "typealias UserId Int\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_ConsecutiveTypeAliasesCanStayTight(t *testing.T) {
	src := "typealias UserList List<User>\ntypealias UserMap Map<String, User>\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_ConsecutiveTypeAliasesPreserveGroupingBlank(t *testing.T) {
	src := "typealias PublicList List<User>\n\ntypealias InternalList List<InternalUser>\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_DistinctType(t *testing.T) {
	src := "type UserId Int\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_DistinctType_ZeroSized(t *testing.T) {
	src := "type Expired\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_DistinctType_Pub(t *testing.T) {
	src := "type Id Int\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_ConsecutiveBodylessTypesCanStayTight(t *testing.T) {
	src := `pub type Email String
pub type Coord (Int, Int)
pub type Handler (String, String) -> Result<String, String>
pub type Expired
pub opaque type Counter Int
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_ConsecutiveBodylessTypesPreserveGroupingBlank(t *testing.T) {
	src := `pub type Email String
pub type Coord (Int, Int)

pub type Expired
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_TypeAndTypeAliasStaySeparate(t *testing.T) {
	src := "type UserId Int\ntypealias UserIdList List<UserId>\n"
	want := "type UserId Int\n\ntypealias UserIdList List<UserId>\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_DocCommentedTypeKeepsSeparation(t *testing.T) {
	src := `type UserId Int
/// Public token marker.
type Token
type Secret
`
	want := `type UserId Int

/// Public token marker.
type Token

type Secret
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_DistinctType_FuncType(t *testing.T) {
	src := "type Callback (String) -> String\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Once(t *testing.T) {
	src := "once pi = 3.14\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Once_Annotated(t *testing.T) {
	src := "once max_retries: Int = 3\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Once_Expression(t *testing.T) {
	src := "once timeout = 30 * 1000\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_ConsecutiveOnceDeclarationsStayTight(t *testing.T) {
	src := "once doubled = 21 * 2\nonce greeting = \"Hello\"\nonce seq = [1, 2, 3]\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_ExternFn(t *testing.T) {
	src := "host fn sqrt(x: Float): Float\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_ExternFn_NoReturn(t *testing.T) {
	src := "host fn do_thing(x: Int)\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_ExternType(t *testing.T) {
	src := "host type Duration\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_ExternType_Generic(t *testing.T) {
	src := "host type Map<K, V>\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Expressions
// ---------------------------------------------------------------------------

func TestFormat_FieldAccess(t *testing.T) {
	got, _ := Format("x = user.name\n")
	if got != "x = user.name\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_FieldAccess_Chained(t *testing.T) {
	got, _ := Format("x = person.address.city\n")
	if got != "x = person.address.city\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_TupleIndex_Zero(t *testing.T) {
	got, _ := Format("x = pair.0\n")
	if got != "x = pair.0\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_TupleIndex_One(t *testing.T) {
	got, _ := Format("x = pair.1\n")
	if got != "x = pair.1\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_PartialApp(t *testing.T) {
	got, _ := Format("f = add(1, _)\n")
	if got != "f = add(1, _)\n" {
		t.Errorf("got %q", got)
	}
}

func TestFormat_PartialApp_Pipe(t *testing.T) {
	// Placeholder used in pipe target: `10 |> divide(100, _)`. Written inline
	// and within budget, the chain stays on one line (width-based pipes).
	src := "x = 10 |> divide(100, _)\n"
	want := "x = 10 |> divide(100, _)\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_TrailingPipeAssertionsCanonicalizeToHeadAssertions(t *testing.T) {
	src := `test "formatter canonicalizes assertion pipelines" {
    "Ada Lovelace"
    |> strings.contains?("Ada")
    |> assert
    "Ada Lovelace"
    |> strings.contains?("Grace")
    |> refute
}
`
	want := `test "formatter canonicalizes assertion pipelines" {
    assert "Ada Lovelace"
        |> strings.contains?("Ada")

    refute "Ada Lovelace"
        |> strings.contains?("Grace")
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_AssertionPipelineBeforeNegativeLiteral(t *testing.T) {
	src := `test "assertion pipeline before negative literal" {
    5
    |> assert Int.to_float()

    -7
    |> assert Int.to_float()
}
`
	want := `test "assertion pipeline before negative literal" {
    assert 5
        |> Int.to_float()

    assert -7
        |> Int.to_float()
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_PatternAssertionPipelineIndentsContinuation(t *testing.T) {
	src := `test "pattern assertion pipeline" {
    assert Some(name) = 1
    |> find_name()
}
`
	want := `test "pattern assertion pipeline" {
    assert Some(name) = 1
        |> find_name()
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_Trivia_StandaloneCommentBetweenPipeStages(t *testing.T) {
	src := `test "comment between pipe stages" {
    5
    |> Int.to_float()
    // a comment
    |> then |value| value == 5.0
    |> assert
}
`
	want := `test "comment between pipe stages" {
    assert 5
        |> Int.to_float()
        // a comment
        |> then |value| value == 5.0
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_StringInterp_Simple(t *testing.T) {
	src := `x = "hello ${name}"` + "\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_StringInterp_MultipleParts(t *testing.T) {
	src := `x = "hello ${name}, you are ${age}"` + "\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_StringInterp_LeadingExpr(t *testing.T) {
	src := `x = "${greeting} world"` + "\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_StringInterp_WithEscapes(t *testing.T) {
	// Escape sequences round-trip: `\n` in source → `\n` in output.
	src := `x = "line: ${name}\n"` + "\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

// Round-trip a non-interpolated StringLit whose value contains a literal `${`.
// The decoded value is `literal: ${name}`; the formatter must write it
// `\${name}` so re-lexing reproduces the same StringLit rather than reading
// an interpolation slot.
func TestFormat_StringLit_LiteralDollarBrace(t *testing.T) {
	src := `x = "literal: \${name}"` + "\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("round-trip failed:\n got: %q\nwant: %q", got, src)
	}
}

// A `#{` in a string is ordinary text and needs no escape.
func TestFormat_StringLit_HashBraceIsText(t *testing.T) {
	src := `x = "cost: #{n} ##"` + "\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("round-trip failed:\n got: %q\nwant: %q", got, src)
	}
}

// A static text segment holding a literal `${` beside an interpolation slot.
func TestFormat_StringInterp_LiteralDollarBraceInTextSegment(t *testing.T) {
	src := `x = "\${name} is ${greeting}"` + "\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("round-trip failed:\n got: %q\nwant: %q", got, src)
	}
}

// A backslash before a literal `${`: the decoded value is `\${`, written as
// the escaped backslash then the escaped opener.
func TestFormat_StringLit_BackslashBeforeDollarBrace(t *testing.T) {
	src := `x = "\\\${"` + "\n"
	if firstStringValue(t, src) != `\${` {
		t.Fatalf("decoded %q", firstStringValue(t, src))
	}
	got, _ := Format(src)
	if got != src {
		t.Errorf("round-trip failed:\n got: %q\nwant: %q", got, src)
	}
}

// --- Triple-quoted string round-trip ---
//
// The bar for these tests is idempotence: format(format(src)) ==
// format(src), and (for canonical inputs) format(src) == src. The goal
// of the structural fix is that triple-quoted literals survive formatting
// as triple-quoted, rather than being downgraded to single-line `\n`-
// escaped form.

// formatOnce wraps Format and fails the test on parse error.
func formatOnce(t *testing.T, src string) string {
	t.Helper()
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format(%q) error: %v", src, err)
	}
	return got
}

// firstStringValue lexes src and returns the decoded value of its first
// single-line string literal (the lexer stores the decoded value in Lexeme).
func firstStringValue(t *testing.T, src string) string {
	t.Helper()
	for _, tok := range lexer.Lex(src) {
		if tok.Type == token.STRING_LITERAL {
			return tok.Lexeme
		}
	}
	t.Fatalf("no STRING_LITERAL token in %q", src)
	return ""
}

// Regression: the formatter must re-encode non-printable runes using the
// lexer's escape syntax (\u{HEX}), not Go's strconv.Quote syntax (\uHEX,
// \xHH, \'U........), which the lexer cannot read back. U+200D (ZERO WIDTH
// JOINER) is a format char that strconv.Quote escapes as \u200d; the Nomi
// lexer keeps a brace-less \u200d as six literal characters, corrupting the
// value and breaking the formatter's semantic-preservation guarantee.
func TestFormat_StringWithFormatChar_RoundTrips(t *testing.T) {
	src := "x = \"a\u200db\"\n"
	want := firstStringValue(t, src)
	got := formatOnce(t, src)
	if gotVal := firstStringValue(t, got); gotVal != want {
		t.Errorf("format corrupted string value:\n  in value:  %q\n  out value: %q\n  formatted: %q", want, gotVal, got)
	}
	if twice := formatOnce(t, got); twice != got {
		t.Errorf("not idempotent:\n  once:  %q\n  twice: %q", got, twice)
	}
}

func TestFormat_TripleStringLit_Idempotent(t *testing.T) {
	// Multi-line triple-quoted literal inside a fn body. The block adds
	// one level of indent; the StringLit's Nest adds another, so the
	// canonical body indent is 8.
	src := "fn main() {\n    x = \"\"\"\n        SELECT *\n        FROM users\n        \"\"\"\n}\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != src {
		t.Errorf("canonical form not preserved:\n want: %q\n  got: %q", src, once)
	}
}

func TestFormat_TripleStringInterp_Idempotent(t *testing.T) {
	src := "fn main() {\n    name = \"Alice\"\n\n    q = \"\"\"\n        SELECT *\n        FROM users\n        WHERE name = '${name}'\n        \"\"\"\n}\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != src {
		t.Errorf("canonical form not preserved:\n want: %q\n  got: %q", src, once)
	}
}

func TestFormat_TripleStringWithLiteralDollarBrace_Idempotent(t *testing.T) {
	// Body has literal `\${name}` (decoded value: `${name}`). The
	// formatter must re-emit `\${` so the lexer reads it as text rather
	// than an interpolation slot.
	src := "fn main() {\n    q = \"\"\"\n        literal: \\${name} and #{tag}\n        \"\"\"\n}\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != src {
		t.Errorf("canonical form not preserved:\n want: %q\n  got: %q", src, once)
	}
}

func TestFormat_TripleStringEmpty_Idempotent(t *testing.T) {
	// Empty body — """ followed immediately by """ on the next line.
	src := "fn main() {\n  q = \"\"\"\n    \"\"\"\n}\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
}

func TestFormat_TripleStringSingleLineBody_Idempotent(t *testing.T) {
	// `"""body"""` (no leading/trailing newline). Decoded value is
	// `body`. Canonical output is the multi-line form with the body
	// on its own indented line — same Doc shape the multi-line case
	// produces, just with one body line.
	src := "fn main() {\n    q = \"\"\"body\"\"\"\n}\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	// The canonical form pulls the body onto its own line.
	want := "fn main() {\n    q = \"\"\"\n        body\n        \"\"\"\n}\n"
	if once != want {
		t.Errorf("canonical form mismatch:\n want: %q\n  got: %q", want, once)
	}
}

func TestFormat_TripleStringTopLevel_Idempotent(t *testing.T) {
	// At top level (no enclosing block), the body indent is just
	// defaultIndent (4 spaces).
	src := "q = \"\"\"\n    SELECT *\n    FROM users\n    \"\"\"\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != src {
		t.Errorf("canonical form not preserved:\n want: %q\n  got: %q", src, once)
	}
}

// Regression: the formatter previously downgraded triple-quoted
// literals to single-line `\n`-escaped form, losing the source-form
// distinction. The structural fix preserves it.
func TestFormat_TripleStringNotDowngradedToSingleLine(t *testing.T) {
	src := "fn main() {\n  q = \"\"\"\n    SELECT *\n    FROM users\n    \"\"\"\n}\n"
	got := formatOnce(t, src)
	// The formatted output must contain the literal `"""` triple-quote
	// marker, not a single-line `"...\\n..."` form.
	if !strings.Contains(got, `"""`) {
		t.Errorf("expected triple-quoted form preserved, got %q", got)
	}
	if strings.Contains(got, `\n`) {
		t.Errorf("expected real newlines, found `\\n` escape in output: %q", got)
	}
}

// --- Raw string round-trip ---
//
// Raw strings (backtick literals) disable `${...}` parsing and the
// `\${` escape — the body is verbatim. The formatter must emit them in raw
// form and must NOT escape `${` in the body, otherwise a literal `${X}`
// would gain a backslash on every format pass and break idempotence.

func TestFormat_RawSingleLine_Idempotent(t *testing.T) {
	src := "x = `price: ${VAR}`\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != src {
		t.Errorf("canonical form not preserved:\n want: %q\n  got: %q", src, once)
	}
	// Confirm the literal ${VAR} survives — it must NOT be escaped.
	if !strings.Contains(once, "${VAR}") {
		t.Errorf("expected literal ${VAR} preserved, got %q", once)
	}
	if strings.Contains(once, `\${VAR}`) {
		t.Errorf("raw form escaped ${: %q", once)
	}
}

func TestFormat_RawSingleLine_NoDollarEscaping(t *testing.T) {
	// `\${X}` in the source is verbatim text in raw form. The formatter
	// must round-trip it with its backslash, NOT drop the backslash and
	// NOT add a second one.
	src := "x = `\\${already_escaped}`\n"
	formatted := formatOnce(t, src)
	if formatted != src {
		t.Fatalf("raw form re-encoded \\${: want %q, got %q", src, formatted)
	}
}

func TestFormat_RawSingleLine_BackslashLiteral(t *testing.T) {
	// `\d` in raw form is a literal backslash + literal `d`. The
	// formatter must emit it as `\d`, not as `\\d` (which is what
	// strconv.Quote would produce for a non-raw string).
	src := "x = `\\d{3,4}`\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != src {
		t.Errorf("canonical form not preserved:\n want: %q\n  got: %q", src, once)
	}
}

func TestFormat_RawTriple_Idempotent(t *testing.T) {
	// Regex with `\d{3,4}` survives verbatim — `\` is literal, no
	// escape interpretation.
	src := "x = `\n    \\d{3,4}\n    `\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != src {
		t.Errorf("canonical form not preserved:\n want: %q\n  got: %q", src, once)
	}
}

func TestFormat_RawTripleWithLiteralDollarBrace_Idempotent(t *testing.T) {
	// Critical: in raw form, `${name}` is literal — the formatter must
	// NOT escape it to `\${name}` on emission, because the re-lex of the
	// backtick string is in raw mode and would keep the backslash.
	src := "x = `\n    for f in *.sh; do echo ${f}; done\n    `\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != src {
		t.Errorf("canonical form not preserved:\n want: %q\n  got: %q", src, once)
	}
	// Confirm ${f} is preserved verbatim.
	if !strings.Contains(once, "${f}") {
		t.Errorf("expected literal ${f} preserved, got %q", once)
	}
	if strings.Contains(once, `\${f}`) {
		t.Errorf("raw form escaped ${: %q", once)
	}
}

func TestFormat_RawTriple_EscapedDollarLiteral(t *testing.T) {
	// `\${` in raw form is verbatim — the backslash must stay.
	src := "x = `\n    cost: \\${price}\n    `\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != src {
		t.Errorf("canonical form not preserved:\n want: %q\n  got: %q", src, once)
	}
}

func TestFormat_RawTriple_IndentStripping(t *testing.T) {
	// Raw triple uses the same min-indent semantics as plain triple
	// — indentation is stripped during lexing, the
	// formatter reapplies a canonical body indent on emission.
	src := "fn main() {\n    q = `\n        \\d{3,4}\n        `\n}\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != src {
		t.Errorf("canonical form not preserved:\n want: %q\n  got: %q", src, once)
	}
}

// --- Triple-quoted string: call-arg layout exception ---
//
// A multi-line triple-quoted string that is a direct argument of a
// function call places its content at the SAME column as the opening
// `"""`, not at +defaultIndent. Bindings (where the opening `"""` sits
// on a different line from the binding name) keep the +defaultIndent
// rule. The two halves are exercised below.

func TestFormat_TripleStringCallArg_ContentAtOpening(t *testing.T) {
	// Input: the +defaultIndent layout (content indented one level inside the
	// opening `"""`'s column). The formatter must normalize it to
	// content-at-opening: closing and body both at the opening's column.
	src := "fn main() {\n" +
		"    show(\n" +
		"        \"label\",\n" +
		"        \"\"\"\n" +
		"            hello\n" +
		"            \"\"\",\n" +
		"    )\n" +
		"}\n"
	want := "fn main() {\n" +
		"    show(\n" +
		"        \"label\",\n" +
		"        \"\"\"\n" +
		"        hello\n" +
		"        \"\"\",\n" +
		"    )\n" +
		"}\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != want {
		t.Errorf("call-arg triple-quoted layout mismatch:\n want: %q\n  got: %q", want, once)
	}
}

func TestFormat_TripleStringBinding_PreservesPlusTwo(t *testing.T) {
	// Companion to the call-arg test: bindings keep the +defaultIndent
	// layout. Input here is already canonical for a binding-RHS triple-
	// quoted string (block adds one level; the StringLit's Nest adds another; the
	// canonical body indent is 8). Round-trip must preserve it.
	src := "fn main() {\n" +
		"    greeting = \"\"\"\n" +
		"        hello\n" +
		"        \"\"\"\n" +
		"}\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != src {
		t.Errorf("binding triple-quoted layout changed (must stay at +defaultIndent):\n want: %q\n  got: %q", src, once)
	}
}

func TestFormat_TripleStringCallArg_Interp(t *testing.T) {
	// Interpolated triple-quoted as a call arg gets the same content-at-
	// opening treatment. Pin the layout + idempotency.
	src := "fn main() {\n" +
		"    name = \"Ada\"\n" +
		"    show(\n" +
		"        \"\"\"\n" +
		"            Hello, ${name}!\n" +
		"            \"\"\",\n" +
		"    )\n" +
		"}\n"
	want := "fn main() {\n" +
		"    name = \"Ada\"\n" +
		"\n" +
		"    show(\n" +
		"        \"\"\"\n" +
		"        Hello, ${name}!\n" +
		"        \"\"\"\n" +
		"    )\n" +
		"}\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != want {
		t.Errorf("call-arg interp triple-quoted layout mismatch:\n want: %q\n  got: %q", want, once)
	}
}

func TestFormat_TripleStringSingleArgWrapperInStructField(t *testing.T) {
	src := "fn main() {\n" +
		"    p = Project{\n" +
		"        manifest: Some(\n" +
		"            \"\"\"\n" +
		"                [module]\n" +
		"                name = \"demo\"\n" +
		"                \"\"\",\n" +
		"        ),\n" +
		"    }\n" +
		"}\n"
	want := "fn main() {\n" +
		"    p = Project{\n" +
		"        manifest: Some(\n" +
		"            \"\"\"\n" +
		"            [module]\n" +
		"            name = \"demo\"\n" +
		"            \"\"\"\n" +
		"        ),\n" +
		"    }\n" +
		"}\n"
	once := formatOnce(t, src)
	twice := formatOnce(t, once)
	if once != twice {
		t.Fatalf("not idempotent:\n  once: %q\n twice: %q", once, twice)
	}
	if once != want {
		t.Errorf("wrapped call-arg triple-quoted layout mismatch:\n want: %q\n  got: %q", want, once)
	}
}

func TestFormat_Try(t *testing.T) {
	// `try x` prefix — round-trip through a function body.
	src := "fn f(): Result<Int, Error> { y = try x \n y }\n"
	got, _ := Format(src)
	if !strings.Contains(got, "try x") {
		t.Errorf("expected try x in output, got %q", got)
	}
}

func TestFormat_Try_Binding(t *testing.T) {
	src := "x = try safe_div(10, 2)\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Return(t *testing.T) {
	src := "fn f(x: Int): Int {\n    if x > 0 { return 42 }\n    x\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Return_Bare(t *testing.T) {
	src := `fn f() {
    x = 1
    return
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Break_Bare(t *testing.T) {
	// Bare break inside a trailing lambda.
	src := `Iter.each(list) { x ->
  if x == 0 { break }
  x
}
`
	got, _ := Format(src)
	if !strings.Contains(got, "break") {
		t.Errorf("expected break in output, got %q", got)
	}
}

func TestFormat_Break_WithValue(t *testing.T) {
	// break with a value.
	src := `loop { n = 5 ->
  if n == 0 { break n }
  n - 1
}
`
	got, _ := Format(src)
	if !strings.Contains(got, "break n") {
		t.Errorf("expected 'break n' in output, got %q", got)
	}
}

func TestFormat_Continue(t *testing.T) {
	src := `Iterator.reduce(list) { total = 0, x ->
  if x < 0 { continue }
  total + x
}
`
	got, _ := Format(src)
	if !strings.Contains(got, "continue") {
		t.Errorf("expected continue in output, got %q", got)
	}
}

func TestFormat_Range_StructLit(t *testing.T) {
	// Range struct literal round-trip (still buildable manually; the
	// preferred form is the literal `0..10`).
	src := "x = Range{start: 0, end: Some(10), inclusive: False}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

// -----------------------------------------------------------------------------
// Imports
// -----------------------------------------------------------------------------

// TestFormat_Import_SlashOutput asserts the canonical formatter output for
// the `/`-separated module-path syntax. Selective imports render with a
// `.` boundary between the file path and declaration selector.
func TestFormat_Import_SlashOutput(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"slash output for stdlib path", "import std/io\n", "import std/io\n"},
		{"drill-through type stays dot", "import std/maybe.Maybe.{Some, None}\n", "import std/maybe.Maybe.{None, Some}\n"},
		{"single value selector stays path dotted", "import std/io: print\n", "import std/io.print\n"},
		{"single drill-through selector stays dotted", "import std/json: Json.Case.Camel\n", "import std/json.Json.Case.Camel\n"},
		{"dotted selector can sit inside owner braces", "import {\n    std/json: FromJson, ToJson, Json.{self, Case.Camel}\n}\n", "import {\n    std/json.{FromJson, ToJson}\n    std/json.Json.{self, Case.Camel}\n}\n"},
		{"snake_case child module stays slash path", "import app/date_time.{self, DateTime}\n", "import app/date_time.{self, DateTime}\n"},
		{"selective single name normalizes to dot selector", "import models.User\n", "import models.User\n"},
		{"user module nested", "import http/request: Request as Req\n", "import http/request.Request as Req\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := Format(tc.src)
			if got != tc.want {
				t.Errorf("Format(%q) =\n  got:  %q\n  want: %q", tc.src, got, tc.want)
			}
		})
	}
}

func TestFormat_Import_Simple(t *testing.T) {
	src := "import std/lists: List\n"
	want := "import std/lists.List\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Import_Selective(t *testing.T) {
	src := "import std/maybe.{None, Some}\n"
	want := "import std/maybe.{None, Some}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_Import_SelectiveAlias(t *testing.T) {
	// Selective list is sorted by pre-alias original name: None before Some.
	src := "import std/maybe.{None as Nothing, Some as Just}\n"
	want := "import std/maybe.{None as Nothing, Some as Just}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_Import_SelectiveMixed(t *testing.T) {
	// Sorted by pre-alias original name: empty, put, size.
	src := "import std/maps.{empty, put as put_kv, size as kv_size}\n"
	want := "import std/maps.{empty, put as put_kv, size as kv_size}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_Import_LongSelective_WrapsSelectorList(t *testing.T) {
	src := "import std/maps.{aaaaaaaaaaaaaaaaaa, bbbbbbbbbbbbbbbbbb, cccccccccccccccccc, dddddddddddddddddd, eeeeeeeeeeeeeeeeee}\n"
	got, _ := Format(src)
	want := `import std/maps.{
    aaaaaaaaaaaaaaaaaa,
    bbbbbbbbbbbbbbbbbb,
    cccccccccccccccccc,
    dddddddddddddddddd,
    eeeeeeeeeeeeeeeeee
}
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_SortsImports_Alphabetical(t *testing.T) {
	src := `import std/strings: String
import std/lists: List
import std/maps: Map
`
	// Distinct imports combine into one block, sorted by path.
	want := "import {\n    std/lists.List\n    std/maps.Map\n    std/strings.String\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_ImportSort_IgnoresNameAlias(t *testing.T) {
	// Sort by path, not alias; aliased imported names are ordinary block entries.
	src := `import std/lists: List as Z
import std/maps: Map as A
`
	want := "import {\n    std/lists.List as Z\n    std/maps.Map as A\n}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_ImportSort_SelectiveListByOriginalName(t *testing.T) {
	src := "import std/maybe.{Some as Just, None as Nothing}\n"
	want := "import std/maybe.{None as Nothing, Some as Just}\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_ImportSort_StopsAtNonImport(t *testing.T) {
	// Once a non-import is seen, trailing imports are left alone. The leading
	// run combines into a block; the trailing single import (after code) stays
	// bare and untouched.
	src := `import std/strings: String
import std/lists: List

x = 1

import std/maps: Map
`
	got, _ := Format(src)
	if !strings.HasPrefix(got, "import {\n    std/lists.List\n    std/strings.String\n}\n") {
		t.Errorf("expected leading run combined+sorted, got:\n%s", got)
	}
	if !strings.HasSuffix(got, "import std/maps.Map\n") {
		t.Errorf("expected trailing single import preserved, got:\n%s", got)
	}
}

// The destructure tests below use a single-statement block on the LHS, so the
// formatter may render the block inline (flat form) rather than broken. We
// only assert that the destructure itself appears in the output, decoupled
// from block-breaking decisions.

func TestFormat_TupleDestructure(t *testing.T) {
	src := `fn main() {
  (name, age) = get_user()
}
`
	got, _ := Format(src)
	if !strings.Contains(got, "(name, age) = get_user()") {
		t.Errorf("tuple destructure not emitted correctly, got:\n%s", got)
	}
}

func TestFormat_TupleDestructure_Wildcard(t *testing.T) {
	// Wildcard bindings in a tuple destructure round-trip as "_".
	src := `fn main() {
  (_, v) = pair
}
`
	got, _ := Format(src)
	if !strings.Contains(got, "(_, v) = pair") {
		t.Errorf("wildcard tuple destructure not emitted correctly, got:\n%s", got)
	}
}

func TestFormat_StructDestructure_Punning(t *testing.T) {
	// Anonymous struct destructure with punning: {x, y} = expr.
	src := `fn main() {
  {x, y} = point
}
`
	got, _ := Format(src)
	if !strings.Contains(got, "{x, y} = point") {
		t.Errorf("struct destructure with punning not emitted correctly, got:\n%s", got)
	}
}

func TestFormat_StructDestructure_WithRename(t *testing.T) {
	// Anonymous struct destructure with rename: {x: a, y: b} = expr.
	src := `fn main() {
  {x: a, y: b} = point
}
`
	got, _ := Format(src)
	if !strings.Contains(got, "{x: a, y: b} = point") {
		t.Errorf("struct destructure with rename not emitted correctly, got:\n%s", got)
	}
}

func TestFormat_DistinctDestructure(t *testing.T) {
	// Distinct type unwrap: TypeName(binding) = expr.
	src := `fn main() {
  Id(unwrapped) = id
}
`
	got, _ := Format(src)
	if !strings.Contains(got, "Id(unwrapped) = id") {
		t.Errorf("distinct destructure not emitted correctly, got:\n%s", got)
	}
}

func TestFormat_DistinctDestructure_Wildcard(t *testing.T) {
	// Distinct type destructure with wildcard binding.
	src := `fn main() {
  Id(_) = id
}
`
	got, _ := Format(src)
	if !strings.Contains(got, "Id(_) = id") {
		t.Errorf("wildcard distinct destructure not emitted correctly, got:\n%s", got)
	}
}

func TestFormat_MapDestructure(t *testing.T) {
	// Map destructure: {"key" => binding, ...} = expr.
	src := `fn main() {
  {"host" => host, "port" => port} = config
}
`
	got, _ := Format(src)
	if !strings.Contains(got, `{"host" => host, "port" => port} = config`) {
		t.Errorf("map destructure not emitted correctly, got:\n%s", got)
	}
}

// Map-distinct call-form pattern round-trips as `Kvs({"a" => v})`,
// preserving the inner parens that distinguish it from the flat
// `Kvs{"a" => v}` literal-attach form.
func TestFormat_MapPattern_CallForm_RoundTrip(t *testing.T) {
	src := `type Kvs Map<String, Int>
fn main() {
  kv = Kvs{"a" => 1}
  case kv {
    Kvs({"a" => v}) -> v
  }
}
`
	got, _ := Format(src)
	if !strings.Contains(got, `Kvs({"a" => v}) -> v`) {
		t.Errorf("call-form map pattern not preserved, got:\n%s", got)
	}
	// Idempotent: applying Format twice should match.
	twice, _ := Format(got)
	if got != twice {
		t.Errorf("non-idempotent.\n--- once ---\n%s\n--- twice ---\n%s", got, twice)
	}
}

func TestFormat_Trivia_LeadingCommentOnStatement(t *testing.T) {
	src := "// note\nx = 1\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Trivia_TrailingCommentOnStatement(t *testing.T) {
	src := "x = 1 // inline\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Trivia_BlankLineBetweenStatements(t *testing.T) {
	src := "x = 1\n\ny = 2\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Trivia_MultipleBlanksCollapseToOne(t *testing.T) {
	// Multiple blank lines in source collapse to one (this is done at the lexer).
	src := "x = 1\n\n\n\ny = 2\n"
	want := "x = 1\n\ny = 2\n"
	got, _ := Format(src)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormat_Trivia_LeadingCommentInsideBlock(t *testing.T) {
	src := `fn f() {
    // comment
    x = 1
    x
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Trivia_BlankLineInsideBlock(t *testing.T) {
	src := `fn f() {
    x = 1

    y = x + 1
    y
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Trivia_LeadingCommentOnCaseArm(t *testing.T) {
	src := `x = case y {
    // first arm
    0 -> "zero"
    _ -> "other"
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Trivia_DocCommentStillWorks(t *testing.T) {
	// The doc-comment path uses the FunctionDef.Doc field, separate from trivia.
	src := "/// adds one\nfn addone(x: Int): Int {\n    x + 1\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Lambda_MultiStmtBody_BreaksAfterArrow(t *testing.T) {
	// Early-exit idioms like `if n == 0 { break n }` stay inline under Option A
	// — `break <atomic>` counts as atomic.
	src := `f = loop { n = 5 ->
  if n == 0 { break n }
  n - 1
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

func TestFormat_Lambda_SingleStmtBody_StillFlat(t *testing.T) {
	// Single-stmt body: existing flat-or-broken logic stays unchanged.
	src := "f = { x -> x + 1 }\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Lambda_TwoStmtsAlwaysBroken(t *testing.T) {
	// Two statements in body — always breaks even if both fit.
	src := `f = { x ->
  y = x + 1
  y * 2
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q", got)
	}
}

func TestFormat_Lambda_SingleStmtBlockWithCommentStaysBlock(t *testing.T) {
	src := `fn main() {
    fake_side_effect = || {
        // Imagine an io.print side effect here
        Unit
    }
}
`
	got := formatOnce(t, src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

// TestFormat_AllTestPrograms_DoNotPanic formats every .nomi file in
// tests/ and stdlib/. A panic in any file fails that subtest; this is
// the canonical panic-sweep to confirm the formatter covers every AST node kind
// currently produced by the parser.
func TestFormat_AllTestPrograms_DoNotPanic(t *testing.T) {
	for _, f := range collectNomiFiles(t) {
		f := f
		t.Run(filepath.Base(filepath.Dir(f))+"/"+filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panicked on %s: %v", f, r)
				}
			}()
			_, _ = Format(string(src))
		})
	}
}

func TestFormat_Trivia_MidPipeComment(t *testing.T) {
	src := `x = 5
  |> double() // note on middle step
  |> add(1)
`
	got, _ := Format(src)
	if !strings.Contains(got, "|> double() // note on middle step") {
		t.Errorf("mid-pipe comment lost, got:\n%s", got)
	}
}

func TestFormat_Trivia_EndOfBlockComment(t *testing.T) {
	src := `fn f() {
  x = 1
  x
  // comment at end
}
`
	got, _ := Format(src)
	if !strings.Contains(got, "// comment at end") {
		t.Errorf("end-of-block comment lost, got:\n%s", got)
	}
}

func TestFormat_Trivia_EndOfCaseBlockComment(t *testing.T) {
	src := `x = case y {
  0 -> "zero"
  _ -> "other"
  // trailing case comment
}
`
	got, _ := Format(src)
	if !strings.Contains(got, "// trailing case comment") {
		t.Errorf("end-of-case comment lost, got:\n%s", got)
	}
}

func TestFormat_TraitBound_Single(t *testing.T) {
	src := `interface Showable { show(value: self): String }
fn identity<T>(x: T): T where T: Showable { x }
`
	got, _ := Format(src)
	if !strings.Contains(got, "where T: Showable") {
		t.Errorf("expected `where T: Showable` to round-trip, got:\n%s", got)
	}
}

func TestFormat_TraitBound_Multi(t *testing.T) {
	src := `interface Showable { show(value: self): String }
interface Tagged { tag(value: self): String }
fn identity<T>(x: T): T where T: Showable and Tagged { x }
`
	got, _ := Format(src)
	if !strings.Contains(got, "where T: Showable and Tagged") {
		t.Errorf("expected `where T: Showable and Tagged` to round-trip, got:\n%s", got)
	}
}

func TestFormat_TypeAlias_BoundAlias(t *testing.T) {
	src := `interface Showable { show(value: self): String }
interface Tagged { tag(value: self): String }
typealias ShowAndTag Showable and Tagged
`
	got, _ := Format(src)
	if !strings.Contains(got, "typealias ShowAndTag Showable and Tagged") {
		t.Errorf("expected `typealias ShowAndTag Showable and Tagged` to round-trip, got:\n%s", got)
	}
}

func TestFormat_TypeAlias_SingleType_Unchanged(t *testing.T) {
	src := `typealias Name String
`
	got, _ := Format(src)
	if !strings.Contains(got, "typealias Name String") {
		t.Errorf("expected single-type alias to round-trip unchanged, got:\n%s", got)
	}
}

// Canonical form: 2+ consecutive `import` statements combine into one sorted
// block (std/* first, then alphabetical).
func TestFormat_SeparateImports_Combine(t *testing.T) {
	src := `import std/maps: Map
import std/lists: List
fn main() { 0 }
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("expected parse to succeed, got: %v", err)
	}
	want := "import {\n    std/lists.List\n    std/maps.Map\n}\n"
	if !strings.Contains(got, want) {
		t.Errorf("expected one combined sorted block, got:\n%s", got)
	}
}

// A blank line between imports is formatting noise unless a comment rides on it:
// the formatter combines and sorts the whole leading import run.
func TestFormat_SeparateImports_BlankGroupsCollapse(t *testing.T) {
	src := `import std/maps: Map
import std/lists: List

import other: other
import models: Models

fn main() { 0 }
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("expected parse to succeed, got: %v", err)
	}
	want := "import {\n    std/lists.List\n    std/maps.Map\n    models.Models\n    other.other\n}\n"
	if !strings.Contains(got, want) {
		t.Errorf("expected one combined import block, got:\n%s", got)
	}
}

// A single import statement stays bare (no block).
func TestFormat_SingleImport_StaysBare(t *testing.T) {
	src := `import std/lists: List

fn main() { 0 }
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("expected parse to succeed, got: %v", err)
	}
	if !strings.Contains(got, "import std/lists.List\n") || strings.Contains(got, "import {") {
		t.Errorf("expected bare single import, got:\n%s", got)
	}
}

// Form 2: a user-written brace block is preserved as a block and always
// rendered multiline, newline-separated, with NO commas and no trailing
// comma — the analog of Go's `import ( ... )`.
func TestFormat_ImportBlock_AlwaysMultilineNoCommas(t *testing.T) {
	src := `import {
    std/maps: Map
    std/lists: List
}
`
	want := `import {
    std/lists.List
    std/maps.Map
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("expected parse to succeed, got: %v", err)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_GoPackageHandle(t *testing.T) {
	src := `gopkg "taggedffiapp" as ffi
`
	want := `gopkg "taggedffiapp" as ffi
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("expected parse to succeed, got: %v", err)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A short block that would have fit on one line still renders multiline —
// the old single-line collapse is gone.
func TestFormat_ImportBlock_NoSingleLineCollapse(t *testing.T) {
	src := `import {
  std/io
  std/lists.List
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("expected parse to succeed, got: %v", err)
	}
	if strings.Contains(got, "import { ") {
		t.Errorf("block must not collapse to a single line, got:\n%s", got)
	}
}

// A single-entry block collapses to the bare per-statement form.
func TestFormat_ImportBlock_SingleEntryCollapses(t *testing.T) {
	src := `import {
  std/lists: List
}

fn main() { 0 }
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("expected parse to succeed, got: %v", err)
	}
	if !strings.Contains(got, "import std/lists.List\n") || strings.Contains(got, "import {") {
		t.Errorf("expected single-entry block to collapse to bare, got:\n%s", got)
	}
}

// When a single-entry block collapses, its leading comment transfers to the
// surviving bare statement.
func TestFormat_ImportBlock_SingleEntryCollapse_PreservesLeadingComments(t *testing.T) {
	src := `// First comment line above the import.
// Second comment line above the import.
import {
  std/lists: List
}

fn main() { 0 }
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("expected parse to succeed, got: %v", err)
	}
	if !strings.Contains(got, "// First comment line above the import.\n") {
		t.Errorf("expected first leading comment preserved, got:\n%s", got)
	}
	if !strings.Contains(got, "// Second comment line above the import.\n") {
		t.Errorf("expected second leading comment preserved, got:\n%s", got)
	}
	if !strings.Contains(got, "import std/lists.List\n") {
		t.Errorf("expected single-entry block to collapse, got:\n%s", got)
	}
}

// Block entries are sorted (std/* first, then alphabetical) and rendered
// newline-separated.
func TestFormat_ImportBlock_SortsEntries(t *testing.T) {
	src := `import {
    log.{Logger}
    console.{Console}
    effects.{Clock}
    runtime/prod: Prod
    runtime/test: Test
    runtime/dev
}
`
	want := `import {
    console.Console
    effects.Clock
    log.Logger
    runtime/dev
    runtime/prod.Prod
    runtime/test.Test
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// std/* entries sort before non-std entries within a block.
func TestFormat_ImportBlock_StdGroupFirst(t *testing.T) {
	src := `import {
    log.{Logger}
    std/io.{inspect}
    console.{Console}
}
`
	want := `import {
    std/io.inspect
    console.Console
    log.Logger
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_ImportGroups_BlankLineCollapses(t *testing.T) {
	src := `import {
    std/context.Context
    std/duration.Duration
}

import {
    std/startup.Startup
    std/io
    app.EffectsApp
}

fn main() {
    Unit
}
`
	want := `import {
    std/context.Context
    std/duration.Duration
    std/io
    std/startup.Startup
    app.EffectsApp
}

fn main() {
    Unit
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_ImportGroups_CommentKeepsSection(t *testing.T) {
	src := `import std/io

// project imports
import app.EffectsApp

fn main() {
    Unit
}
`
	want := `import std/io

// project imports
import app.EffectsApp

fn main() {
    Unit
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A selective entry inside a block keeps its own comma-separated name list —
// the no-comma rule applies to the block's module list, not to selecting
// names out of a module.
func TestFormat_ImportBlock_SelectiveEntryKeepsCommas(t *testing.T) {
	src := `import {
  std/io
  std/maybe.Maybe.{Some, None}
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("expected parse to succeed, got: %v", err)
	}
	if !strings.Contains(got, "std/maybe.Maybe.{None, Some}") {
		t.Errorf("expected selective name list to keep commas (sorted), got:\n%s", got)
	}
}

func TestFormat_ImportBlock_LongSelectiveEntryWraps(t *testing.T) {
	src := `import {
    modules/math
    modules/models: Greeting, Point, User
    visibility_chain_public/api.{Token, make, make_token}
    visibility_chain_public/api.http.header.{self, canonical_name}
    visibility_chain_public/api.Widget.{self, label}
}
`
	want := `import {
    modules/math
    modules/models.{Greeting, Point, User}
    visibility_chain_public/api.{Token, make, make_token}
    visibility_chain_public/api.Widget.{self, label}
    visibility_chain_public/api/http/header.{self, canonical_name}
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("expected parse to succeed, got: %v", err)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_ImportBlock_OwnerSelfCanonical(t *testing.T) {
	src := `import {
    std/maybe: Maybe, Maybe.{Some, None}
}
`
	want := `import {
    std/maybe.Maybe
    std/maybe.Maybe.{None, Some}
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("expected parse to succeed, got: %v", err)
	}
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// The per-import selective list keeps commas and stays inline when it fits
// (it selects names out of one module — distinct from the block form).
func TestFormat_SelectiveList_KeepsCommasInline(t *testing.T) {
	src := "import std/maps.{empty, put, size}\n"
	want := "import std/maps.{empty, put, size}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("expected parse to succeed, got: %v", err)
	}
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Per-item and line-level re-export modifiers on imports (plan B.6)
// ---------------------------------------------------------------------------

func TestFormat_ImportExport_PerItem(t *testing.T) {
	src := "import some_mod.{Thing export}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if !strings.Contains(got, "Thing export") {
		t.Errorf("expected `Thing export` in output, got:\n%s", got)
	}
}

func TestFormat_ImportExport_PerItemRename(t *testing.T) {
	src := "import some_mod.{Thing export as Tng}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if !strings.Contains(got, "Thing export as Tng") {
		t.Errorf("expected `Thing export as Tng` in output, got:\n%s", got)
	}
}

func TestFormat_ImportExport_LocalAndExportRename(t *testing.T) {
	src := "import some_mod.{a as l export as p}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if !strings.Contains(got, "a as l export as p") {
		t.Errorf("expected `a as l export as p` in output, got:\n%s", got)
	}
}

func TestFormat_ImportExport_LineLevelShorthand(t *testing.T) {
	src := "import some_mod: a, b, c export\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	want := "import some_mod.{a, b, c} export\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestFormat_ImportExport_BraceLineLevelNormalizesToFlat(t *testing.T) {
	src := "import some_mod: {a, b, c} export\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	want := "import some_mod.{a, b, c} export\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// Round-trip stability: parse → format → parse should produce equivalent
// output. Once the formatter has normalized a form, re-formatting must be
// idempotent.
func TestFormat_ImportExport_RoundTripStable(t *testing.T) {
	forms := []string{
		"import x.{a export}\n",
		"import x.{a export as A}\n",
		"import x.{a as l export as p}\n",
		"import x: a, b export\n",
	}
	for _, src := range forms {
		out, err := Format(src)
		if err != nil {
			t.Fatalf("Format(%q) error: %v", src, err)
		}
		out2, err := Format(out)
		if err != nil {
			t.Fatalf("Format(%q) (second pass) error: %v", out, err)
		}
		if out != out2 {
			t.Errorf("format not idempotent on %q:\nfirst:  %q\nsecond: %q", src, out, out2)
		}
	}
}

// ---------------------------------------------------------------------------
// Inline `pub` and `opaque` modifiers (plan A.10)
// ---------------------------------------------------------------------------

func TestFormat_PubFn(t *testing.T) {
	src := "pub fn foo(x: Int): Int { x + 1 }\n"
	got, _ := Format(src)
	if !strings.Contains(got, "pub fn foo") {
		t.Errorf("expected `pub fn foo` in output, got:\n%s", got)
	}
}

func TestFormat_PubStruct(t *testing.T) {
	src := "pub struct User { name: String }\n"
	got, _ := Format(src)
	if !strings.Contains(got, "pub struct User") {
		t.Errorf("expected `pub struct User` in output, got:\n%s", got)
	}
}

func TestFormat_PubEnum(t *testing.T) {
	src := "pub enum Color { Red | Blue }\n"
	got, _ := Format(src)
	if !strings.Contains(got, "pub enum Color") {
		t.Errorf("expected `pub enum Color` in output, got:\n%s", got)
	}
}

func TestFormat_PubTypeDistinct(t *testing.T) {
	src := "pub type UserId Int\n"
	got, _ := Format(src)
	if !strings.Contains(got, "pub type UserId Int") {
		t.Errorf("expected `pub type UserId Int` in output, got:\n%s", got)
	}
}

func TestFormat_PubInterface(t *testing.T) {
	src := "pub interface Display { fn to_string(value: self): String }\n"
	got, _ := Format(src)
	if !strings.Contains(got, "pub interface Display") {
		t.Errorf("expected `pub interface Display` in output, got:\n%s", got)
	}
}

func TestFormat_PubTypealias(t *testing.T) {
	src := "pub typealias Id String\n"
	got, _ := Format(src)
	if !strings.Contains(got, "pub typealias Id String") {
		t.Errorf("expected `pub typealias Id String` in output, got:\n%s", got)
	}
}

func TestFormat_PubOnce(t *testing.T) {
	src := "pub once max_retries: Int = 3\n"
	got, _ := Format(src)
	if !strings.Contains(got, "pub once max_retries") {
		t.Errorf("expected `pub once max_retries` in output, got:\n%s", got)
	}
}

func TestFormat_PubExternFn(t *testing.T) {
	src := "pub host fn println(value: String)\n"
	got, _ := Format(src)
	if !strings.Contains(got, "pub host fn println") {
		t.Errorf("expected `pub host fn println` in output, got:\n%s", got)
	}
}

func TestFormat_PubExternType(t *testing.T) {
	src := "pub host type Regex\n"
	got, _ := Format(src)
	if !strings.Contains(got, "pub host type Regex") {
		t.Errorf("expected `pub host type Regex` in output, got:\n%s", got)
	}
}

func TestFormat_PubOpaqueType(t *testing.T) {
	src := "pub opaque type UserId Int\n"
	got, _ := Format(src)
	if !strings.Contains(got, "pub opaque type UserId") {
		t.Errorf("expected `pub opaque type UserId` in output, got:\n%s", got)
	}
}

func TestFormat_OpaqueTypePrivate(t *testing.T) {
	// Opaque without pub — private opaque type.
	src := "opaque type UserId Int\n"
	got, _ := Format(src)
	if !strings.Contains(got, "opaque type UserId") {
		t.Errorf("expected `opaque type UserId` in output, got:\n%s", got)
	}
	if strings.Contains(got, "pub opaque") {
		t.Errorf("did not expect `pub opaque` in output, got:\n%s", got)
	}
}

func TestFormat_OpaqueOnStructPreserved(t *testing.T) {
	// The combination is analyzer-rejected, but the formatter must preserve
	// what was written so the analyzer's diagnostic stays anchored at the
	// opaque keyword.
	src := "pub opaque struct Foo { x: Int }\n"
	got, _ := Format(src)
	if !strings.Contains(got, "pub opaque struct Foo") {
		t.Errorf("expected `pub opaque struct Foo` in output, got:\n%s", got)
	}
}

func TestFormat_OpaqueOnEnumPreserved(t *testing.T) {
	src := "pub opaque enum Color { Red | Blue }\n"
	got, _ := Format(src)
	if !strings.Contains(got, "pub opaque enum Color") {
		t.Errorf("expected `pub opaque enum Color` in output, got:\n%s", got)
	}
}

func TestFormat_OpaqueOnStructPrivatePreserved(t *testing.T) {
	src := "opaque struct Foo { x: Int }\n"
	got, _ := Format(src)
	if !strings.Contains(got, "opaque struct Foo") {
		t.Errorf("expected `opaque struct Foo` in output, got:\n%s", got)
	}
	if strings.Contains(got, "pub ") {
		t.Errorf("did not expect `pub ` in output, got:\n%s", got)
	}
}

// Round-trip an anonymous struct type used as a tuple element of a
// distinct-tuple type. The formatter delegates to TypeString(), so this
// pins the canonical shape `{name: String, age: Int}`.
func TestFormat_AnonStructType_TupleDistinct(t *testing.T) {
	src := "type Tagged (String, {name: String, age: Int})\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

// Round-trip an anonymous struct type as a function parameter annotation.
func TestFormat_AnonStructType_FunctionParam(t *testing.T) {
	src := "fn greet(p: {name: String}): String {\n    p.name\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

// TestFormat_AnonStructType_LongFnParam_Breaks: a long anon struct in a
// function parameter annotation breaks across lines with trailing commas
// per field. Previously emitTypeExpr always returned the AST's flat
// TypeString(), so the formatter could only break the surrounding param
// list around the anon struct — never inside it. Now the anon struct
// participates in the width budget like other braced bodies.
func TestFormat_AnonStructType_LongFnParam_Breaks(t *testing.T) {
	src := "fn process(_opts: {retries: Int, timeout: Int, on_error: (Error) -> Unit, max_concurrency: Int, backoff_ms: Int}): Result<Int, Error> {\n    Ok(0)\n}\n"
	got, _ := Format(src)
	if !strings.Contains(got, "opts: {\n") {
		t.Errorf("expected anon struct to break, got:\n%s", got)
	}
	if !strings.Contains(got, "\n        retries: Int,\n") {
		t.Errorf("expected indented field with trailing comma, got:\n%s", got)
	}
	if !strings.Contains(got, "\n        backoff_ms: Int,\n") {
		t.Errorf("expected last field also trailing-commaed, got:\n%s", got)
	}
}

// TestFormat_AnonStructType_ShortFnParam_StaysFlat: short anon structs in
// fn params still fit flat; the new rule is width-based, not always-broken.
// Acts as a regression guard against an over-aggressive break rule.
func TestFormat_AnonStructType_ShortFnParam_StaysFlat(t *testing.T) {
	src := "fn greet(p: {name: String, age: Int}): String {\n    p.name\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

// TestFormat_AnonStructType_NestedInTuple_Breaks: an anon struct nested
// inside a distinct-type tuple breaks under width pressure even though it
// is not at the top of its emitTypeExpr call. The tuple itself stays flat
// (no Group on the tuple type) and the anon struct breaks at its column.
// Mirrors Prettier's `Promise<{...}>` behaviour — outer container stays
// inline, inner anon struct flows multi-line inside it.
func TestFormat_AnonStructType_NestedInTuple_Breaks(t *testing.T) {
	src := "pub type MyTuple (String, {status: Int, message: String, retryable: Bool, a_long_field: String, another_field: Int})\n"
	want := `pub type MyTuple (String, {
    status: Int,
    message: String,
    retryable: Bool,
    a_long_field: String,
    another_field: Int,
})
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestFormat_AnonStructType_NestedInGeneric_Breaks: same rule for an anon
// struct inside a generic type argument like `List<{...}>` — the generic's
// `Name<` and `>` stay inline, the anon struct breaks inside. Uses a
// typealias rather than a fn to avoid an enclosing param-list Group whose
// own break decision would race with the anon struct's (Wadler's `fits`
// computes total flat width and would force the outer Group broken before
// the inner one even gets a chance to break alone).
func TestFormat_AnonStructType_NestedInGeneric_Breaks(t *testing.T) {
	src := "pub typealias Pages List<{status: Int, message: String, retryable: Bool, a_long_field: String, another_field: Int}>\n"
	want := `pub typealias Pages List<{
    status: Int,
    message: String,
    retryable: Bool,
    a_long_field: String,
    another_field: Int,
}>
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestFormat_AnonStructType_NestedInFuncType_Breaks: an anon struct inside
// a function type's param list. Unlike the tuple/generic cases (where the
// container stays inline and the inner anon breaks), the function-type
// param list itself wraps in a Group and breaks under width — that's the
// only way to bring the trailing `-> R` back inside the budget when the
// inner anon's flat form itself fits at its column but the whole signature
// still overflows. After the function type breaks, the anon at the inner
// indent fits flat and stays there. No trailing comma — function-type
// syntax does not permit it.
func TestFormat_AnonStructType_NestedInFuncType_Breaks(t *testing.T) {
	src := "pub typealias Handler ({req: String, headers: Map<String, String>, body: String}) -> Result<String, Int>\n"
	want := `pub typealias Handler (
    {req: String, headers: Map<String, String>, body: String}
) -> Result<String, Int>
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestFormat_TypeExpr_SimpleRoundTrip: simple type expressions
// (SimpleType, GenericType, FuncType, QualifiedType, SelfType) round-trip
// unchanged. Regression guard against the type-expr Doc rewrite breaking
// canonical flat output.
func TestFormat_TypeExpr_SimpleRoundTrip(t *testing.T) {
	cases := []string{
		"type Pair (Int, String)\n",
		"typealias Handler (String, String) -> Result<String, String>\n",
		"fn f(_xs: List<Int>): Int {\n    0\n}\n",
		"fn f(_m: Map<String, Int>): Int {\n    0\n}\n",
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			got, _ := Format(src)
			if got != src {
				t.Errorf("got:\n%s\nwant:\n%s", got, src)
			}
		})
	}
}

// --- Compound-pattern vertical-break rule -------------------------------
//
// A Map/List/struct-shaped pattern (or its construction-site counterpart)
// breaks vertically when (a) its single-line form exceeds 100 cols OR
// (b) the count of consecutive compound-pattern nesting reaches 3.
// Wrapped variants (`Some(x)`, `Ok(...)`, `String(s)`) do not contribute
// to the depth.

// Depth 1 — fits in width, no compound nesting: stays single-line.
func TestFormat_CompoundDepth_Depth1_StaysFlat(t *testing.T) {
	src := `fn main() {
    case x {
        Obj{"name" => String(n)} -> n
        _ -> "miss"
    }
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("depth-1 pattern broke unexpectedly.\ngot:\n%s\nwant:\n%s", got, src)
	}
}

// Depth 2 — fits in width, two compound levels: stays single-line.
func TestFormat_CompoundDepth_Depth2_FitsStaysFlat(t *testing.T) {
	src := `fn main() {
    case x {
        Obj{"data" => Obj{"k" => v}} -> v
        _ -> "miss"
    }
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("depth-2 pattern broke unexpectedly.\ngot:\n%s\nwant:\n%s", got, src)
	}
}

// Depth 3 — fits in width but the depth threshold fires anyway. Chain-
// aware: once the outermost Obj decides to break, every inner compound
// in the chain ALSO breaks regardless of its own depth-from-itself.
// Wrapper variants (Ok, String) propagate the break-decision to their
// payload without breaking themselves.
func TestFormat_CompoundDepth_Depth3_BreaksEvenWhenFits(t *testing.T) {
	src := `fn extract(x: Maybe<JV>): Maybe<String> {
    case x {
        Ok(Obj{"a" => Obj{"b" => Obj{"c" => String(v)}}}) -> Some(v)
        _ -> None
    }
}
`
	want := `fn extract(x: Maybe<JV>): Maybe<String> {
    case x {
        Ok(Obj{
            "a" => Obj{
                "b" => Obj{
                    "c" => String(v),
                },
            },
        }) -> Some(v)
        _ -> None
    }
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("depth-3 chain did not cascade.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

// Chain-aware cascade: a parent's break-decision propagates through
// every compound in the chain regardless of the inner's own
// depth-from-itself. Pinned explicitly because it's easy for a later
// refactor to silently drop the propagation and have only the outer
// compound break.
func TestFormat_CompoundDepth_ChainAware_PropagatesToChildren(t *testing.T) {
	// Five-deep Obj chain. Per-node, only the outermost (depth 5) would
	// fire; inner compounds (depths 4, 3, 2, 1) would each independently
	// stay flat. Chain-aware: all five break.
	src := `fn main() {
    case x {
        Obj{"a" => Obj{"b" => Obj{"c" => Obj{"d" => Obj{"e" => v}}}}} -> v
        _ -> 0
    }
}
`
	want := `fn main() {
    case x {
        Obj{
            "a" => Obj{
                "b" => Obj{
                    "c" => Obj{
                        "d" => Obj{
                            "e" => v,
                        },
                    },
                },
            },
        } -> v
        _ -> 0
    }
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("chain-aware cascade did not propagate to all compounds.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
	// Sanity: every Obj level must have its own break.
	wantObjBreaks := 5
	gotObjBreaks := strings.Count(got, "Obj{\n")
	if gotObjBreaks != wantObjBreaks {
		t.Errorf("expected %d Obj-open-then-newline pairs, got %d.\noutput:\n%s", wantObjBreaks, gotObjBreaks, got)
	}
}

// Width-driven break: a depth-2 compound whose single-line form exceeds
// 100 cols still breaks via the standard Group machinery.
func TestFormat_CompoundDepth_Depth2_WidthBreaks(t *testing.T) {
	// A single Map pattern at the case-arm indent (4 cols) with a payload
	// that pushes the line past 100. Depth = 2 (Obj + inner Obj), so the
	// depth rule alone wouldn't fire — width does the breaking here.
	src := `fn main() {
    case x {
        Obj{"some_long_field_name" => Obj{"other_field_with_a_longer_name" => String(some_long_binding_name)}} -> 1
        _ -> 0
    }
}
`
	got, _ := Format(src)
	if !strings.Contains(got, "Obj{\n            \"some_long_field_name\"") {
		t.Errorf("depth-2 over-width pattern did not break.\ngot:\n%s", got)
	}
}

// Wrapped variants don't bump depth — Some(Obj{...}) stays single-line
// because the only compound is the inner Obj (depth 1).
func TestFormat_CompoundDepth_Wrapped_DoesntBumpDepth(t *testing.T) {
	src := `fn main() {
    case x {
        Some(Obj{"k" => v}) -> v
        _ -> 0
    }
}
`
	got, _ := Format(src)
	if got != src {
		t.Errorf("Some(Obj{...}) wrapper bumped depth and broke unexpectedly.\ngot:\n%s\nwant:\n%s", got, src)
	}
}

// Construction-site analogue — Map literal with depth 3 breaks even when
// the single-line form would fit.
func TestFormat_CompoundDepth_MapLit_Depth3_Breaks(t *testing.T) {
	src := `fn main() {
    m = {"a" => {"b" => {"c" => 1}}}
}
`
	got, _ := Format(src)
	if !strings.Contains(got, "m = {\n        \"a\" => ") {
		t.Errorf("depth-3 nested map literal did not break.\ngot:\n%s", got)
	}
}

// Construction-site analogue — List literal with depth 3 (List of List
// of List) breaks even when single-line would fit. Chain-aware: every
// compound level breaks once the outer fires.
func TestFormat_CompoundDepth_ListLit_Depth3_Breaks(t *testing.T) {
	src := "fn main() {\n    xs = [[[1, 2]], [[3, 4]]]\n}\n"
	want := `fn main() {
    xs = [
        [
            [
                1,
                2,
            ],
        ],
        [
            [
                3,
                4,
            ],
        ],
    ]
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("depth-3 nested list literal cascade mismatch.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

// Idempotency — formatting twice produces the same output across the
// representative compound-pattern shapes above.
func TestFormat_CompoundDepth_Idempotent(t *testing.T) {
	srcs := []string{
		`fn main() {
  case x {
    Obj{"name" => String(n)} -> n
    _ -> "miss"
  }
}
`,
		`fn main() {
  case x {
    Obj{"data" => Obj{"k" => v}} -> v
    _ -> "miss"
  }
}
`,
		`fn extract(x: Maybe<JV>): Maybe<String> {
  case x {
    Ok(Obj{"a" => Obj{"b" => Obj{"c" => String(v)}}}) -> Some(v)
    _ -> None
  }
}
`,
		`fn main() {
  m = {"a" => {"b" => {"c" => 1}}}
}
`,
		"fn main() {\n  xs = [[[1, 2]], [[3, 4]]]\n}\n",
	}
	for i, src := range srcs {
		t.Run(strconvI(i), func(t *testing.T) {
			once, err := Format(src)
			if err != nil {
				t.Fatalf("first Format errored: %v", err)
			}
			twice, err := Format(once)
			if err != nil {
				t.Fatalf("second Format errored: %v", err)
			}
			if once != twice {
				t.Errorf("non-idempotent compound-pattern format.\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
			}
		})
	}
}

// TestFormat_TrailingTopLevelComments_Preserved guards against a regression
// where comments after the last top-level declaration (through EOF) were
// silently dropped — the parser collected them but had no AST node to
// attach them to, and the emitter never knew they existed. The fix:
// parser.ParseFile returns end-of-file trivia separately, and emitFile
// emits it after the last node.
func TestFormat_TrailingTopLevelComments_Preserved(t *testing.T) {
	src := `import std/io

fn main() {
    io.print("hello")
}

// Trailing top-level comment after all declarations.
// Second line of the trailing comment.
// Third line.
`
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("trailing comments dropped or rewritten\ngot:\n%s\nwant:\n%s", got, src)
	}
	// Idempotency — format twice produces the same output.
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%s\ntwice:\n%s", got, twice)
	}
}

// TestFormat_TrailingTopLevelComment_SingleLine covers the one-line variant
// (no preceding blank line, single comment).
func TestFormat_TrailingTopLevelComment_SingleLine(t *testing.T) {
	src := "fn main() {}\n// the only trailing comment\n"
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

// TestFormat_TrailingTopLevelComment_OnlyComments covers a file whose
// entire contents are comments (no declarations at all). The trivia-only
// path is its own branch in emitFile.
func TestFormat_TrailingTopLevelComment_OnlyComments(t *testing.T) {
	src := "// just a comment\n// another\n"
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

// TestFormat_TrailingTopLevelComment_BlankBeforeComment verifies the blank
// line between the last decl and the trailing-comment block is preserved.
func TestFormat_TrailingTopLevelComment_BlankBeforeComment(t *testing.T) {
	src := "x = 1\n\n// trailing\n"
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

// TestFormat_TrailingTopLevelComment_NoBlankBeforeComment verifies the
// no-blank-line form is also preserved (parser doesn't synthesize an
// extra blank-line trivia when none existed).
func TestFormat_TrailingTopLevelComment_NoBlankBeforeComment(t *testing.T) {
	src := "x = 1\n// trailing\n"
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

// TestFormat_TrailingCommentInsideFuncBody_Preserved checks that the
// already-working in-block trailing-comment path keeps working (a sibling
// of the top-level fix; verifies the block emitter's endTrivia handling
// isn't disturbed).
func TestFormat_TrailingCommentInsideFuncBody_Preserved(t *testing.T) {
	src := `fn f() {
    x = 1
    x
    // trailing inside body
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

// TestFormat_TrailingCommentAfterLastCaseArm_Preserved checks that
// trailing trivia after the last case arm survives — verifies the existing
// Case end-trivia path keeps working.
func TestFormat_TrailingCommentAfterLastCaseArm_Preserved(t *testing.T) {
	src := `fn main() {
    x = case [1, 2] {
        [] -> "empty"
        _ -> "full"
        // trailing comment after last arm
    }
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("got %q\nwant %q", got, src)
	}
}

// TestFormat_StructBody_TrailingComment_Preserved verifies a `// comment`
// between the last struct field and the closing `}` survives a format pass
// and is idempotent. Pre-fix: parser rejected the COMMENT token at the
// top of the field-parse loop ("expected field name in struct definition"),
// so `nomi fmt -w` errored out rather than silently dropping — but the
// user could never include the comment in the first place.
func TestFormat_StructBody_TrailingComment_Preserved(t *testing.T) {
	src := "pub struct Point {\n    x: Int\n    y: Int\n    // trailing comment inside struct body\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("trailing comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// TestFormat_EnumBody_TrailingComment_Preserved mirrors the struct test for
// enums. Pre-fix: parser rejected with "expected `|` between enum variants".
func TestFormat_EnumBody_TrailingComment_Preserved(t *testing.T) {
	src := "pub enum Color {\n    Red\n    Green\n    Blue\n    // trailing comment\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("trailing comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// TestFormat_StructShapedVariant_TrailingComment_Preserved covers
// `Variant { fields..., // comment }` — a struct-shaped enum variant
// body. Pre-fix: parser rejected with "expected field name in struct
// variant".
func TestFormat_StructShapedVariant_TrailingComment_Preserved(t *testing.T) {
	src := "pub enum Shape {\n    Circle Float\n    Rect {\n        width: Float,\n        height: Float,\n        // trailing comment\n    }\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("trailing comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// TestFormat_InterfaceBody_TrailingComment_Preserved covers interface
// bodies. Pre-fix: parser silently swallowed the comment via
// skipNewlines — `nomi fmt -w` dropped the user's note (data loss).
func TestFormat_InterfaceBody_TrailingComment_Preserved(t *testing.T) {
	src := "pub interface Speech {\n    fn speak(s: Int): Int\n    // trailing comment\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("trailing comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// TestFormat_AnonStructType_TrailingComment_Preserved covers anonymous
// struct types in parameter position. Pre-fix: separator loop silently
// consumed COMMENT/BLANK_LINE — `nomi fmt -w` dropped the comment.
func TestFormat_AnonStructType_TrailingComment_Preserved(t *testing.T) {
	src := "fn greet(p: {\n    name: String,\n    age: Int,\n    // trailing comment\n}): String {\n    p.name\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("trailing comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// TestFormat_StructLit_TrailingComment_Preserved covers nominal struct
// literals in expression position. Pre-fix: separator loop's skipNewlines
// silently dropped the COMMENT.
func TestFormat_StructLit_TrailingComment_Preserved(t *testing.T) {
	src := "fn main() {\n    p = Point{\n        x: 3,\n        y: 4,\n        // trailing comment\n    }\n\n    p\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("trailing comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// TestFormat_AnonStructLit_TrailingComment_Preserved covers anonymous
// struct literals. Pre-fix: parser rejected with "expected field name in
// struct literal".
func TestFormat_AnonStructLit_TrailingComment_Preserved(t *testing.T) {
	src := "fn main() {\n    p = {\n        name: \"alice\",\n        age: 30,\n        // trailing comment\n    }\n\n    p\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("trailing comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// TestFormat_MapLit_TrailingComment_Preserved covers map literals.
// Pre-fix: parser rejected the COMMENT before `}` with "expected `}` to
// close map literal" — users couldn't write a map literal with a
// trailing in-body comment at all.
func TestFormat_MapLit_TrailingComment_Preserved(t *testing.T) {
	src := "fn main() {\n    m = {\n        \"a\" => 1,\n        \"b\" => 2,\n        // trailing comment\n    }\n\n    m\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("trailing comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// TestFormat_ListLit_TrailingComment_Preserved covers list literals.
// Pre-fix: parser silently dropped the COMMENT via post-element
// skipNewlines — real data-loss bug, same severity as the previous
// silent-drop fixes.
func TestFormat_ListLit_TrailingComment_Preserved(t *testing.T) {
	src := "fn main() {\n    l = [\n        1,\n        2,\n        // trailing comment\n    ]\n\n    l\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("trailing comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// TestFormat_MapPattern_TrailingComment_Preserved covers map patterns
// inside a case branch. Pre-fix: same parse error as MapLit (shared
// parseMapPatternEntries code path).
func TestFormat_MapPattern_TrailingComment_Preserved(t *testing.T) {
	src := "fn main() {\n    m = {\"a\" => 1}\n\n    case m {\n        {\n            \"a\" => x,\n            // trailing comment\n        } -> x\n        _ -> 0\n    }\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("trailing comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// TestFormat_ListPattern_TrailingComment_Preserved covers list patterns
// inside a case branch. Pre-fix: silent data-loss same as ListLit.
func TestFormat_ListPattern_TrailingComment_Preserved(t *testing.T) {
	src := "fn main() {\n    l = [1, 2]\n\n    case l {\n        [\n            a,\n            b,\n            // trailing comment\n        ] -> a + b\n        _ -> 0\n    }\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("trailing comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// Inter-field comment round-trips through StructDef and is idempotent.
// Pre-fix: parser rejected with "expected field name in struct
// definition". This commit adds LeadingComments to StructField.
func TestFormat_StructBody_InterFieldComment_Preserved(t *testing.T) {
	src := "pub struct Point {\n    x: Int\n    // a comment between two fields\n    y: Int\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("inter-field comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// Inter-variant comment round-trips through EnumDef. Pre-fix: rejected
// with "expected `|` between enum variants". LeadingComments on the
// next variant carries the comment; emitEnumDeclVariant puts it above
// the `variant` row at the body indent.
func TestFormat_EnumBody_InterVariantComment_Preserved(t *testing.T) {
	src := "pub enum Color {\n    Red\n    // a comment between two variants\n    Green\n    Blue\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("inter-variant comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// Inter-method comment round-trips through InterfaceDef. Pre-fix:
// silent data-loss via skipNewlines. Uses InterfaceMethod's existing
// TriviaCarrier.Leading slot.
func TestFormat_InterfaceBody_InterMethodComment_Preserved(t *testing.T) {
	src := "pub interface Speech {\n    fn speak(s: Int): Int\n    // a comment between two methods\n    fn shout(s: Int): Int\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("inter-method comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// Inter-field comment round-trips through interface body's `field`
// requirements. New InterfaceField.LeadingComments slot.
func TestFormat_InterfaceBody_InterFieldComment_Preserved(t *testing.T) {
	src := "pub interface Boxed {\n    field width: Int\n    // a comment between two fields\n    field height: Int\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("inter-field comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// Inter-field comment round-trips through anon-struct types in param
// position. New StructField.LeadingComments slot.
func TestFormat_AnonStructType_InterFieldComment_Preserved(t *testing.T) {
	src := "fn greet(p: {\n    name: String,\n    // a comment between two anon-type fields\n    age: Int,\n}): String {\n    p.name\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("inter-field comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// Inter-field comment round-trips through nominal struct literals.
// New StructFieldVal.LeadingComments slot. A leading comment on any
// field forces the broken (multi-line) form.
func TestFormat_StructLit_InterFieldComment_Preserved(t *testing.T) {
	src := "fn main() {\n    p = Point{\n        x: 3,\n        // a comment between two literal fields\n        y: 4,\n    }\n\n    p\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("inter-field comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// Inter-field comment round-trips through anonymous struct literals.
// Same StructFieldVal.LeadingComments slot (StructLit is the shared
// AST node for nominal and anonymous literals).
func TestFormat_AnonStructLit_InterFieldComment_Preserved(t *testing.T) {
	src := "fn main() {\n    p = {\n        name: \"alice\",\n        // a comment between two anon-literal fields\n        age: 30,\n    }\n\n    p\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("inter-field comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

// Inter-field comment round-trips through struct-shaped enum variants.
// Uses StructField.LeadingComments inside parseEnumVariant's struct
// payload arm.
func TestFormat_StructShapedVariant_InterFieldComment_Preserved(t *testing.T) {
	src := "pub enum Shape {\n    Rect {\n        width: Float,\n        // a comment between fields\n        height: Float,\n    }\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("inter-field comment dropped or reflowed\ngot:\n%q\nwant:\n%q", got, src)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%q\ntwice:\n%q", got, twice)
	}
}

func TestFormat_GroupedExpression_PreservesCompoundIntent(t *testing.T) {
	src := "fn main() {\n    assert (parse_id(\"42\") == Ok(42) and a) or True\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != src {
		t.Errorf("grouped compound expression changed\ngot:\n%s\nwant:\n%s", got, src)
	}
}

func TestFormat_GroupedExpression_DropsAtomicParens(t *testing.T) {
	src := "fn main() {\n    x = (foo())\n    y = (name)\n}\n"
	want := "fn main() {\n    x = foo()\n    y = name\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != want {
		t.Errorf("atomic grouping not canonicalized\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_GroupedExpression_CollapsesDuplicateParens(t *testing.T) {
	src := "fn main() {\n    assert ((a and b)) or c\n}\n"
	want := "fn main() {\n    assert (a and b) or c\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != want {
		t.Errorf("duplicate grouping not collapsed\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_PipeKeywordStage_IfIndentsBlockInsideStage(t *testing.T) {
	src := `fn main(): String {
    "Ada"
    |> if strings.contains?("A") {
        "initialed"
    } else {
        "plain"
    }
    |> dbg
}
`
	want := `fn main(): String {
    "Ada"
    |> if strings.contains?("A") {
            "initialed"
        } else {
            "plain"
        }
    |> dbg
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != want {
		t.Errorf("pipe if stage not indented as nested stage\ngot:\n%s\nwant:\n%s", got, want)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatalf("second format failed: %v", err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%s\ntwice:\n%s", got, twice)
	}
}

func TestFormat_PipeKeywordStage_CaseIndentsArmsInsideStage(t *testing.T) {
	src := `fn main(): String {
    "Ada"
    |> case {
        "Ada" -> "found"
        _ -> "plain"
    }
}
`
	want := `fn main(): String {
    "Ada"
    |> case {
            "Ada" -> "found"
            _ -> "plain"
        }
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}
	if got != want {
		t.Errorf("pipe case stage not indented as nested stage\ngot:\n%s\nwant:\n%s", got, want)
	}
	twice, err := Format(got)
	if err != nil {
		t.Fatalf("second format failed: %v", err)
	}
	if twice != got {
		t.Errorf("non-idempotent\nonce:\n%s\ntwice:\n%s", got, twice)
	}
}

// strconvI is a tiny local helper to avoid pulling strconv into the test
// file just for one decimal-int formatting.
func strconvI(i int) string {
	if i == 0 {
		return "0"
	}
	var out []byte
	for i > 0 {
		out = append([]byte{byte('0' + i%10)}, out...)
		i /= 10
	}
	return string(out)
}
