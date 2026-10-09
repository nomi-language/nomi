package vmhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// literalDiagnostics loads src and answers the diagnostics its load fails
// with, failing the test when it loads.
func literalDiagnostics(t *testing.T, src string) Diagnostics {
	t.Helper()
	_, err := LoadSource("main", src)
	if err == nil {
		t.Fatalf("the program loads; want a compile error:\n%s", src)
	}
	var ds Diagnostics
	if !errors.As(err, &ds) {
		t.Fatalf("the load failed with %T %v; want diagnostics", err, err)
	}
	return ds
}

// A backtick Regex literal is a Regex: bound with an annotation, in a
// `once`, passed to String.find_all. Double-quoted ones, with and without
// interpolation, are still Results.
func TestBacktickLiteral_IsItsHandlersOkValue(t *testing.T) {
	const src = "import {\n" +
		"    std/io\n" +
		"    std/regex.Regex\n" +
		"}\n\n" +
		"once letters = Regex`[A-Za-z]+`\n\n" +
		"fn main() {\n" +
		"    digits: Regex = Regex`\\d+`\n" +
		"    io.print(String.find_all(\"order 66, aisle 7\", digits))\n" +
		"    io.print(String.find_all(\"order 66, aisle 7\", letters))\n" +
		"    io.print(String.find_all(\"banana\", Regex`a.`))\n" +
		"    plain: Result<Regex, String> = Regex\"\\\\d\"\n" +
		"    word = \"an\"\n" +
		"    spliced: Result<Regex, String> = Regex\"${word}+\"\n" +
		"    bad: Result<Regex, String> = Regex\"[${word}\"\n" +
		"    io.print(Debug.inspect(plain))\n" +
		"    io.print(Debug.inspect(spliced))\n" +
		"    io.print(Result.with_default(Result.map(bad, Regex.pattern), \"invalid at run time\"))\n" +
		"}\n"
	var out strings.Builder
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := "[66, 7]\n[order, aisle]\n[an, an]\nOk(Regex`\\d`)\nOk(Regex`an+`)\ninvalid at run time\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// An invalid backtick pattern is a compile error at the literal carrying the
// regex error, from `nomi run`'s load and from `nomi check` alike.
func TestBacktickLiteral_InvalidPatternIsACompileError(t *testing.T) {
	const src = "import std/regex.Regex\n\nfn main() {\n    _ = Regex`a(`\n}\n"
	ds := literalDiagnostics(t, src)
	if len(ds) != 1 {
		t.Fatalf("%d diagnostics, want 1: %v", len(ds), ds)
	}
	d := ds[0]
	if want := "typed literal Regex`a(` is invalid: error parsing regexp: missing closing ): `a(`"; d.Message != want {
		t.Errorf("message = %q, want %q", d.Message, want)
	}
	if d.Line != 4 || d.Col != 9 || d.EndLine != 4 || d.EndCol != 18 {
		t.Errorf("span = %d:%d-%d:%d, want 4:9-4:18", d.Line, d.Col, d.EndLine, d.EndCol)
	}
	if d.Code != InvalidLiteralCode {
		t.Errorf("code = %q, want %q", d.Code, InvalidLiteralCode)
	}
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Check(path)
	if err == nil || !strings.Contains(err.Error(), "main.nomi:4:9: typed literal Regex`a(` is invalid") {
		t.Fatalf("nomi check answered %v; want the literal's error", err)
	}
}

// An Err that is not a String is shown through its Display impl, as a
// failed main's error is.
func TestBacktickLiteral_ErrorIsShownThroughDisplay(t *testing.T) {
	ds := literalDiagnostics(t, "import std/calendar.Date\n\nfn main() {\n    _ = Date`2026-13-04`\n}\n")
	if len(ds) != 1 || ds[0].Message != "typed literal Date`2026-13-04` is invalid: invalid value: month out of range: 13" {
		t.Fatalf("diagnostics = %v", ds)
	}
}

// A handler that reaches an effect cannot run at compile time, so a
// backtick literal of it is an error saying so, pointing at double quotes.
// The double-quoted literal of the same handler loads.
func TestBacktickLiteral_EffectfulHandlerIsACompileError(t *testing.T) {
	const decls = "import {\n    std/io\n    std/literals.{Fragment, Literal}\n}\n\n" +
		"struct Loud {\n    n: Int\n}\n\n" +
		"impl Literal for Loud {\n" +
		"    fn from_fragments(fragments: List<Fragment<String>>): Result<Loud, String> {\n" +
		"        io.print(\"building\")\n" +
		"        Ok(Loud{n: Iter.count(fragments)})\n" +
		"    }\n" +
		"}\n\n"
	ds := literalDiagnostics(t, decls+"fn main() {\n    _ = Loud`x`\n}\n")
	if len(ds) != 1 {
		t.Fatalf("%d diagnostics, want 1: %v", len(ds), ds)
	}
	if want := "typed literal Loud`x` cannot be checked at compile time: its handler has effects (Loud.from_fragments calls io.print)"; ds[0].Message != want {
		t.Errorf("message = %q, want %q", ds[0].Message, want)
	}
	if len(ds[0].Hints) != 1 || !strings.Contains(ds[0].Hints[0], "write it with double quotes") {
		t.Errorf("hints = %q", ds[0].Hints)
	}
	if _, err := LoadSource("main", decls+"fn main() {\n    _ = Loud\"x\"\n}\n"); err != nil {
		t.Fatalf("the double-quoted literal does not load: %v", err)
	}
}

// `try` on a backtick literal is the checker's "try requires a Result or
// Maybe" error, with the hint.
func TestBacktickLiteral_TryIsRejected(t *testing.T) {
	ds := literalDiagnostics(t, "import std/regex.Regex\n\nfn main(): Result<Unit, String> {\n    _ = try Regex`\\d+`\n    Ok(Unit)\n}\n")
	if len(ds) != 1 || ds[0].Message != "try requires a Result or Maybe, got Regex" {
		t.Fatalf("diagnostics = %v", ds)
	}
	if len(ds[0].Hints) != 1 || !strings.Contains(ds[0].Hints[0], "checked at compile time") {
		t.Errorf("hints = %q", ds[0].Hints)
	}
}

// A backtick literal's handler runs once per literal site, not once per
// evaluation: a literal in a loop reads its cell. Counted in VM steps (calls
// and backward branches): the handler costs about 1,000, and the loop runs
// 100 times, so rebuilding the literal each time would take over 100,000.
// The double-quoted literal, which is not cached, shows the limit tells the
// two apart.
func TestBacktickLiteral_InALoopIsBuiltOnce(t *testing.T) {
	const src = "import std/literals.{Fragment, Literal}\n\n" +
		"struct Slow {\n    n: Int\n}\n\n" +
		"impl Literal for Slow {\n" +
		"    fn from_fragments(fragments: List<Fragment<String>>): Result<Slow, String> {\n" +
		"        n = Iter.reduce(1..=1000, |acc = 0, i| acc + i)\n" +
		"        Ok(Slow{n: n + Iter.count(fragments)})\n" +
		"    }\n" +
		"}\n\n" +
		"fn raw(): Int {\n" +
		"    Iter.reduce(1..=100, |acc = 0, _i| acc + Slow`x`.n)\n" +
		"}\n\n" +
		"fn quoted(): Int {\n" +
		"    Iter.reduce(1..=100, |acc = 0, _i| acc + Result.with_default(Result.map(Slow\"x\", |s| s.n), 0))\n" +
		"}\n\n" +
		"fn main() {\n    _ = raw() + quoted()\n}\n"
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	const steps = 20_000
	v, err := p.Evaluate(context.Background(), "raw", EvalLimits{Steps: steps})
	if err != nil || v != int64(100*500501) {
		t.Fatalf("raw() = %v, %v; want %d within %d steps", v, err, 100*500501, steps)
	}
	if _, err := p.Evaluate(context.Background(), "quoted", EvalLimits{Steps: steps}); !errors.Is(err, ErrEvalLimit) {
		t.Fatalf("quoted() answered %v within %d steps; want ErrEvalLimit, or the limit cannot tell a rebuilt literal from a cached one", err, steps)
	}
}
