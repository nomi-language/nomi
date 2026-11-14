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

// A Regex literal without slots is a Regex in every quote style: bound with
// an annotation, in a `once`, passed to String.find_all. Ones with a slot
// are still Results.
func TestLiteralWithoutSlots_IsItsHandlersOkValue(t *testing.T) {
	const src = "import\n" +
		"    std::io\n" +
		"    std::regex.Regex\n" +
		"end\n\n" +
		"once letters = Regex`[A-Za-z]+`\n\n" +
		"fn main()\n" +
		"    digits: Regex<Unit> = Regex`\\d+`\n" +
		"    io.print(String.find_all(\"order 66, aisle 7\", digits))\n" +
		"    io.print(String.find_all(\"order 66, aisle 7\", letters))\n" +
		"    io.print(String.find_all(\"banana\", Regex`a.`))\n" +
		"    plain: Regex<Unit> = Regex\"\\\\d\"\n" +
		"    triple: Regex<Unit> = Regex\"\"\"\n        \\w\n        \"\"\"\n" +
		"    word = \"an\"\n" +
		"    spliced: Result<Regex<List<Maybe<String>>>, String> = Regex\"${word}+\"\n" +
		"    bad: Result<Regex<List<Maybe<String>>>, String> = Regex\"[${word}\"\n" +
		"    io.print(Debug.inspect(plain))\n" +
		"    io.print(Debug.inspect(triple))\n" +
		"    io.print(Debug.inspect(spliced))\n" +
		"    io.print(Result.with_default(Result.map(bad, Regex.pattern), \"invalid at run time\"))\n" +
		"end\n"
	var out strings.Builder
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := "[66, 7]\n[order, aisle]\n[an, an]\nRegex`\\d`\nRegex`\\w`\nOk(Regex\"an+\")\ninvalid at run time\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// An invalid backtick pattern is a compile error at the literal carrying the
// regex error, from `nomi run`'s load and from `nomi check` alike.
func TestBacktickLiteral_InvalidPatternIsACompileError(t *testing.T) {
	const src = "import std::regex.Regex\n\nfn main()\n    _ = Regex`a(`\nend\n"
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
// failed main's error is. A quoted literal without slots is checked as a
// backtick one is, and a long one is named by its tag and quotes.
func TestLiteralWithoutSlots_ErrorIsShownThroughDisplay(t *testing.T) {
	for lit, want := range map[string]string{
		"Date`2026-13-04`":   "typed literal Date`2026-13-04` is invalid: invalid value: month out of range: 13",
		"Date\"2026-13-04\"": "typed literal Date\"2026-13-04\" is invalid: invalid value: month out of range: 13",
		"Date\"\"\"\n        2026-13-04\n        \"\"\"": "typed literal Date\"\"\"…\"\"\" is invalid: invalid value: month out of range: 13",
	} {
		ds := literalDiagnostics(t, "import std::calendar.Date\n\nfn main()\n    _ = "+lit+"\nend\n")
		if len(ds) != 1 || ds[0].Message != want {
			t.Errorf("%s: diagnostics = %v, want %q", lit, ds, want)
		}
	}
}

// A handler that reaches an effect cannot run at compile time, so a literal
// of it without slots, in either quote style, is an error saying so, pointing
// at the type's own functions. A literal of the same handler with a slot
// loads.
func TestLiteralWithoutSlots_EffectfulHandlerIsACompileError(t *testing.T) {
	const decls = "import\n    std::io\n    std::literals.{Fragment, Literal}\nend\n\n" +
		"struct Loud\n    n: Int\nend\n\n" +
		"impl Literal for Loud\n" +
		"    fn from_fragments(fragments: List<Fragment<String>>) -> Result<Loud, String>\n" +
		"        io.print(\"building\")\n" +
		"        Ok(Loud{n: Iter.count(fragments)})\n" +
		"    end\n" +
		"end\n\n"
	for _, lit := range []string{"Loud`x`", "Loud\"x\""} {
		ds := literalDiagnostics(t, decls+"fn main()\n    _ = "+lit+"\nend\n")
		if len(ds) != 1 {
			t.Fatalf("%s: %d diagnostics, want 1: %v", lit, len(ds), ds)
		}
		if want := "typed literal " + lit + " cannot be checked at compile time: its handler has effects (Loud.from_fragments calls io.print)"; ds[0].Message != want {
			t.Errorf("message = %q, want %q", ds[0].Message, want)
		}
		if want := "call one of `Loud`'s functions instead"; len(ds[0].Hints) != 1 || !strings.Contains(ds[0].Hints[0], want) {
			t.Errorf("hints = %q, want one naming %q", ds[0].Hints, want)
		}
	}
	if _, err := LoadSource("main", decls+"fn main()\n    s = \"x\"\n    _ = Loud\"${s}\"\nend\n"); err != nil {
		t.Fatalf("the literal with a slot does not load: %v", err)
	}
}

// `?` on a literal without slots is the checker's "try requires a Result or
// Maybe" error, with the hint.
func TestLiteralWithoutSlots_TryIsRejected(t *testing.T) {
	ds := literalDiagnostics(t, "import std::regex.Regex\n\nfn main() -> Result<Unit, String>\n    _ = Regex`\\d+`?\n    Ok(Unit)\nend\n")
	if len(ds) != 1 || ds[0].Message != "`?` requires a Result or Maybe, got Regex<Unit>" {
		t.Fatalf("diagnostics = %v", ds)
	}
	if len(ds[0].Hints) != 1 || !strings.Contains(ds[0].Hints[0], "checked at compile time") {
		t.Errorf("hints = %q", ds[0].Hints)
	}
}

// A literal without slots runs its handler once per literal site, not once
// per evaluation: a literal in a loop reads its cell, in either quote style.
// Counted in VM steps (calls and backward branches): the handler costs about
// 1,000, and the loop runs 100 times, so rebuilding the literal each time
// would take over 100,000. The literal with a slot, which is not cached,
// shows the limit tells the two apart.
func TestLiteralWithoutSlots_InALoopIsBuiltOnce(t *testing.T) {
	const src = "import std::literals.{Fragment, Literal}\n\n" +
		"struct Slow\n    n: Int\nend\n\n" +
		"impl Literal for Slow\n" +
		"    fn from_fragments(fragments: List<Fragment<String>>) -> Result<Slow, String>\n" +
		"        n = Iter.reduce(1..=1000, 0, fn acc, i -> acc + i)\n" +
		"        Ok(Slow{n: n + Iter.count(fragments)})\n" +
		"    end\n" +
		"end\n\n" +
		"fn raw() -> Int\n" +
		"    Iter.reduce(1..=100, 0, fn acc, _i -> acc + Slow`x`.n)\n" +
		"end\n\n" +
		"fn quoted() -> Int\n" +
		"    Iter.reduce(1..=100, 0, fn acc, _i -> acc + Slow\"x\".n)\n" +
		"end\n\n" +
		"fn slotted() -> Int\n" +
		"    s = \"x\"\n" +
		"    Iter.reduce(1..=100, 0, fn acc, _i -> acc + Result.with_default(Result.map(Slow\"${s}\", fn v -> v.n), 0))\n" +
		"end\n\n" +
		"fn main()\n    _ = raw() + quoted() + slotted()\nend\n"
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	const steps = 20_000
	v, err := p.Evaluate(context.Background(), "raw", EvalLimits{Steps: steps})
	if err != nil || v != int64(100*500501) {
		t.Fatalf("raw() = %v, %v; want %d within %d steps", v, err, 100*500501, steps)
	}
	if v, err := p.Evaluate(context.Background(), "quoted", EvalLimits{Steps: steps}); err != nil || v != int64(100*500501) {
		t.Fatalf("quoted() = %v, %v; want %d within %d steps", v, err, 100*500501, steps)
	}
	if _, err := p.Evaluate(context.Background(), "slotted", EvalLimits{Steps: steps}); !errors.Is(err, ErrEvalLimit) {
		t.Fatalf("slotted() answered %v within %d steps; want ErrEvalLimit, or the limit cannot tell a rebuilt literal from a cached one", err, steps)
	}
}

// A handler that cannot fail runs at compile time too: its literal keeps the
// handler's type, and a handler that traps or reaches an effect makes a
// literal without slots a compile error, while one with a slot loads.
func TestLiteralWithoutSlots_InfallibleHandlerRunsAtCompileTime(t *testing.T) {
	handler := func(body string) string {
		return "import\n    std::io\n    std::literals.{Fragment, Literal}\nend\n\n" +
			"struct Word\n    text: String\nend\n\n" +
			"impl Literal for Word\n" +
			"    fn from_fragments(fragments: List<Fragment<String>>) -> Word\n" +
			body +
			"    end\nend\n\n"
	}
	ok := handler("        Word{text: Iter.reduce(fragments, \"\", fn acc, f -> case f\n            .Static(s) -> acc + s\n            .Dynamic(s) -> acc + s\n        end)}\n")
	p, err := LoadSource("main", ok+"fn main()\n    w: Word = Word\"hi\"\n    io.print(w.text)\nend\n")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil || out.String() != "hi\n" {
		t.Fatalf("run = %q, %v; want hi", out.String(), err)
	}
	traps := handler("        _ = fragments\n        Word{text: Int.to_string(1 / 0)}\n")
	ds := literalDiagnostics(t, traps+"fn main()\n    io.print(Word\"x\".text)\nend\n")
	if len(ds) != 1 || !strings.HasPrefix(ds[0].Message, "typed literal Word\"x\" cannot be checked at compile time: its handler failed: ") {
		t.Errorf("a trapping handler: diagnostics = %v", ds)
	}
	loud := handler("        io.print(\"building\")\n        Word{text: Int.to_string(Iter.count(fragments))}\n")
	ds = literalDiagnostics(t, loud+"fn main()\n    io.print(Word`x`.text)\nend\n")
	if want := "typed literal Word`x` cannot be checked at compile time: its handler has effects (Word.from_fragments calls io.print)"; len(ds) != 1 || ds[0].Message != want {
		t.Errorf("an effectful handler: diagnostics = %v, want %q", ds, want)
	}
	if _, err := LoadSource("main", traps+"fn main()\n    s = \"x\"\n    io.print(Word\"${s}\".text)\nend\n"); err != nil {
		t.Fatalf("a literal with a slot does not load: %v", err)
	}
}
