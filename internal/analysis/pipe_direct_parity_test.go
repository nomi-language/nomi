package analysis_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// pipeParityDecls are the declarations the cases of
// TestPipeStage_RecordsWhatTheDirectCallRecords call.
const pipeParityDecls = `import std::regex.Regex
import std::testing

enum Shape
    Circle(Float)
    Segment((Int, Int))
    Rect {w: Int, h: Int}
    Dot
end

type UserId Int

type Handler (Int) -> Int

type Celsius Int

struct Point
    x: Int
    y: Int
end

impl Add<Point, Point> for Point
    fn add(lhs: Point, rhs: Point) -> Point
        Point{x: lhs.x + rhs.x, y: lhs.y + rhs.y}
    end
end

fn add_to(a: Int, b: Int = 10) -> Int
    a + b
end

fn apply(x: Int, scale: Int = 1, f: (Int) -> Int) -> Int
    f(x) * scale
end

fn none_like<T>(n: Int) -> Option<T>
    if n > 0 then None else None
end

fn call_with<F>(g: (F, Int) -> Int, f: F) -> Int
    g(f, 1)
end

fn double(n: Int) -> Int
    n * 2
end

interface Sized
    fn size(s: self) -> Int
end

impl Sized for Point
    fn size(p: Point) -> Int
        p.x * p.y
    end
end

fn sized_twice<T>(t: T) -> Int where T: Sized
    T.size(t) * 2
end

`

