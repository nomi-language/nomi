package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const pointDecl = "struct Point {\n    x: Int\n    y: Int\n}\n\n"

// captureMain is decls, then body in `fn main() { ... }`.
func captureMain(decls, body string) string {
	return strings.TrimPrefix(fnMainAfter(decls, body), "import std/io\n\n")
}

// A lambda the style rule writes as a capture becomes one: `&` against a
// call or a literal, `&(...)` around an operator.
func TestUseCapture_RewritesAOneStepLambda(t *testing.T) {
	for _, c := range []struct {
		name, decls, body, want string
	}{
		{"comparison", "", "big = Iter.filter([1, 60], ‸fn s -> s > 50) |> Iter.to_list()\ndbg big", "big = Iter.filter([1, 60], &(&1 > 50)) |> Iter.to_list()"},
		{"call", "", "letters = %[\"a\"]\nhit = Iter.filter([\"a\", \"b\"], fn l -> ‸Set.contains?(letters, l)) |> Iter.to_list()\ndbg hit", "hit = Iter.filter([\"a\", \"b\"], &Set.contains?(letters, &1)) |> Iter.to_list()"},
		{"struct", pointDecl, "ps = Iter.map([1, 2], ‸fn x -> #Point{x: x, y: 0}) |> Iter.to_list()\ndbg ps", "ps = Iter.map([1, 2], &#Point{x: &1, y: 0}) |> Iter.to_list()"},
		{"punned field", pointDecl, "ps = Iter.map([1, 2], ‸fn x -> #Point{x, y: 0}) |> Iter.to_list()\ndbg ps", "ps = Iter.map([1, 2], &#Point{x: &1, y: 0}) |> Iter.to_list()"},
		{"tuple", "", "ps = Iter.map([1, 2], ‸fn x -> #(x, 0)) |> Iter.to_list()\ndbg ps", "ps = Iter.map([1, 2], &#(&1, 0)) |> Iter.to_list()"},
		{"negation", "", "ns = Iter.map([1, 2], ‸fn x -> -x) |> Iter.to_list()\ndbg ns", "ns = Iter.map([1, 2], &(-&1)) |> Iter.to_list()"},
		{"two parameters", "", "add: (Int, Int) -> Int = ‸fn a, b -> a + b\ndbg add(1, 2)", "add: (Int, Int) -> Int = &(&1 + &2)"},
		{"named argument", "", "s = Iter.map([1, 2], ‸fn n -> Int.to_string(n)) |> String.join(\",\")\ndbg s", "s = Iter.map([1, 2], &Int.to_string(&1)) |> String.join(\",\")"},
		{"a then stage", "fn divide(x: Int, y: Int) -> Int {\n    x / y\n}\n\n", "dbg 10 |> then ‸fn n -> divide(100, n)", "dbg 10 |> then &divide(100, &1)"},
		{"a call in another argument", "", "xs = Iter.map([1], ‸fn x -> Int.max(x, Int.abs(-3))) |> Iter.to_list()\ndbg xs", "xs = Iter.map([1], &Int.max(&1, Int.abs(-3))) |> Iter.to_list()"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkRefactor(t, captureMain(c.decls, c.body), "Use a capture", protocol.CodeActionKindRefactorRewrite)
			if !strings.Contains(got, c.want) {
				t.Errorf("result lacks %q:\n%s", c.want, got)
			}
		})
	}
}

// A lambda the style rule keeps as a lambda is offered no capture.
func TestUseCapture_KeepsALambdaTheRuleKeeps(t *testing.T) {
	for _, c := range []struct {
		name, decls, body string
		allowed           []string
	}{
		{"two steps", "", "xs = Iter.map([1], ‸fn x -> x * 2 + 1) |> Iter.to_list()\ndbg xs", nil},
		{"two uses", "", "xs = Iter.map([1], ‸fn n -> n * n) |> Iter.to_list()\ndbg xs", nil},
		{"pattern", "", "xs = Iter.map([#(1, 2)], ‸fn #(_, v) -> v) |> Iter.to_list()\ndbg xs", nil},
		{"field read", pointDecl, "xs = Iter.filter([#Point{x: 1, y: 2}], ‸fn p -> p.x > 0) |> Iter.to_list()\ndbg xs", nil},
		{"seed", "", "total = Iter.reduce([1, 2], ‸fn acc = 0, x -> acc + x)\ndbg total", nil},
		{"no expected type", "", "f = ‸fn x: Int -> x > 2\ndbg f(3)", nil},
		{"out of order", "", "sub: (Int, Int) -> Int = ‸fn a, b -> b - a\ndbg sub(1, 2)", nil},
		{"nested use", "", "xs = Iter.map([1], ‸fn x -> Int.max(x + 1, 0)) |> Iter.to_list()\ndbg xs", nil},
		{"and", "", "ready = True\nxs = Iter.filter([True], ‸fn b -> b and ready) |> Iter.to_list()\ndbg xs", nil},
		{"a list where an Iter is declared", "", "ns = Iter.map([[1], [2, 3]], ‸fn xs -> Iter.count(xs)) |> Iter.to_list()\ndbg ns", nil},
		{"block body", "", "xs = Iter.map([1], ‸fn x -> {\n    x + 1\n}) |> Iter.to_list()\ndbg xs", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			refuseRefactor(t, captureMain(c.decls, c.body), "Use a capture", c.allowed...)
		})
	}
}

