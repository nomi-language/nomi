package lsp

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

const pointDecl = "struct Point {\n    x: Int\n    y: Int\n}\n\n"

// captureMain is decls, then body in `fn main() { ... }`.
func captureMain(decls, body string) string {
	return strings.TrimPrefix(fnMainAfter(decls, body), "import std::io\n\n")
}

// A lambda the style rule writes as a capture becomes one: `&` against a
// call or a literal, `&(...)` around an operator.
func TestUseCapture_RewritesAOneStepLambda(t *testing.T) {
	for _, c := range []struct {
		name, decls, body, want string
	}{
		{"comparison", "", "big = Iter.filter([1, 60], ‸fn s -> s > 50) |> Iter.to_list()\ndbg big", "big = Iter.filter([1, 60], &(&1 > 50)) |> Iter.to_list()"},
		{"call", "", "letters = %[\"a\"]\nhit = Iter.filter([\"a\", \"b\"], fn l -> ‸Set.contains(letters, l)) |> Iter.to_list()\ndbg hit", "hit = Iter.filter([\"a\", \"b\"], &Set.contains(letters, &1)) |> Iter.to_list()"},
		{"struct", pointDecl, "ps = Iter.map([1, 2], ‸fn x -> Point{x: x, y: 0}) |> Iter.to_list()\ndbg ps", "ps = Iter.map([1, 2], &Point{x: &1, y: 0}) |> Iter.to_list()"},
		{"punned field", pointDecl, "ps = Iter.map([1, 2], ‸fn x -> Point{x, y: 0}) |> Iter.to_list()\ndbg ps", "ps = Iter.map([1, 2], &Point{x: &1, y: 0}) |> Iter.to_list()"},
		{"tuple", "", "ps = Iter.map([1, 2], ‸fn x -> #(x, 0)) |> Iter.to_list()\ndbg ps", "ps = Iter.map([1, 2], &#(&1, 0)) |> Iter.to_list()"},
		{"negation", "", "ns = Iter.map([1, 2], ‸fn x -> -x) |> Iter.to_list()\ndbg ns", "ns = Iter.map([1, 2], &(-&1)) |> Iter.to_list()"},
		{"two parameters", "", "add: (Int, Int) -> Int = ‸fn a, b -> a + b\ndbg add(1, 2)", "add: (Int, Int) -> Int = &(&1 + &2)"},
		{"named argument", "", "s = Iter.map([1, 2], ‸fn n -> Int.to_string(n)) |> String.join(\",\")\ndbg s", "s = Iter.map([1, 2], &Int.to_string(&1)) |> String.join(\",\")"},
		{"a then stage", "fn divide(x: Int, y: Int) -> Int {\n    x / y\n}\n\n", "dbg 10 |> then ‸fn n -> divide(100, n)", "dbg 10 |> then &divide(100, &1)"},
		{"a call in another argument", "", "xs = Iter.map([1], ‸fn x -> Int.max(x, Int.abs(-3))) |> Iter.to_list()\ndbg xs", "xs = Iter.map([1], &Int.max(&1, Int.abs(-3))) |> Iter.to_list()"},
		{"string", "", "ids = Iter.map([1, 2], ‸fn x -> \"id ${x}\") |> Iter.to_list()\ndbg ids", "ids = Iter.map([1, 2], &\"id ${&1}\") |> Iter.to_list()"},
		{"two steps", "", "xs = Iter.map([1], ‸fn x -> x * 2 + 1) |> Iter.to_list()\ndbg xs", "xs = Iter.map([1], &(&1 * 2 + 1)) |> Iter.to_list()"},
		{"two uses", "", "xs = Iter.map([1], ‸fn n -> n * n) |> Iter.to_list()\ndbg xs", "xs = Iter.map([1], &(&1 * &1)) |> Iter.to_list()"},
		{"a use inside a call", "", "dbg \"level\" |> then ‸fn t -> t == String.reverse(t)", "dbg \"level\" |> then &(&1 == String.reverse(&1))"},
		{"nested use", "", "xs = Iter.map([1], ‸fn x -> Int.max(x + 1, 0)) |> Iter.to_list()\ndbg xs", "xs = Iter.map([1], &Int.max(&1 + 1, 0)) |> Iter.to_list()"},
		{"a field read and a use", pointDecl, "xs = Iter.map([Point{x: 1, y: 2}], ‸fn p -> p.x + p.y) |> Iter.to_list()\ndbg xs", "xs = Iter.map([Point{x: 1, y: 2}], &(&1.x + &1.y)) |> Iter.to_list()"},
		{"a field read below an operand", pointDecl, "xs = Iter.map([Point{x: 1, y: 2}], ‸fn p -> Int.max(Int.abs(p.x), 0)) |> Iter.to_list()\ndbg xs", "xs = Iter.map([Point{x: 1, y: 2}], &Int.max(Int.abs(&1.x), 0)) |> Iter.to_list()"},
		{"a slot that computes", "", "ids = Iter.map([1], ‸fn x -> \"id ${x + 1}\") |> Iter.to_list()\ndbg ids", "ids = Iter.map([1], &\"id ${&1 + 1}\") |> Iter.to_list()"},
		{"two parameters, the first used twice", "", "f: (Int, Int) -> Int = ‸fn a, b -> a * a + b\ndbg f(1, 2)", "f: (Int, Int) -> Int = &(&1 * &1 + &2)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkRefactor(t, captureMain(c.decls, c.body), "Use a capture", protocol.CodeActionKindRefactorRewrite)
			if !strings.Contains(got, c.want) {
				t.Errorf("result lacks %q:\n%s", c.want, got)
			}
		})
	}
}

