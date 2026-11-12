package vmhost

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// shoutTag is a checked literal whose check refuses a `!` in the literal's
// text, naming the static and the offset of the `!`.
const shoutTag = "import\n    std::io\n    std::literals.{CheckedLiteral, Fragment, LiteralError}\nend\n\n" +
	"type Shout\n\n" +
	"impl CheckedLiteral<Int, String> for Shout\n" +
	"    fn check(statics: List<String>) -> Result<Unit, LiteralError>\n" +
	"        bad = Iter.with_index(statics) |> Iter.find(fn (_, s) -> String.contains(s, \"!\"))\n\n" +
	"        case bad\n" +
	"            Some((i, s)) ->\n" +
	"                before = String.split(s, \"!\") |> Iter.first() |> Maybe.with_default(\"\")\n\n" +
	"                Err(LiteralError{fragment: i, offset: String.length(before), message: \"no `!` in a shout\"})\n\n" +
	"            None -> Ok(Unit)\n" +
	"        end\n" +
	"    end\n\n" +
	"    fn build(fragments: List<Fragment<Int>>) -> String\n" +
	"        fragments\n" +
	"        |> Iter.map(fn f ->\n" +
	"            case f\n" +
	"                .Static(s) -> String.uppercase(s)\n" +
	"                .Dynamic(n) -> \"${n}\"\n" +
	"            end\n" +
	"        end)\n" +
	"        |> String.join()\n" +
	"    end\n" +
	"end\n\n"

// A checked literal is its build's result in every quote style, never a
// Result, and builds its slots when the program runs.
func TestCheckedLiteral_IsBuildsResultInEveryQuoteStyle(t *testing.T) {
	src := shoutTag +
		"fn main()\n" +
		"    n = 3\n" +
		"    a: String = Shout\"hello ${n} times\"\n" +
		"    b: String = Shout`plain`\n" +
		"    c: String = Shout\"\"\"\n" +
		"        line ${n}\n" +
		"        \"\"\"\n" +
		"    io.print(a)\n" +
		"    io.print(b)\n" +
		"    io.print(c)\n" +
		"end\n"
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects a checked literal: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "HELLO 3 TIMES\nPLAIN\nLINE 3\n"; out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A checked literal whose check refuses its text is a compile error at the
// character the LiteralError names, in every quote style.
func TestCheckedLiteral_RefusedTextIsACompileErrorAtItsPlace(t *testing.T) {
	for _, c := range []struct {
		line, message string
		col           int
	}{
		{"    _ = Shout\"it is ${n} oh no! times\"", "typed literal Shout\"it is ${n} oh no! times\" is invalid: no `!` in a shout", 31},
		{"    _ = Shout`wow!`", "typed literal Shout`wow!` is invalid: no `!` in a shout", 18},
		{"    _ = Shout\"!${n}\"", "typed literal Shout\"!${n}\" is invalid: no `!` in a shout", 9},
	} {
		src := shoutTag + "fn main()\n    n = 3\n    io.print(n)\n" + c.line + "\nend\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 {
			t.Fatalf("%s: %d diagnostics, want 1: %v", c.line, len(ds), ds)
		}
		d := ds[0]
		if d.Message != c.message {
			t.Errorf("%s: message = %q, want %q", c.line, d.Message, c.message)
		}
		if d.Code != InvalidLiteralCode {
			t.Errorf("%s: code = %q, want %q", c.line, d.Code, InvalidLiteralCode)
		}
		wantLine := strings.Count(shoutTag, "\n") + 4
		if d.Line != wantLine || d.Col != c.col {
			t.Errorf("%s: at %d:%d, want %d:%d", c.line, d.Line, d.Col, wantLine, c.col)
		}
	}
}

// A check with effects cannot run when the program is checked: one error at
// the check, however many literals use it.
func TestCheckedLiteral_EffectfulCheckIsOneErrorAtTheCheck(t *testing.T) {
	src := "import\n" +
		"    std::io\n" +
		"    std::literals.{CheckedLiteral, Fragment, LiteralError}\n" +
		"end\n\n" +
		"type Loud\n\n" +
		"impl CheckedLiteral<String, String> for Loud\n" +
		"    fn check(statics: List<String>) -> Result<Unit, LiteralError>\n" +
		"        io.print(\"checking ${Iter.count(statics)}\")\n" +
		"        Ok(Unit)\n" +
		"    end\n\n" +
		"    fn build(fragments: List<Fragment<String>>) -> String\n" +
		"        \"${Iter.count(fragments)}\"\n" +
		"    end\n" +
		"end\n\n" +
		"fn main()\n" +
		"    _ = Loud\"a\"\n" +
		"    _ = Loud\"b\"\n" +
		"end\n"
	ds := literalDiagnostics(t, src)
	if len(ds) != 1 {
		t.Fatalf("%d diagnostics, want 1: %v", len(ds), ds)
	}
	if want := "Loud.check has effects"; !strings.HasPrefix(ds[0].Message, want) {
		t.Errorf("message = %q, want it to start %q", ds[0].Message, want)
	}
	if ds[0].Line != 9 {
		t.Errorf("at line %d, want the check's line 9", ds[0].Line)
	}
}

// A type implements Literal or CheckedLiteral, not both: the front end
// refuses the CheckedLiteral impl, and the message says why.
func TestCheckedLiteral_BothInterfacesIsAnError(t *testing.T) {
	src := "import std::literals.{CheckedLiteral, Fragment, Literal, LiteralError}\n\n" +
		"type Twice\n\n" +
		"impl Literal<String, String> for Twice\n" +
		"    fn from_fragments(fragments: List<Fragment<String>>) -> String\n" +
		"        \"\"\n" +
		"    end\n" +
		"end\n\n" +
		"impl CheckedLiteral<String, String> for Twice\n" +
		"    fn check(statics: List<String>) -> Result<Unit, LiteralError>\n" +
		"        Ok(Unit)\n" +
		"    end\n\n" +
		"    fn build(fragments: List<Fragment<String>>) -> String\n" +
		"        \"\"\n" +
		"    end\n" +
		"end\n\n" +
		"fn main()\n" +
		"    _ = Twice\"a\"\n" +
		"end\n"
	_, err := LoadSource("main", src)
	if err == nil {
		t.Fatal("the front end admits a type that implements both Literal and CheckedLiteral")
	}
	var ds Diagnostics
	if !errors.As(err, &ds) {
		t.Fatalf("the load failed with %T %v; want diagnostics", err, err)
	}
	want := "type `Twice` implements both `Literal` and `CheckedLiteral`; a typed literal has one handler"
	for _, d := range ds {
		if d.Message == want {
			return
		}
	}
	t.Errorf("no diagnostic %q in %v", want, ds)
}
