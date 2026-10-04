package analysis_test

import (
	"strings"
	"testing"
)

// Operator diagnostics were all reported at column 1 of the operator's line,
// so `Instant.from_seconds(15) - 5` read `bad.nomi:4:1` and the editor marked
// the start of the line. The rule now: a diagnostic about the operator
// applied to its operands (a mismatch between them, a missing or unmatched
// impl, an operand type the operator is not defined on) points at the
// operator token; one that blames a single operand of two points at that
// operand's first token. The owner-call form `Subtract.subtract(a, b)` has no
// operator token: its missing impl points at the call, its argument mismatch
// at the argument.
//
// Each source marks the expected position with ‸, removed before analysis.
func TestOperatorDiagnosticsPointAtTheOperatorOrOperand(t *testing.T) {
	rows := []struct {
		name string
		src  string
		want string
	}{
		{"missing impl among several", instantSubtractImports + `
fn main() {
    _ = Instant.from_seconds(15) ‸- 5
}
`, "no matching Subtract impl for Instant - Int"},
		{"builtin mismatch", `fn main() {
    _ = 1 ‸+ "a"
}
`, "binary + type mismatch: Int vs String"},
		{"operand on a continuation line", `fn main() {
    _ = True and
        ‸3
}
`, "'and' operands must be Bool, got Int"},
		{"equality mismatch", `fn main() {
    _ = 1 ‸== "a"
}
`, "equality type mismatch: Int vs String"},
		{"comparison mismatch", `fn main() {
    _ = 1 ‸< "a"
}
`, "comparison type mismatch: Int vs String"},
		{"unorderable operand", `fn main() {
    _ = (1, 2) ‸< (1, 3)
}
`, "no impl of `Comparable`"},
		{"type without an operator impl", `fn main() {
    _ = {"a" => 1} ‸- {"a" => 1}
}
`, "no impl of `Subtract` for `Map`"},
		{"right operand of a single impl", `fn main() {
    _ = #{1} + ‸2
}
`, "binary + right operand mismatch: Add expects Set<Int>, got Int"},
		{"right operand of `and`", `fn main() {
    _ = True and ‸3
}
`, "'and' operands must be Bool, got Int"},
		{"left operand of `or`", `fn main() {
    _ = ‸(1 + 2) or True
}
`, "'or' operands must be Bool, got Int"},
		{"unary minus", `fn main() {
    _ = ‸-"x"
}
`, "unary - operand must be numeric, got String"},
		{"interface-qualified argument", `fn main() {
    _ = Add.add(#{1}, ‸2)
}
`, "argument 2: expected Set<Int>, got Int"},
		{"interface-qualified missing impl", instantSubtractImports + `
fn main() {
    _ = ‸Subtract.subtract(Instant.from_seconds(15), 5)
}
`, "no matching Subtract impl for Instant - Int"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			wantLine, wantCol := markerPosition(t, row.src)
			src := strings.Replace(row.src, "‸", "", 1)
			errs := diagnosticsFor(t, src)
			for _, e := range errs {
				if !strings.Contains(e.Message, row.want) {
					continue
				}
				if e.Line != wantLine || e.Col != wantCol {
					t.Fatalf("%q reported at %d:%d, want %d:%d", e.Message, e.Line, e.Col, wantLine, wantCol)
				}
				return
			}
			t.Fatalf("want an error containing %q, got %v", row.want, errs)
		})
	}
}

// markerPosition is the 1-based line and byte column of the ‸ in src, counted
// as if the marker were not there.
func markerPosition(t *testing.T, src string) (int, int) {
	t.Helper()
	i := strings.Index(src, "‸")
	if i < 0 {
		t.Fatal("source has no ‸ marker")
	}
	before := src[:i]
	line := strings.Count(before, "\n") + 1
	col := i - strings.LastIndex(before, "\n")
	return line, col
}
