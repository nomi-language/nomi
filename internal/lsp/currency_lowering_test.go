package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A custom currency literal the module's nomi.toml does not list is an
// error among the lowering diagnostics, as `nomi check` reports it; a
// `Currency.custom` call with literal arguments that is no error is a
// warning naming the literal form.
func TestLoweringDiagnostics_CustomCurrencies(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	src := "import {\n    std/io\n    std/currency.Currency\n}\n\nfn main() {\n" +
		"    io.print(Currency`PST:0`)\n" +
		"    io.print(Debug.inspect(Currency.custom(\"PTS\", 0)))\n}\n"
	dir := writeProject(t, map[string]string{
		"main.nomi": src,
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[money]\ncustom_currencies = [\"PTS:0\"]\n",
	})
	diags := loweringDiagnostics(filepath.Join(dir, "main.nomi"), src, nil)
	if len(diags) != 2 {
		t.Fatalf("lowering diagnostics %+v; want two", diags)
	}
	lit, call := diags[0], diags[1]
	if want := "typed literal Currency`PST:0`: `PST:0` is a custom currency this module does not list: add it to [money].custom_currencies in nomi.toml\nhelp: did you mean `PTS:0`? nomi.toml lists it"; lit.Message != want {
		t.Errorf("literal message = %q, want %q", lit.Message, want)
	}
	if lit.Code == nil || lit.Code.Value != "invalid-literal" || lit.Severity == nil || *lit.Severity != protocol.DiagnosticSeverityError {
		t.Errorf("literal code %+v severity %v, want an invalid-literal error", lit.Code, lit.Severity)
	}
	if lit.Range.Start != (protocol.Position{Line: 6, Character: 13}) {
		t.Errorf("literal range %+v", lit.Range)
	}
	if !strings.HasPrefix(call.Message, "Currency.custom(\"PTS\", 0) makes a fixed currency: write Currency`PTS:0`") {
		t.Errorf("call message = %q", call.Message)
	}
	if call.Code == nil || call.Code.Value != "custom-currency-call" || call.Severity == nil || *call.Severity != protocol.DiagnosticSeverityWarning {
		t.Errorf("call code %+v severity %v, want a custom-currency-call warning", call.Code, call.Severity)
	}
	if call.Range.Start != (protocol.Position{Line: 7, Character: 27}) {
		t.Errorf("call range %+v", call.Range)
	}
}
