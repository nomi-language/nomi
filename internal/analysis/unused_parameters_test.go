package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"testing"
)

func TestUnusedParameterBodies(t *testing.T) {
	for _, tc := range []struct{ name, source, parameter string }{
		{"function", `fn answer(value: Int) -> Int
    42
end`, "value"},
		{"lambda", `fn answer() -> Int
    f = fn value: Int -> 42
    f(1)
end`, "value"},
		{"method", `struct Thing end impl Thing
    fn answer(value: Thing) -> Int
        42
    end
end`, "value"},
		{"interface default", `interface Answer
    fn answer(value: self) -> Int
        42
    end
end`, "value"},
		{"default argument", `fn answer(value: Int = 1) -> Int
    42
end`, "value"},
		{"boot", `
fn boot(startup: Startup) -> Result<{context: Context}, String>
    Ok({context: Context.root()})
end`, "startup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectUnusedBindingContaining(t, unusedParameterErrors(t, tc.source), "parameter '"+tc.parameter+"' is never read")
		})
	}
}

func TestUnusedParameterAcceptedForms(t *testing.T) {
	for _, source := range []string{
		`fn answer(value: Int) -> Int
    value
end`,
		`fn answer(value: Int) -> Int
    f = fn (value, acc): (Int, Int) = (value, 1) -> value + acc
    f()
end`,
		`fn answer(value: Int) -> Int
    f = fn value: Int = value -> value
    f()
end`,
		`fn answer(_: Int, _reason: String) -> Int
    42
end`,
		`fn answer(value: Int) -> Int
    f = fn -> value
    f()
end`,
		`interface Answer
    fn answer(value: self, extra: Int) -> Int
end`,
		`host fn answer(value: Int) -> Int`,
		`interface Answer
    host fn answer(value: self) -> Int
end`,
		`interface Answer
    fn answer(_value: self, _extra: Int) -> Int
        42
    end
end`,
	} {
		expectNoUnusedBindings(t, unusedParameterErrors(t, source))
	}
}

func TestUnusedParameterInterfaceDefaultDiscardIsNotInScope(t *testing.T) {
	_, errs := checkSourceWithStdlib(`interface Answer
    fn answer(_value: self) -> Int
        _value
    end
end`)
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
interface Answer
    fn answer(value: self, reason: String) -> Int
end
struct Thing end
impl Answer for Thing
 fn answer(_value: Thing, reason: String) -> Int
     _ = reason
     42
 end
end
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
}
