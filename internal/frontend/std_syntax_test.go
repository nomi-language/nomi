package frontend

import (
	"errors"
	"strings"
	"testing"

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