// A pipe stage is the call it stands for: `x |> f(a)` is `f(x, a)` (spec §8
// *Pipes*). Each row writes a body both ways, under one header, and every
// call in the two bodies must record the same callee, the same type
// arguments and instantiated signature, the same filling of each parameter
// (a pipe's head as its stage's first argument), the same defaults and the
// same type, and the two must give the same diagnostics. Where the spec
// makes a stage differ, the row's pipedErrs says what the stage reports and
// cites the rule.
func TestPipeStage_RecordsWhatTheDirectCallRecords(t *testing.T) {
	for _, tc := range []struct {
		name, header, direct, piped string
		// errs is the diagnostics both forms give (pipeParityRecord
		// normalizes them); nil for none.
		errs []string
		// pipedErrs, when set, is what the stage reports where the spec
		// words its diagnostic differently from the direct call's.
		pipedErrs []string
	}{
		{name: "Iter.each_while with a lambda", header: "fn f(xs: List<Int>) -> Bool",
			direct: "Iter.each_while(xs, fn x -> x < 3)",
			piped:  "xs |> Iter.each_while(fn x -> x < 3)"},
		{name: "an interface function specialised from its first argument", header: "fn f(xs: List<String>) -> List<Int>",
			direct: "Iter.to_list(Iter.map(xs, String.length))",
			piped:  "xs |> Iter.map(String.length) |> Iter.to_list()"},
		{name: "a trailing lambda routed to the last parameter", header: "fn f() -> Int",
			direct: "apply(2, fn n -> n + 1)",
			piped:  "2 |> apply(fn n -> n + 1)"},
		{name: "a default argument", header: "fn f() -> Int",
			direct: "add_to(1)",
			piped:  "1 |> add_to()"},
		{name: "a named argument", header: "fn f() -> Int",
			direct: "add_to(1, b: 2)",
			piped:  "1 |> add_to(b: 2)"},
		{name: "an unknown named argument", header: "fn f() -> Int",
			direct: "add_to(1, c: 2)",
			piped:  "1 |> add_to(c: 2)",
			errs:   []string{"no parameter named 'c'"}},
		{name: "an operator interface's function", header: "fn f(p: Point, q: Point) -> Point",
			direct: "Add.add(p, q)",
			piped:  "p |> Add.add(q)"},
		{name: "a std operator interface's function", header: "fn f() -> Int",
			direct: "Add.add(1, 2)",
			piped:  "1 |> Add.add(2)"},
		{name: "an impl chosen among one type's impls of a generic interface", header: "fn f(s: String) -> Bool",
			direct: "String.contains(s, Regex`a+`)",
			piped:  "s |> String.contains(Regex`a+`)"},
		{name: "a variant constructor", header: "fn f() -> Shape",
			direct: "Shape.Circle(2.0)",
			piped:  "2.0 |> Shape.Circle()"},
		{name: "a prelude variant constructor", header: "fn f() -> Option<Int>",
			direct: "Some(2)",
			piped:  "2 |> Some()"},
		{name: "a flat tuple-payload variant", header: "fn f() -> Shape",
			direct: "Shape.Segment(1, 2)",
			piped:  "1 |> Shape.Segment(2)"},
		{name: "a struct-shaped variant's record call form", header: "fn f() -> Shape",
			direct: "Shape.Rect({w: 1, h: 2})",
			piped:  "{w: 1, h: 2} |> Shape.Rect()"},
		{name: "a distinct type's constructor", header: "fn f() -> UserId",
			direct: "UserId(5)",
			piped:  "5 |> UserId()"},
		{name: "a value of a function-wrapping distinct type called", header: "fn f(h: Handler) -> Int",
			direct: "h(5)",
			piped:  "5 |> h()"},
		{name: "a callee an inference variable stands for", header: "fn f() -> Int",
			direct: "call_with(fn g, n -> g(n), double)",
			piped:  "call_with(fn g, n -> n |> g(), double)"},
		{name: "a generic function solved from the expected type", header: "fn f() -> Option<String>",
			direct: "none_like(1)",
			piped:  "1 |> none_like()"},
		{name: "a generic function under a bound", header: "fn f(p: Point) -> Int",
			direct: "sized_twice(p)",
			piped:  "p |> sized_twice()"},
		{name: "a type parameter's function", header: "fn f<T>(t: T) -> Int where T: Sized",
			direct: "T.size(t)",
			piped:  "t |> T.size()"},
		{name: "an interface function through its interface", header: "fn f(p: Point) -> Int",
			direct: "Sized.size(p)",
			piped:  "p |> Sized.size()"},
		{name: "a type's own function", header: "fn f(p: Point) -> Int",
			direct: "Point.size(p)",
			piped:  "p |> Point.size()"},
		{name: "a struct's call form", header: "fn f() -> Point",
			direct: "Point({x: 1, y: 2})",
			piped:  "{x: 1, y: 2} |> Point()"},
		{name: "the receiver's own function by its bare name", header: "impl Display for Point\n    fn to_string(p: Point) -> String\n        PLACEHOLDER\n    end\nend\n\nfn f() -> Int",
			direct: "_ = size(p)\n        \"p\"",
			piped:  "_ = p |> size()\n        \"p\"",
			errs:   []string{}},
		{name: "an interface function called by its bare name", header: "pub interface Speech\n    fn speak(s: self) -> String\nend\n\nimpl Speech for Point\n    fn speak(p: Point) -> String\n        \"${p.x}\"\n    end\nend\n\nfn f(p: Point) -> String",
			direct: "speak(p)",
			piped:  "p |> speak()",
			errs:   []string{"bare-name dispatch is not supported: \"speak\" is an interface function (of Speech); qualify the call as Speech.speak(...) or Type.speak(...)"}},
		{name: "an undefined function's arguments are checked", header: "fn f() -> Int",
			direct: "nowhere(1, elsewhere)",
			piped:  "1 |> nowhere(elsewhere)",
			errs:   []string{"undefined variable 'nowhere'", "undefined variable 'elsewhere'"}},
		{name: "testing.check of a value it cannot judge", header: "fn f() -> Int",
			direct: "_ = testing.check(5)\n    0",
			piped:  "_ = 5 |> testing.check()\n    0",
			errs:   []string{"testing.check expects Bool, Result, Option, or Assertable, got Int"}},
		{name: "testing.check of an Option", header: "fn f() -> Int",
			direct: "_ = testing.check(Some(5))\n    0",
			piped:  "_ = Some(5) |> testing.check()\n    0"},
		{name: "testing.check given two values", header: "fn f() -> Int",
			direct: "_ = testing.check(True, False)\n    0",
			piped:  "_ = True |> testing.check(False)\n    0",
			errs:   []string{"testing.check expects 1 argument, got 2"}},
		{name: "a type name is no function", header: "fn f() -> Int",
			direct: `Int("4")`,
			piped:  `"4" |> Int()`,
			errs:   []string{"`Int` is a type, not a function"}},
		{name: "a value that is no function", header: "fn f(n: Int) -> Int",
			direct: "n(1)",
			piped:  "1 |> n()",
			errs:   []string{"`Int` is not a function"}},
		{name: "a variant with no payload", header: "fn f() -> Shape",
			direct: "Shape.Dot(1)",
			piped:  "1 |> Shape.Dot()",
			errs:   []string{"variant Shape.Dot carries no data; write Shape.Dot without parentheses"}},
		{name: "a positional variant's named payload", header: "fn f() -> Option<Int>",
			direct: "Some(l: 1)",
			piped:  "1 |> Some(l: 1)",
			errs:   []string{"no parameter named 'l'"}},
		{name: "a function value's parameters have no defaults", header: "fn f(g: (Int, Int) -> Int) -> Int",
			direct: "g(1)",
			piped:  "1 |> g()",
			errs:   []string{"expected 2 arguments, got 1"}},
		{name: "too many arguments", header: "fn f() -> Int",
			direct: "add_to(1, 2, 3)",
			piped:  "1 |> add_to(2, 3)",
			errs:   []string{"expected 1 to 2 arguments, got 3"}},
		// Spec §8 *Pipes*, "A stage that does not take the value": the
		// error names the argument the value fills.
		{name: "an argument of the wrong type", header: "fn f() -> Int",
			direct:    `add_to("x")`,
			piped:     `"x" |> add_to()`,
			errs:      []string{"argument 1: expected Int, got String"},
			pipedErrs: []string{"`add_to` takes an Int as its first argument, but the pipe passes a String"}},
		{name: "testing.check", header: "fn f(b: Bool) -> Bool",
			direct: "_ = testing.check(b)\n    b",
			piped:  "_ = b |> testing.check()\n    b"},
		{name: "the implementor's constructor in an interface default", header: "interface Scaled\n    fn scale(self(n), by: Int) -> self where self: Distinct<Int>\n        PLACEHOLDER\n    end\nend\n\nimpl Scaled for Celsius end\n\nfn f() -> Int",
			direct: "self(n * by)",
			piped:  "n * by |> self()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direct := pipeParityRecord(t, tc.header, tc.direct)
			piped := pipeParityRecord(t, tc.header, tc.piped)
			if direct.record != piped.record {
				t.Errorf("the two forms record differently\ndirect %s:\n%s\npiped %s:\n%s", tc.direct, direct.record, tc.piped, piped.record)
			}
			want := strings.Join(tc.errs, "\n")
			if direct.errs != want {
				t.Errorf("direct %s: diagnostics\n%s\nwant\n%s", tc.direct, direct.errs, want)
			}
			if tc.pipedErrs != nil {
				want = strings.Join(tc.pipedErrs, "\n")
			}
			if piped.errs != want {
				t.Errorf("piped %s: diagnostics\n%s\nwant\n%s", tc.piped, piped.errs, want)
			}
		})
	}
}

