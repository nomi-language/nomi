package lsp

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// An operand in a field's new value that reads the field's current value by
// its path is a hint over the path that `..` writes it. A path to another
// field, or to the same field of another value, is no hint.
func TestRestatedPathHint(t *testing.T) {
	src := "struct A\n    street: String\n    city: String\nend\n\nstruct P\n    address: A\nend\n\n" +
		"fn main()\n    ada = P{address: A{street: \"1\", city: \"Bath\"}}\n    bob = ada\n" +
		"    moved = {..ada, address: {.., street: ada.address.street + \"!\", city: ada.address.street + bob.address.city}}\n" +
		"    _ = moved\nend\n"
	nodes, errs := parser.ParseWithRecovery(lexer.Lex(src))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	lib := loadStd()
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, "/tmp", nil)
	analysis.CheckTypes(fa, nodes)
	if len(fa.TypeErrors) > 0 {
		t.Fatalf("the program does not check: %v", fa.TypeErrors)
	}
	diags := restatedPathDiagnostics(fa, nodes)
	if len(diags) != 1 {
		t.Fatalf("want one hint, got %+v", diags)
	}
	d := diags[0]
	if d.Message != "this reads the value `street` already holds here, which `..` writes" || *d.Severity != protocol.DiagnosticSeverityHint {
		t.Errorf("hint = %q severity %v", d.Message, *d.Severity)
	}
	if d.Range.Start.Line != 12 || d.Range.Start.Character != 42 || d.Range.End.Character != 60 {
		t.Errorf("range = %+v, want line 12, characters 42 to 60", d.Range)
	}
}
