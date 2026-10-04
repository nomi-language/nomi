package analysis_test

import (
	"fmt"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

type wantDiag struct {
	line, col int
	msg       string
}

// checkDiagnostics analyzes src and requires its diagnostics to be exactly
// want, in any order. An empty want requires the source to be accepted.
func checkDiagnostics(t *testing.T, src string, want []wantDiag) {
	t.Helper()
	errs := diagnosticsFor(t, src)
	got := map[string]bool{}
	for _, e := range errs {
		got[fmt.Sprintf("%d:%d: %s", e.Line, e.Col, diagText(e))] = true
	}
	for _, w := range want {
		key := fmt.Sprintf("%d:%d: %s", w.line, w.col, w.msg)
		if !got[key] {
			t.Errorf("missing diagnostic %s", key)
		}
		delete(got, key)
	}
	for k := range got {
		if len(want) == 0 {
			t.Errorf("the front end rejects this source, which must be accepted: %s", k)
		} else {
			t.Errorf("unexpected diagnostic %s", k)
		}
	}
}

func TestRecursiveRender_DisplayRendersItsReceiver(t *testing.T) {
	src := `import std/io

struct Foo {
    name: String
}

impl Display for Foo {
    fn to_string(foo: Foo): String {
        a = Display.to_string(foo)
        b = Foo.to_string(foo)
        io.print(foo)
        same = foo
        c = foo |> Display.to_string()
        "${foo} ${same} ${a} ${b} ${c}"
    }
}

fn main() {
    io.print(Foo{name: "a"})
}
`
	const disp = "calls Display.to_string(foo), the function being defined, so it never returns\nhelp: Debug.inspect(foo) renders its fields"
	const direct = "calls the function being defined with its own receiver, so it never returns\nhelp: Debug.inspect(foo) renders its fields"
	checkDiagnostics(t, src, []wantDiag{
		{9, 13, "Display.to_string(foo) " + direct},
		{10, 13, "Foo.to_string(foo) " + direct},
		{11, 9, "io.print(foo) " + disp},
		{13, 13, "Display.to_string(foo) " + direct},
		{14, 12, `"${foo}" ` + disp},
		{14, 19, `"${same}" ` + disp},
	})
}

func TestRecursiveRender_DebugRendersItsReceiver(t *testing.T) {
	src := `import std/io

struct Foo {
    name: String
}

impl Debug for Foo {
    fn inspect(foo: Foo): String {
        io.inspect(foo)
        _ = dbg foo
        _ = Foo.inspect(foo)
        Debug.inspect(foo)
    }
}

fn main() {
    io.inspect(Foo{name: "a"})
}
`
	const dbg = "calls Debug.inspect(foo), the function being defined, so it never returns"
	const direct = "calls the function being defined with its own receiver, so it never returns"
	checkDiagnostics(t, src, []wantDiag{
		{9, 9, "io.inspect(foo) " + dbg},
		{10, 13, "dbg foo " + dbg},
		{11, 13, "Foo.inspect(foo) " + direct},
		{12, 9, "Debug.inspect(foo) " + direct},
	})
}

// Rendering a field, another value of the same type, or the receiver through
// the other interface terminates, and stays legal.
func TestRecursiveRender_TerminatingRenderingsAreAccepted(t *testing.T) {
	src := `import std/io

enum Tree {
    Leaf
    Node{left: Tree, value: Int, right: Tree}
}

impl Display for Tree {
    fn to_string(tree: Tree): String {
        case tree {
            .Leaf -> "."
            .Node{left, value, right} -> "(${left} ${value} ${right})"
        }
    }
}

struct Foo {
    name: String
}

impl Display for Foo {
    fn to_string(foo: Foo): String {
        "Foo ${foo.name} ${Debug.inspect(foo)}"
    }
}

impl Debug for Foo {
    fn inspect(foo: Foo): String {
        "<${foo}>"
    }
}

fn main() {
    io.print(Tree.Node{left: Tree.Leaf, value: 1, right: Tree.Leaf})
    io.print(Foo{name: "a"})
}
`
	checkDiagnostics(t, src, nil)
}

func TestUnconditionalRecursion_EveryPathRecurses(t *testing.T) {
	src := `import std/io

fn spin(x: Int): Int {
    spin(x)
}

fn grow(x: Int): Int {
    y = grow(x)
    y + 1
}

fn arms(x: Int): Int {
    case x {
        0 -> arms(x)
        _ -> arms(x)
    }
}

fn branches(x: Int, s: String): Int {
    io.print(s)
    if x > 0 { 1 + branches(x, s) } else { branches(s: s, x: x) - 1 }
}

fn piped(x: Int): Int {
    x |> piped
}

struct Foo {
    n: Int
}

impl Foo {
    fn again(foo: Foo): Int {
        Foo.again(foo)
    }
}

fn outer(x: Int): Int {
    fn inner(y: Int): Int {
        inner(y)
    }
    inner(x)
}

fn main() {
    io.print(spin(1) + grow(1) + arms(1) + branches(1, "s") + piped(1) + Foo.again(Foo{n: 1}) + outer(1))
}
`
	const tail = "with the arguments it was given, so it never returns"
	checkDiagnostics(t, src, []wantDiag{
		{3, 4, "every path through spin calls spin(x) " + tail},
		{7, 4, "every path through grow calls grow(x) " + tail},
		{12, 4, "every path through arms calls arms(x) " + tail},
		{19, 4, "every path through branches calls branches(x, s) " + tail},
		{24, 4, "every path through piped calls piped(x) " + tail},
		{33, 8, "every path through again calls Foo.again(foo) " + tail},
		{39, 8, "every path through inner calls inner(y) " + tail},
	})
}

// A path that returns without the call, changed arguments, a call inside a
// lambda (returned, or handed to Iter.reduce as in spec §12), a tail call
// after an effect (a deliberate loop), and mutual recursion are all accepted.
func TestUnconditionalRecursion_TerminatingOrDeliberateIsAccepted(t *testing.T) {
	src := `import std/io

fn base(x: Int): Int {
    if x == 0 { 0 } else { base(x) }
}

fn countdown(x: Int): Int {
    if x == 0 { 0 } else { countdown(x - 1) }
}

fn unbounded(x: Int): Int {
    unbounded(x - 1)
}

fn early(x: Int): Int {
    if x > 0 {
        return 0
    }
    early(x)
}

fn tried(x: Int): Result<Int, String> {
    _ = try check(x)
    tried(x)
}

fn check(x: Int): Result<Int, String> {
    if x > 0 { Ok(x) } else { Err("no") }
}

fn serve(x: Int): Int {
    io.print(x)
    serve(x)
}

fn ping(x: Int): Int {
    pong(x)
}

fn pong(x: Int): Int {
    ping(x)
}

fn later(x: Int): (Int) -> Int {
    |n| later(x)(n)
}

fn sum(xs: List<Int>): Int {
    Iter.reduce(xs, |acc = 0, _| acc + sum(xs))
}

fn main() {
    io.print(base(0) + countdown(3) + early(1) + ping(0) + serve(0) + unbounded(0))
    io.print(tried(1))
    io.print(later(1)(2) + sum([]))
}
`
	checkDiagnostics(t, src, nil)
}

func TestUnconditionalRecursion_Code(t *testing.T) {
	errs := diagnosticsFor(t, `fn spin(x: Int): Int {
    spin(x)
}

fn main() {
    _ = spin(1)
}
`)
	var codes []string
	for _, e := range errs {
		codes = append(codes, e.Code)
	}
	if len(codes) != 1 || codes[0] != analysis.UnconditionalRecursionCode {
		t.Fatalf("codes = %v, want [%s]", codes, analysis.UnconditionalRecursionCode)
	}
}
