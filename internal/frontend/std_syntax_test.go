package frontend

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// A syntax error in the embedded stdlib is reported to every program,
// alone and at the std file's line and column, and not as what the checker
// says about the part of the file recovery skipped.
func TestStdSyntaxErrorIsReportedToEveryProgram(t *testing.T) {
	_, errs := parser.ParseWithRecovery(lexer.Lex("fn f() -> Int {\n  1\n    * 2\n}\n"))
	if len(errs) == 0 {
		t.Fatal("the parser accepts a line that starts with `*`; pick another syntax error")
	}
	broken := *std.Shared()
	broken.SyntaxErrors = map[string][]parser.ParseError{"io": errs}

	c := New(Config{})
	c.lib = &broken
	_, err := c.CheckSource("main", "fn main() {\n  1\n}\n", Mode{})
	var ds Diagnostics
	if !errors.As(err, &ds) {
		t.Fatalf("CheckSource = %v, want the stdlib's syntax diagnostics", err)
	}
	if len(ds) != len(errs) {
		t.Fatalf("got %d diagnostics, want %d (the stdlib's syntax errors alone): %v", len(ds), len(errs), ds)
	}
	d := ds[0]
	if d.Path != "std/io.nomi" || d.Line != errs[0].Line || d.Col != errs[0].Col || d.Message != errs[0].Message {
		t.Errorf("diagnostic = %s:%d:%d: %s, want std/io.nomi:%d:%d: %s",
			d.Path, d.Line, d.Col, d.Message, errs[0].Line, errs[0].Col, errs[0].Message)
	}
	if !strings.Contains(strings.Join(d.Hints, "\n"), "rebuild nomi") {
		t.Errorf("hints %q do not say the built-in stdlib needs a rebuild", d.Hints)
	}

	// A stdlib module's own check reports it too.
	c = New(Config{})
	c.lib = &broken
	if _, _, err := c.CheckStdlibSource("maybe", "pub fn g() -> Int {\n  1\n}\n", Mode{}); !errors.As(err, &ds) || ds[0].Path != "std/io.nomi" {
		t.Errorf("CheckStdlibSource = %v, want the stdlib's syntax diagnostics", err)
	}
}

// A check error in the embedded stdlib is reported to every program too, at
// the std file's line and column, so a std module that names something it
// never imports cannot be lowered and run as if it checked. The same
// program checks once the stdlib checks.
func TestStdCheckErrorIsReportedToEveryProgram(t *testing.T) {
	src := "fn main() {\n  dbg 1\n}\n"
	c := New(Config{})
	if _, err := c.CheckSource("main", src, Mode{}); err != nil {
		t.Fatalf("the program does not check against the embedded stdlib: %v", err)
	}
	broken := *std.Shared()
	broken.Errors = map[string][]analysis.TypeError{
		"base64": {{Line: 39, Col: 43, Message: "undefined type or variant 'True'"}},
	}
	c = New(Config{})
	c.lib = &broken
	_, err := c.CheckSource("main", src, Mode{})
	var ds Diagnostics
	if !errors.As(err, &ds) {
		t.Fatalf("CheckSource = %v, want the stdlib's check diagnostics", err)
	}
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want the stdlib's one: %v", len(ds), ds)
	}
	d := ds[0]
	if d.Path != "std/base64.nomi" || d.Line != 39 || d.Col != 43 || d.Message != "undefined type or variant 'True'" {
		t.Errorf("diagnostic = %s:%d:%d: %s, want std/base64.nomi:39:43: undefined type or variant 'True'",
			d.Path, d.Line, d.Col, d.Message)
	}
	if !strings.Contains(strings.Join(d.Hints, "\n"), "does not check; fix it and rebuild nomi") {
		t.Errorf("hints %q do not say the built-in stdlib needs a rebuild", d.Hints)
	}
}