const studentDecls = `struct Address {
    city: String
}

struct Student {
    score: Int
    address: Address
    marks: List<Int>
}

fn grade(score: Int) -> Int {
    score / 10
}

fn compare(a: Int, b: Int) -> Int {
    a - b
}

`

const studentsLine = "ss = [\n    Student{score: 60, address: Address{city: \"O\"}, marks: [1]},\n]\n\n"

// A parameter read through a field stands as the receiver of the field
// read: `fn s -> grade(s.score)` is `&grade(&1.score)`. The capture is no
// partial, so it takes the expected function type, and checks to the
// lambda's type.
func TestUseCapture_RewritesAFieldRead(t *testing.T) {
	for _, c := range []struct {
		name, decls, body, want string
	}{
		{"a call", studentDecls, studentsLine + "by_grade = ss |> Iter.group_by(‸fn s -> grade(s.score))\ndbg by_grade", "by_grade = ss |> Iter.group_by(&grade(&1.score))"},
		{"a comparison", studentDecls, studentsLine + "hi = Iter.filter(ss, ‸fn s -> s.score > 50) |> Iter.to_list()\ndbg hi", "hi = Iter.filter(ss, &(&1.score > 50)) |> Iter.to_list()"},
		{"two parameters", studentDecls, "by: (Student, Student) -> Int = ‸fn a, b -> compare(a.score, b.score)\ndbg by", "by: (Student, Student) -> Int = &compare(&1.score, &2.score)"},
		{"a field read and a whole parameter", studentDecls, "by: (Int, Student) -> Int = ‸fn a, b -> compare(a, b.score)\ndbg by", "by: (Int, Student) -> Int = &compare(&1, &2.score)"},
		{"a chain", studentDecls, studentsLine + "ns = Iter.map(ss, ‸fn s -> String.length(s.address.city)) |> Iter.to_list()\ndbg ns", "ns = Iter.map(ss, &String.length(&1.address.city)) |> Iter.to_list()"},
		{"a List where an Iter is declared", studentDecls, studentsLine + "ns = Iter.map(ss, ‸fn s -> Iter.count(s.marks)) |> Iter.to_list()\ndbg ns", "ns = Iter.map(ss, &Iter.count(&1.marks)) |> Iter.to_list()"},
		{"a struct field", pointDecl, "ps = Iter.map([Point{x: 1, y: 2}], ‸fn p -> Point{x: p.y, y: 0}) |> Iter.to_list()\ndbg ps", "ps = Iter.map([Point{x: 1, y: 2}], &Point{x: &1.y, y: 0}) |> Iter.to_list()"},
		{"a tuple", pointDecl, "ps = Iter.map([Point{x: 1, y: 2}], ‸fn p -> #(p.x, 0)) |> Iter.to_list()\ndbg ps", "ps = Iter.map([Point{x: 1, y: 2}], &#(&1.x, 0)) |> Iter.to_list()"},
		{"a tuple element", "", "xs = Iter.filter([#(1, 2), #(3, 4)], ‸fn t -> t.0 > 1) |> Iter.to_list()\ndbg xs", "xs = Iter.filter([#(1, 2), #(3, 4)], &(&1.0 > 1)) |> Iter.to_list()"},
		{"a negation", pointDecl, "ns = Iter.map([Point{x: 1, y: 2}], ‸fn p -> -p.x) |> Iter.to_list()\ndbg ns", "ns = Iter.map([Point{x: 1, y: 2}], &(-&1.x)) |> Iter.to_list()"},
		{"a then stage", pointDecl, "dbg Point{x: 1, y: 2} |> then ‸fn p -> Int.abs(p.x)", "|> then &Int.abs(&1.x)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := captureMain(c.decls, c.body)
			got := checkRefactor(t, src, "Use a capture", protocol.CodeActionKindRefactorRewrite)
			if !strings.Contains(got, c.want) {
				t.Fatalf("result lacks %q:\n%s", c.want, got)
			}
			clean, _ := refactorSource(t, src)
			lambda := functionTypeOf[*ast.Lambda](t, clean)
			capture := functionTypeOf[*ast.Capture](t, got)
			t.Logf("both type to %s", lambda)
			if lambda != capture {
				t.Errorf("the capture types to %s, the lambda to %s", capture, lambda)
			}
		})
	}
}

