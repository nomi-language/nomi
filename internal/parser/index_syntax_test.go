package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// A `[` written against the end of an operand is a subscript, which Nomi
// does not have: the error says so at the `[`, in a statement, an operand
// and an argument list alike.
func TestParser_SubscriptIsRefusedWithItsSpelling(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		col        int
		want       string
	}{
		{"after a name in a statement", "dbg xs[1]", 9, "`xs[1]`: read an element with `List.get(xs, 1)`"},
		{"after a name in an operand", "y = xs[1] + 1", 9, "`xs[1]`: read an element with `List.get(xs, 1)`"},
		{"after a name in an argument list", "dbg f(xs[1])", 11, "`xs[1]`: read an element with `List.get(xs, 1)`"},
		{"after a field", "dbg self.items[n - 1]", 17, "`self.items[n - 1]`: read an element with `List.get(self.items, n - 1)`"},
		{"after a call's `)`", "dbg f(x)[0]", 11, "`f(x)[0]`: read an element with `List.get(f(x), 0)`"},
		{"after a `]`", "dbg Vector[1, 2][0]", 19, "`Vector[1, 2][0]`: read an element with `List.get(Vector[1, 2], 0)`"},
		{"a long operand is shown as xs", "dbg some_long_module_name.some_long_field[1]", 44, "`xs[1]`: read an element with `List.get(xs, 1)`"},
		{"a string index is shown as i", `dbg m["k"]`, 8, "`m[i]`: read an element with `List.get(m, i)`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(lexer.Lex("fn main()\n  " + tc.line + "\nend\n"))
			if err == nil {
				t.Fatalf("Parse accepts %q; want the subscript refused", tc.line)
			}
			pe, ok := err.(ParseError)
			if !ok {
				t.Fatalf("Parse = %T %v, want a ParseError", err, err)
			}
			if pe.Line != 2 || pe.Col != tc.col {
				t.Errorf("error at %d:%d, want 2:%d (the `[`)", pe.Line, pe.Col, tc.col)
			}
			if !strings.HasPrefix(pe.Message, "Nomi has no "+tc.want) ||
				!strings.HasSuffix(pe.Message, "which returns a Maybe (`Vector.get`, `Map.get` and `String.at` for those types), or take it apart with a pattern") {
				t.Errorf("message = %q, want it to start %q", pe.Message, "Nomi has no "+tc.want)
			}
		})
	}
}

// The forms a `[` already has keep parsing: a named literal, a list literal
// after a space or a line break, a list pattern, and generic arguments,
// which take `<...>`.
func TestParser_SubscriptCheckLeavesBracketFormsAlone(t *testing.T) {
	for _, body := range []string{
		"v = Vector[1, 2]",
		"s = Set[1, 2]",
		`p = Path["a", "b"]`,
		"dbg Set[1] |> Set.size()",
		"f(x, [1])",
		"f(\n    [1],\n  )",
		"dbg [1]",
		"ys = [1, 2]\n  dbg ys",
		"[a, ..rest] = xs",
		"case xs\n    [] -> 0\n    [x, ..] -> x\n  end",
		"g = fn Items[x] -> x",
		"dbg List.empty<Int>()",
	} {
		t.Run(body, func(t *testing.T) {
			if _, err := Parse(lexer.Lex("fn main()\n  " + body + "\nend\n")); err != nil {
				t.Fatalf("Parse refuses %q: %v", body, err)
			}
		})
	}
}
