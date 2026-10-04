package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

func unusedBindingErrors(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if e.Code == analysis.UnusedBindingCode {
			out = append(out, e)
		}
	}
	return out
}

func expectUnusedBindingContaining(t *testing.T, errs []analysis.TypeError, substr string) analysis.TypeError {
	t.Helper()
	for _, e := range unusedBindingErrors(errs) {
		if strings.Contains(diagText(e), substr) {
			return e
		}
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	t.Fatalf("expected unused-binding error containing %q, got %d errors:\n  %s",
		substr, len(errs), strings.Join(msgs, "\n  "))
	return analysis.TypeError{}
}

func expectNoUnusedBindings(t *testing.T, errs []analysis.TypeError) {
	t.Helper()
	for _, e := range unusedBindingErrors(errs) {
		t.Errorf("unexpected unused-binding error: %s", e.Error())
	}
}

func expectDiagnosticContaining(t *testing.T, errs []analysis.TypeError, substr string) analysis.TypeError {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			return e
		}
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	t.Fatalf("expected diagnostic containing %q, got %d errors:\n  %s",
		substr, len(errs), strings.Join(msgs, "\n  "))
	return analysis.TypeError{}
}

func TestUnusedBinding_LocalBindingNeverRead(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn main() {
  total =
    5
    |> dbg
  Unit
}
`)
	e := expectUnusedBindingContaining(t, errs, "binding 'total' is never read")
	if e.Line != 3 {
		t.Errorf("error line: got %d, want 3", e.Line)
	}
	if e.Code != analysis.UnusedBindingCode {
		t.Errorf("code: got %q, want %q", e.Code, analysis.UnusedBindingCode)
	}
}

func TestUnusedBinding_ReadBindingIsAccepted(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn main(): Int {
  total = 5
  dbg total
}
`)
	expectNoUnusedBindings(t, errs)
}

func TestUnusedBinding_AssertPatternBindingNeverRead(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
test "pattern binding is unused" {
  assert Some(value) = Some(1)
}
`)
	expectUnusedBindingContaining(t, errs, "binding 'value' is never read")
}

func TestUnusedBinding_AssertPatternBindingReadIsAccepted(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
test "pattern binding is used" {
  assert Some(value) = Some(1)
  assert value == 1
}
`)
	expectNoUnusedBindings(t, errs)
}

func TestUnusedBinding_ShadowedBindingMustBeReadBeforeShadow(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn main(): String {
  name = "Jane"
  name = "John"
  dbg name
}
`)
	expectUnusedBindingContaining(t, errs, "binding 'name' is never read")
}

func TestUnusedBinding_RefinementShadowingIsAccepted(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn main(): String {
  name = "  JANE  "
  name = String.trim(name)
  name = String.to_lower(name)
  dbg name
}
`)
	expectNoUnusedBindings(t, errs)
}

func TestUnusedBinding_WildcardPatternIsIgnored(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn main() {
  (_, value) = (1, 2)
  dbg value
}
`)
	expectNoUnusedBindings(t, errs)
}

func TestUnusedBinding_DiscardBindingDoesNotEnterScope(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn main(): Int {
  _total = 5
  _total
}
`)
	expectDiagnosticContaining(t, errs, "undefined variable '_total'")
}

func TestUnusedBinding_ShortDiscardBindingDoesNotEnterScope(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn main() {
  _ = 5
}
`)
	expectNoUnusedBindings(t, errs)
}

func TestUnusedBinding_DiscardParameterDoesNotEnterScope(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn ignore(_value: Int): Int {
  _value
}
`)
	expectDiagnosticContaining(t, errs, "undefined variable '_value'")
}

func TestUnusedBinding_DoubleUnderscoreBindingEntersScope(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn main(): Int {
  __internal = 5
  __internal
}
`)
	expectNoUnusedBindings(t, errs)
}

func TestUnusedBinding_MapPatternKeyReadsBinding(t *testing.T) {
	errs := buildErrsWithStdlib(t, `
fn main(): String {
  base = 1
  numbers = {2 => "computed"}
  case numbers {
    {base + 1 => value} -> value
    _ -> "?"
  }
}
`)
	expectNoUnusedBindings(t, errs)
}