// functionTypeOf checks src and answers the type of its first N node, and
// the function type expected where it stands.
func functionTypeOf[N ast.Node](t *testing.T, src string) string {
	t.Helper()
	s := NewServer()
	s.docs.Open(refactorURI, src)
	snap := s.docs.Snapshot(refactorURI)
	if snap == nil || snap.Analysis == nil || len(snap.Errors) > 0 || len(snap.Analysis.TypeErrors) > 0 {
		t.Fatalf("source does not check:\n%s", src)
	}
	var found ast.Node
	for _, n := range snap.Nodes {
		ast.Inspect(n, func(m ast.Node) bool {
			if _, ok := m.(N); ok && found == nil {
				found = m
			}
			return found == nil
		})
	}
	if found == nil {
		t.Fatalf("no %T in:\n%s", *new(N), src)
	}
	got := analysis.ResolveTypeVar(snap.Analysis.ExprTypes[found])
	want := analysis.ResolveTypeVar(snap.Analysis.ExpectedTypes[found])
	if got == nil {
		t.Fatalf("%T has no type", found)
	}
	out := got.String()
	if want != nil {
		out += " where " + want.String() + " is expected"
	}
	return out
}

// A lambda the style rule keeps as a lambda is offered no capture.
func TestUseCapture_KeepsALambdaTheRuleKeeps(t *testing.T) {
	for _, c := range []struct {
		name, decls, body string
		allowed           []string
	}{
		{"pattern", "", "xs = Iter.map([#(1, 2)], ‸fn #(_, v) -> v) |> Iter.to_list()\ndbg xs", nil},
		{"a bare field read", pointDecl, "xs = Iter.map([Point{x: 1, y: 2}], ‸fn p -> p.x) |> Iter.to_list()\ndbg xs", nil},
		{"a field read out of order", pointDecl, "sub: (Point, Point) -> Int = ‸fn a, b -> b.x - a.x\ndbg sub(Point{x: 1, y: 0}, Point{x: 2, y: 0})", nil},
		{"a field read piped", pointDecl, "xs = Iter.map([Point{x: 1, y: 2}], ‸fn p -> p.x |> Int.abs()) |> Iter.to_list()\ndbg xs", nil},
		{"seed", "", "total = Iter.reduce([1, 2], ‸0, fn acc, x -> acc + x)\ndbg total", nil},
		{"no expected type", "", "f = ‸fn x: Int -> x > 2\ndbg f(3)", nil},
		{"out of order", "", "sub: (Int, Int) -> Int = ‸fn a, b -> b - a\ndbg sub(1, 2)", nil},
		{"and", "", "ready = True\nxs = Iter.filter([True], ‸fn b -> b and ready) |> Iter.to_list()\ndbg xs", nil},
		{"a list where an Iter is declared", "", "ns = Iter.map([[1], [2, 3]], ‸fn xs -> Iter.count(xs)) |> Iter.to_list()\ndbg ns", nil},
		{"a use in a lambda of its own", "", "xs = Iter.map([1], ‸fn x -> Iter.map([2], fn y -> y + x)) |> Iter.to_list()\ndbg xs", nil},
		{"a capture inside", "", "xs = Iter.map([[1]], ‸fn x -> Iter.map(x, &(&1 + 1))) |> Iter.to_list()\ndbg xs", nil},
		{"the second parameter read first", "", "f: (Int, Int) -> Int = ‸fn a, b -> b * a + a\ndbg f(1, 2)", nil},
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
		{"struct", pointDecl, "ps = Iter.map([1, 2], &Point{x: ‸&1, y: 0}) |> Iter.to_list()\ndbg ps", "ps = Iter.map([1, 2], fn x -> Point{x, y: 0}) |> Iter.to_list()"},
		{"two parameters", "", "add: (Int, Int) -> Int = &(&1 ‸+ &2)\ndbg add(1, 2)", "add: (Int, Int) -> Int = fn x, y -> x + y"},
		{"a name in scope", "", "x = 50\nbig = Iter.filter([1, 60], &(&1 ‸> x)) |> Iter.to_list()\ndbg big", "big = Iter.filter([1, 60], fn x2 -> x2 > x) |> Iter.to_list()"},
		{"a then stage", "fn divide(x: Int, y: Int) -> Int {\n    x / y\n}\n\n", "dbg 10 |> then ‸&divide(100, &1)", "|> then fn y -> divide(100, y)"},
		{"a field read", studentDecls, studentsLine + "by_grade = ss |> Iter.group_by(‸&grade(&1.score))\ndbg by_grade", "by_grade = ss |> Iter.group_by(fn x -> grade(x.score))"},
		{"two field reads", studentDecls, "by: (Student, Student) -> Int = ‸&compare(&1.score, &2.score)\ndbg by", "by: (Student, Student) -> Int = fn x, y -> compare(x.score, y.score)"},
		{"string", "", "ids = Iter.map([1, 2], ‸&\"id ${&1}\") |> Iter.to_list()\ndbg ids", "ids = Iter.map([1, 2], fn x -> \"id ${x}\") |> Iter.to_list()"},
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
		{pointDecl, "ps = Iter.map([1, 2], ‸fn x -> Point{x, y: 0}) |> Iter.to_list()\ndbg ps", "fn x -> Point{x, y: 0}"},
		{"fn scale(value: Int, by: Int) -> Int {\n    value * by\n}\n\n", "xs = Iter.map([1, 2], ‸fn value -> scale(value, 10)) |> Iter.to_list()\ndbg xs", "fn value -> scale(value, 10)"},
		{"", "add: (Int, Int) -> Int = ‸fn x, y -> x + y\ndbg add(1, 2)", "fn x, y -> x + y"},
		{studentDecls, studentsLine + "by_grade = ss |> Iter.group_by(‸fn x -> grade(x.score))\ndbg by_grade", "fn x -> grade(x.score)"},
		{"", "ids = Iter.map([1, 2], ‸fn x -> \"id ${x}\") |> Iter.to_list()\ndbg ids", "fn x -> \"id ${x}\""},
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

// A `tap` capture that only passes the piped value on becomes the function
// by name.
func TestTapByName_RewritesAPassingCapture(t *testing.T) {
	decls := "fn log_it(n: Int) {\n    io.print(n)\n}\n\n"
	for _, c := range []struct{ name, body, title, want string }{
		{"plain", "dbg 1 |> tap ‸&log_it(&1)", "Pass `log_it` by name", "dbg 1 |> tap log_it\n"},
		{"qualified", "dbg 1 |> tap &io.‸print(&1)", "Pass `io.print` by name", "dbg 1 |> tap io.print\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkRefactor(t, fnMainAfter(decls, c.body), c.title, protocol.CodeActionKindRefactorRewrite)
			if !strings.Contains(got, c.want) {
				t.Errorf("result lacks %q:\n%s", c.want, got)
			}
		})
	}
}

// A capture that does more than pass the value on keeps its form, and so
// does one outside a `tap` stage.
func TestTapByName_RefusesACaptureThatDoesMore(t *testing.T) {
	decls := "fn show(label: String, n: Int) {\n    io.print(\"${label} ${n}\")\n}\n\n"
	for _, body := range []string{
		"dbg 1 |> tap ‸&show(\"n\", &1)",
		"dbg 1 |> tap ‸&io.print(&1 + 1)",
		"show(\"a\", 1)\nxs = Iter.map([1], ‸&Int.abs(&1)) |> Iter.to_list()\ndbg xs",
	} {
		refuseRefactor(t, fnMainAfter(decls, body), "Pass `")
	}
}
