package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"testing"
)

func TestUnusedParameterBodies(t *testing.T) {
	for _, tc := range []struct{ name, source, parameter string }{
		{"function", `fn answer(value: Int): Int { 42 }`, "value"},
		{"lambda", `fn answer(): Int { f = |value: Int| 42; f(1) }`, "value"},
		{"method", `struct Thing {} impl Thing { fn answer(value: Thing): Int { 42 } }`, "value"},
		{"interface default", `interface Answer { fn answer(value: self): Int { 42 } }`, "value"},
		{"default argument", `fn answer(value: Int = 1): Int { 42 }`, "value"},
		{"boot", `
fn boot(startup: Startup): {context: Context} { {context: Context.root()} }`, "startup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectUnusedBindingContaining(t, unusedParameterErrors(t, tc.source), "parameter '"+tc.parameter+"' is never read")
		})
	}
}

func TestUnusedParameterAcceptedForms(t *testing.T) {
	for _, source := range []string{
		`fn answer(value: Int): Int { value }`,
		`fn answer(value: Int): Int { f = |(value, acc): (Int, Int) = (value, 1)| value + acc; f() }`,
		`fn answer(value: Int): Int { f = |value: Int = value| value; f() }`,
		`fn answer(_: Int, _reason: String): Int { 42 }`,
		`fn answer(value: Int): Int { f = || value; f() }`,
		`interface Answer { fn answer(value: self, extra: Int): Int }`,
		`host fn answer(value: Int): Int`,
		`interface Answer { host fn answer(value: self): Int }`,
		`interface Answer { fn answer(_value: self, _extra: Int): Int { 42 } }`,
	} {
		expectNoUnusedBindings(t, unusedParameterErrors(t, source))
	}
}

func TestUnusedParameterInterfaceDefaultDiscardIsNotInScope(t *testing.T) {
	_, errs := checkSourceWithStdlib(`interface Answer { fn answer(_value: self): Int { _value } }`)
	expectDiagnosticContaining(t, errs, "undefined variable '_value'")
}

func unusedParameterErrors(t *testing.T, source string) []analysis.TypeError {
	t.Helper()
	if _, err := parser.Parse(lexer.Lex(source)); err != nil {
		t.Fatal(err)
	}
	return buildErrsWithStdlib(t, source)
}

func TestUnusedParameterRequiredInterfaceLabelExplicitDiscard(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
interface Answer { fn answer(value: self, reason: String): Int }
struct Thing {}
impl Answer for Thing {
 fn answer(_value: Thing, reason: String): Int { _ = reason; 42 }
}
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
}
