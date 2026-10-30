package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A generic call's argument that no solution of the parameter's type
// variables makes fit is an argument mismatch at the argument
// (reportGenericArgMismatch). The checker used to drop such a failed
// unification unless the parameter was an interface or a record, so
// `weight(#(10, "x"))` against `#(Float, T)` checked and the IR builder then
// declined the call; the same argument to a parameter with no type parameter
// in it was rejected.

const genericSlotDecls = `struct Point {
    x: Int
}

struct Size {
    w: Int
}

fn weight<T>(pair: #(Float, T)) -> Float {
    pair.0
}

fn nested<T>(pair: #(#(Float, T), Int)) -> Float {
    pair.0.0
}

fn rest<T>(pairs: List<#(Float, T)>) -> Int {
    _ = pairs
    0
}

fn floats<T>(xs: List<Float>, tag: T) -> T {
    _ = xs
    tag
}

fn maybe<T>(m: Maybe<Float>, tag: T) -> T {
    _ = m
    tag
}

fn result<T>(r: Result<Float, T>) -> Bool {
    Result.is_ok(r)
}

fn record<T>(r: #{w: Float, t: T}) -> Float {
    r.w
}

fn first<T>(pair: #(Point, T)) -> Point {
    pair.0
}

fn label<T>(pair: #(String, T)) -> String {
    pair.0
}

fn walk<T>(xs: List<T>) -> Int {
    Iter.each(xs, fn _x -> 5)
    Iter.count(xs)
}

`

// genericSlotSource puts stmt on a line of its own, at column 5.
func genericSlotSource(stmt string) (src string, line int) {
	imports := ""
	if strings.Contains(stmt, "Generator.") {
		imports = "import std/random.Generator\n\n"
	}
	head := imports + genericSlotDecls + "fn main() {\n"
	return head + "    " + stmt + "\n    _ = x\n}\n", strings.Count(head, "\n") + 1
}

func TestGenericSlotConflict_MismatchIsRejectedAtTheArgument(t *testing.T) {
	for _, tc := range []struct {
		stmt string
		// arg is the text the error starts at.
		arg  string
		want string
	}{
		{`x = weight(#(10, "x"))`, `#(10`, "argument 1: expected #(Float, T), got #(Int, String)"},
		{`x = nested(#(#(10, "x"), 1))`, `#(#`, "argument 1: expected #(#(Float, T), Int), got #(#(Int, String), Int)"},
		{`x = rest([#(10, "x")])`, `[`, "argument 1: expected List<#(Float, T)>, got List<#(Int, String)>"},
		{`x = result(Ok(10))`, `Ok`, "argument 1: expected Result<Float, T>, got Result<Int, _>"},
		{`x = Generator.weighted(#(-10, "bad"), [])`, `#(-10`, "argument 1: expected #(Float, T), got #(Int, String)"},
		{`x = first(#(#Size{w: 1}, "x"))`, `#(#Size`, "argument 1: expected #(Point, T), got #(Size, String)"},
		{`x = label(#(#Point{x: 1}, 2))`, `#(#Point`, "argument 1: expected #(String, T), got #(Point, Int)"},
		{`x = first(#(1, "x"))`, `#(1`, "argument 1: expected #(Point, T), got #(Int, String)"},
		{`x = weight(#(1.0, "x", 2))`, `#(1.0`, "argument 1: expected #(Float, T), got #(Float, String, Int)"},
		{`x = record(#{w: 10, t: 1})`, `#{w`, "argument 1: expected #{w: Float, t: T}, got #{w: Int, t: Int}"},
		{`x = maybe(Some(10), 1)`, `Some`, "argument 1: expected Maybe<Float>, got Maybe<Int>"},
		{`x = floats([10], 1)`, `10]`, "list element type mismatch: expected Float, got Int"},
		{`x = Generator.weighted(#(-10.0, "bad"), [#(1, "x")])`, `#(1,`, "list element type mismatch: expected #(Float, String), got #(Int, String)"},
	} {
		src, line := genericSlotSource(tc.stmt)
		_, errs := checkSourceWithStdlib(src)
		expectStdlibError(t, errs, tc.want)
		col := 5 + strings.Index(tc.stmt, tc.arg)
		var at *analysis.TypeError
		for i := range errs {
			if strings.Contains(errs[i].Message, tc.want) {
				at = &errs[i]
				break
			}
		}
		if at.Line != line || at.Col != col {
			t.Errorf("%s: error at %d:%d, want %d:%d (%q)", tc.stmt, at.Line, at.Col, line, col, tc.arg)
		}
	}
}

// The mirror: the right types in those slots check, and so do the two
// failed unifications reportGenericArgMismatch leaves alone: a callback
// whose result a Unit-result parameter discards, inside a generic body
// (walk's `Iter.each(xs, fn _x -> 5)`), and a patch whose Partial<self> is
// not solved yet.
func TestGenericSlotConflict_MatchingArgumentsAreAccepted(t *testing.T) {
	for _, stmt := range []string{
		`x = weight(#(10.0, "x"))`,
		`x = weight(#(10.0, 1))`,
		`x = nested(#(#(10.0, "x"), 1))`,
		`x = rest([#(10.0, "x"), #(-1.5, "y")])`,
		`x = floats([10.0], 1)`,
		`x = maybe(Some(10.0), 1)`,
		`x = result(Ok(10.0))`,
		`x = record(#{w: 10.0, t: 1})`,
		`x = first(#(#Point{x: 1}, "x"))`,
		`x = label(#("a", #Size{w: 1}))`,
		`x = walk([1, 2])`,
		// A patch named before the struct that solves its Partial<self>.
		`x = Struct.update(updates: #{x: 2}, original: #Point{x: 1})`,
		`x = Generator.weighted(#(-10.0, "bad"), [])`,
	} {
		src, _ := genericSlotSource(stmt)
		_, errs := checkSourceWithStdlib(src)
		expectNoStdlibErrors(t, errs)
	}
}