// A capture becomes a lambda, its parameter named for the callee's
// parameter it fills.
func TestWriteAsLambda_RewritesACapture(t *testing.T) {
	for _, c := range []struct {
		name, decls, body, want string
	}{
		{"operator", "", "big = Iter.filter([1, 60], &(&1 ‸> 50)) |> Iter.to_list()\ndbg big", "big = Iter.filter([1, 60], fn x -> x > 50) |> Iter.to_list()"},
		{"partial call", "fn scale(value: Int, by: Int) -> Int {\n    value * by\n}\n\n", "xs = Iter.map([1, 2], ‸&scale(&1, 10)) |> Iter.to_list()\ndbg xs", "xs = Iter.map([1, 2], fn value -> scale(value, 10)) |> Iter.to_list()"},
		{"named slot", "fn scale(value: Int, by: Int) -> Int {\n    value * by\n}\n\n", "xs = Iter.map([1, 2], ‸&scale(10, by: &1)) |> Iter.to_list()\ndbg xs", "xs = Iter.map([1, 2], fn by -> scale(10, by: by)) |> Iter.to_list()"},
		{"struct", pointDecl, "ps = Iter.map([1, 2], &#Point{x: ‸&1, y: 0}) |> Iter.to_list()\ndbg ps", "ps = Iter.map([1, 2], fn x -> #Point{x, y: 0}) |> Iter.to_list()"},
		{"two parameters", "", "add: (Int, Int) -> Int = &(&1 ‸+ &2)\ndbg add(1, 2)", "add: (Int, Int) -> Int = fn x, y -> x + y"},
		{"a name in scope", "", "x = 50\nbig = Iter.filter([1, 60], &(&1 ‸> x)) |> Iter.to_list()\ndbg big", "big = Iter.filter([1, 60], fn x2 -> x2 > x) |> Iter.to_list()"},
		{"a then stage", "fn divide(x: Int, y: Int) -> Int {\n    x / y\n}\n\n", "dbg 10 |> then ‸&divide(100, &1)", "|> then fn y -> divide(100, y)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkRefactor(t, captureMain(c.decls, c.body), "Write as a lambda", protocol.CodeActionKindRefactorRewrite)
			if !strings.Contains(got, c.want) {
				t.Errorf("result lacks %q:\n%s", c.want, got)
			}
		})
	}
}

// A partial capture bound with no expected type has no lambda that types
// without an annotation, so it is offered none.
func TestWriteAsLambda_RefusesWhatIsNoFunctionOfItsPosition(t *testing.T) {
	decls := "fn divide(x: Int, y: Int) -> Int {\n    x / y\n}\n\n"
	for _, body := range []string{
		"halve = ‸&divide(&1, 2)\ndbg halve(10)",
	} {
		refuseRefactor(t, captureMain(decls, body), "Write as a lambda")
	}
}

// Use a capture, then write it as a lambda again: each step checks, and the
// last is the source it started from.
func TestCaptureRoundTrip_KeepsTheProgram(t *testing.T) {
	for _, c := range []struct{ decls, body, lambda string }{
		{"", "big = Iter.filter([1, 60], ‸fn x -> x > 50) |> Iter.to_list()\ndbg big", "fn x -> x > 50"},
		{pointDecl, "ps = Iter.map([1, 2], ‸fn x -> #Point{x, y: 0}) |> Iter.to_list()\ndbg ps", "fn x -> #Point{x, y: 0}"},
		{"fn scale(value: Int, by: Int) -> Int {\n    value * by\n}\n\n", "xs = Iter.map([1, 2], ‸fn value -> scale(value, 10)) |> Iter.to_list()\ndbg xs", "fn value -> scale(value, 10)"},
		{"", "add: (Int, Int) -> Int = ‸fn x, y -> x + y\ndbg add(1, 2)", "fn x, y -> x + y"},
	} {
		src := captureMain(c.decls, c.body)
		clean, _ := refactorSource(t, src)
		capture := checkRefactor(t, src, "Use a capture", protocol.CodeActionKindRefactorRewrite)
		i := strings.Index(clean, c.lambda)
		if i < 0 {
			t.Fatalf("no %q in source", c.lambda)
		}
		// The capture starts where the lambda did.
		back := checkRefactor(t, capture[:i]+"‸"+capture[i:], "Write as a lambda", protocol.CodeActionKindRefactorRewrite)
		if back != clean {
			t.Errorf("round trip:\n%s\nwant:\n%s", back, clean)
		}
	}
}