// A stage of a function another file declares is the direct call too: the
// variant constructor of another file's opaque enum is refused through a
// pipe as it is when called, and an imported function and variant are
// called as the functions they are.
func TestPipeStage_AcrossFilesIsTheDirectCall(t *testing.T) {
	const status = `pub opaque enum Status
    Active
    Inactive(String)
end

pub enum Shape
    Dot(Int)
end

pub fn inactive(reason: String) -> Status
    Status.Inactive(reason)
end

pub fn twice(n: Int) -> Int
    n * 2
end
`
	for _, tc := range []struct {
		name, imports, direct, piped string
		errs                         []string
	}{
		{name: "an opaque enum's variant", imports: "import status.{Status}",
			direct: `_ = Status.Inactive("r")`,
			piped:  `_ = "r" |> Status.Inactive()`,
			errs:   []string{"constructor of opaque type 'Status' is private to its defining module — use an exported constructor function"}},
		{name: "an imported function", imports: "import status.{twice}",
			direct: "_ = twice(2)",
			piped:  "_ = 2 |> twice()"},
		{name: "an imported variant", imports: "import status.Shape.{Dot}",
			direct: "_ = Dot(2)",
			piped:  "_ = 2 |> Dot()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := func(stmt string) string {
				_, errs := opaqueProject(t, map[string]string{
					"status.nomi": status,
					"main.nomi":   tc.imports + "\n\nfn main()\n    " + stmt + "\nend\n",
				})
				var msgs []string
				for _, e := range withoutUnusedBindingErrors(errs) {
					msgs = append(msgs, e.Message)
				}
				return strings.Join(msgs, "\n")
			}
			want := strings.Join(tc.errs, "\n")
			if got := check(tc.direct); got != want {
				t.Errorf("direct %s: diagnostics\n%s\nwant\n%s", tc.direct, got, want)
			}
			if got := check(tc.piped); got != want {
				t.Errorf("piped %s: diagnostics\n%s\nwant\n%s", tc.piped, got, want)
			}
		})
	}
}

