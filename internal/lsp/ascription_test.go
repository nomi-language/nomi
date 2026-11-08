package lsp

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A hover on `as` shows the type it gives the value.
func TestHoverAscription(t *testing.T) {
	got := hoverAtMark(t, "fn main() {\n    tags = [] ▮as List<String>\n    _ = tags\n}\n")
	if !strings.Contains(got, "List<String>") {
		t.Errorf("hover = %q, want it to name List<String>", got)
	}
}

// An `as` whose value already has the type is marked unnecessary, as a
// hint over `as Type`.
func TestRedundantAscriptionHint(t *testing.T) {
	src := "fn main() {\n    x = 3\n    _ = x as Int\n    _ = [] as List<Int>\n}\n"
	nodes, errs := parser.ParseWithRecovery(lexer.Lex(src))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	lib := loadStd()
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, "/tmp", nil)
	analysis.CheckTypes(fa, nodes)
	diags := redundantAscriptionDiagnostics(fa, nodes)
	if len(diags) != 1 {
		t.Fatalf("want one hint, got %+v", diags)
	}
	d := diags[0]
	if d.Message != "`as Int` is redundant: the value already has type `Int`" || *d.Severity != protocol.DiagnosticSeverityHint {
		t.Errorf("hint = %q severity %v", d.Message, *d.Severity)
	}
	if d.Range.Start.Line != 2 || d.Range.Start.Character != 10 || d.Range.End.Character != 16 {
		t.Errorf("range = %+v, want line 2, characters 10 to 16", d.Range)
	}
}
