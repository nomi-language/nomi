package lsp

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// A std that does not parse puts one error on every document, naming the
// std file, line and column; a std that parses puts none.
func TestStdSyntaxDiagnostic(t *testing.T) {
	if d, ok := stdSyntaxDiagnostic(std.Shared()); ok {
		t.Fatalf("the embedded std parses, but the server reports %q", d.Message)
	}
	broken := &std.StdLib{SyntaxErrors: map[string][]parser.ParseError{
		"io": {{Line: 12, Col: 5, Message: "unexpected `and`"}},
	}}
	d, ok := stdSyntaxDiagnostic(broken)
	if !ok {
		t.Fatal("no diagnostic for a std with a syntax error")
	}
	if !strings.Contains(d.Message, "std/io.nomi:12:5: unexpected `and`") {
		t.Errorf("message %q does not name the std file's error", d.Message)
	}
}