type pipeParityResult struct {
	record, errs string
}

// pipeParityRecord checks header with body below it, and describes what the
// checker recorded for each call in body, in order: a pipe into a call is
// described as that call, its head as the first argument.
func pipeParityRecord(t *testing.T, header, body string) pipeParityResult {
	t.Helper()
	var src string
	if strings.Contains(header, "PLACEHOLDER") {
		src = pipeParityDecls + strings.Replace(header, "PLACEHOLDER", body, 1) + "\n    0\nend\n"
	} else {
		src = pipeParityDecls + header + "\n    " + body + "\nend\n"
	}
	fa, nodes, errs := checkSourceNodesWithStdlib(src)
	var out []string
	stages := map[*ast.Call]bool{}
	for _, n := range nodes {
		ast.Inspect(n, func(x ast.Node) bool {
			if fn, ok := x.(*ast.FuncDef); ok && fn.LineNum() <= strings.Count(pipeParityDecls, "\n") {
				return false
			}
			var call *ast.Call
			shift := 0
			switch v := x.(type) {
			case *ast.Call:
				if stages[v] {
					return true
				}
				call = v
			case *ast.Binary:
				if c, ok := v.Right.(*ast.Call); ok && v.Op == "|>" {
					call, shift = c, 1
					stages[c] = true
				}
			}
			if call != nil {
				out = append(out, pipeParityCall(fa, x, call, shift))
			}
			return true
		})
	}
	var msgs []string
	for _, e := range errs {
		if strings.Contains(e.Message, "unused") {
			continue
		}
		msgs = append(msgs, strings.ReplaceAll(e.Message, " (counting the piped value)", ""))
	}
	return pipeParityResult{record: strings.Join(out, "\n"), errs: strings.Join(msgs, "\n")}
}

// pipeParityCall describes what the checker recorded for call, written at
// expr (the call, or the pipe into it, whose head shifts its arguments by
// shift).
func pipeParityCall(fa *analysis.FileAnalysis, expr ast.Node, call *ast.Call, shift int) string {
	out := []string{"call " + callText(call.Func), "  type: " + typeText(fa.ExprTypes[expr])}
	if rc := fa.Calls[call]; rc != nil {
		out = append(out, "  callee: "+calleeText(rc.Callee))
		var slots []string
		for _, s := range rc.Slots {
			switch s.Fill {
			case analysis.SlotPiped:
				slots = append(slots, "arg0")
			case analysis.SlotArg:
				slots = append(slots, fmt.Sprintf("arg%d", s.Arg+shift))
			case analysis.SlotHole:
				slots = append(slots, fmt.Sprintf("hole%d", s.Arg+shift))
			case analysis.SlotDefault:
				slots = append(slots, "default")
			default:
				slots = append(slots, fmt.Sprintf("fill%d", s.Fill))
			}
		}
		out = append(out, "  slots: "+strings.Join(slots, ", "))
	} else {
		out = append(out, "  no call record")
	}
	if dc := fa.CallDefaults[call]; dc != nil {
		out = append(out, fmt.Sprintf("  defaults: %s %v", declText(dc.Decl), dc.Slots))
	}
	return strings.Join(out, "\n")
}

func callText(fn ast.Node) string {
	switch f := fn.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.TypeIdent:
		return f.Name
	case *ast.FieldAccess:
		if f.Field != nil {
			return callText(f.Object) + "." + f.Field.Name
		}
	}
	return fmt.Sprintf("%T", fn)
}

func declText(n ast.Node) string {
	if n == nil {
		return "<none>"
	}
	line := 0
	if p, ok := n.(interface{ LineNum() int }); ok {
		line = p.LineNum()
	}
	return fmt.Sprintf("%T@%d", n, line)
}

func calleeText(c analysis.Callee) string {
	s := fmt.Sprintf("kind %d decl %s impl %s form %d", c.Kind, declText(c.Decl), declText(c.Impl), c.Form)
	if c.For != nil {
		s += " for " + c.For.String()
	}
	if c.Ref != nil {
		s += " sig " + typeText(c.Ref.CallType)
		var args []string
		for name, ty := range c.Ref.CallTypeArgs {
			args = append(args, name+"="+typeText(ty))
		}
		sort.Strings(args)
		s += " targs [" + strings.Join(args, " ") + "]"
		if c.Ref.CallInterface != nil {
			s += " through " + c.Ref.CallInterface.String()
		}
	}
	return s
}
