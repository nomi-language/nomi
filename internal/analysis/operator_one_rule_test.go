package analysis_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// oneRuleFamily is an operator family as the one-rule table asks it: the
// operator written, the interface function it goes through, the bound a
// generic body over it is written with, and how a function of the
// family's operands and result is headed.
type oneRuleFamily struct {
	op, call string
	unary    bool
	// bound is the generic body's where clause over P (and R, O).
	bound string
	// result is the written operator's result type for operand types t,
	// r and o; called the call form's.
	result, called func(t, r, o string) string
}

var oneRuleFamilies = map[string]oneRuleFamily{
	"==": {op: "==", call: "Equatable.equal", bound: "P: Equatable",
		result: func(string, string, string) string { return "Bool" }, called: func(string, string, string) string { return "Bool" }},
	"!=": {op: "!=", call: "Equatable.equal", bound: "P: Equatable",
		result: func(string, string, string) string { return "Bool" }, called: func(string, string, string) string { return "Bool" }},
	"<": {op: "<", call: "Comparable.compare", bound: "P: Comparable",
		result: func(string, string, string) string { return "Bool" }, called: func(string, string, string) string { return "Ordering" }},
	"+": {op: "+", call: "Add.add", bound: "P: Add<R, O>",
		result: func(_, _, o string) string { return o }, called: func(_, _, o string) string { return o }},
	"&&&": {op: "&&&", call: "BitwiseAnd.bitwise_and", bound: "P: BitwiseAnd<R, O>",
		result: func(_, _, o string) string { return o }, called: func(_, _, o string) string { return o }},
	"~~~": {op: "~~~", call: "BitwiseNot.bitwise_not", unary: true, bound: "P: BitwiseNot<O>",
		result: func(_, _, o string) string { return o }, called: func(_, _, o string) string { return o }},
}

// oneRuleShape is an operand type: its type (and, for an operator over two
// types, the right operand's and the result's), and the families it takes.
// Every shape's declarations are oneRuleDecls.
type oneRuleShape struct {
	name     string
	t, r, o  string
	families []string
}

const oneRuleDecls = `struct DS
    n: Int
end

derive Equatable for DS
derive Comparable for DS

enum DE
    A
    B(Int)
end

derive Equatable for DE
derive Comparable for DE

type Id Int

struct PI
    n: Int
end

impl Equatable for PI
    fn equal(a: PI, b: PI) -> Bool
        a.n == b.n
    end
end

impl Comparable for PI
    fn compare(a: PI, b: PI) -> Ordering
        Int.compare(a.n, b.n)
    end
end

impl Add<PI, PI> for PI
    fn add(lhs: PI, rhs: PI) -> PI
        PI{n: lhs.n + rhs.n}
    end
end

impl BitwiseNot<PI> for PI
    fn bitwise_not(value: PI) -> PI
        PI{n: ~~~value.n}
    end
end

interface Named
    fn name(value: self) -> String
end

type Day Int
type Days Int
type Weeks Int

impl Add<Days, Day> for Day
    fn add(lhs: Day, rhs: Days) -> Day
        Day(a) = lhs
        Days(b) = rhs
        Day(a + b)
    end
end

impl Add<Weeks, Day> for Day
    fn add(lhs: Day, rhs: Weeks) -> Day
        Day(a) = lhs
        Weeks(b) = rhs
        Day(a + b * 7)
    end
end
`

var oneRuleShapes = []oneRuleShape{
	{name: "Int", t: "Int", families: []string{"==", "<", "+", "&&&", "~~~"}},
	{name: "Decimal", t: "Decimal", families: []string{"==", "<", "+"}},
	{name: "String", t: "String", families: []string{"==", "<", "+"}},
	{name: "Bool", t: "Bool", families: []string{"==", "<"}},
	{name: "Unit", t: "Unit", families: []string{"=="}},
	{name: "List", t: "List<Int>", families: []string{"==", "<", "+"}},
	{name: "Vector", t: "Vector<Int>", families: []string{"==", "<", "+"}},
	{name: "Set", t: "Set<Int>", families: []string{"==", "+"}},
	{name: "Map", t: "Map<String, Int>", families: []string{"==", "+"}},
	{name: "Option", t: "Option<Int>", families: []string{"=="}},
	{name: "Result", t: "Result<Int, String>", families: []string{"=="}},
	{name: "tuple", t: "(Int, String)", families: []string{"==", "<"}},
	{name: "anonymous struct", t: "{x: Int}", families: []string{"=="}},
	{name: "derived struct", t: "DS", families: []string{"==", "<"}},
	{name: "derived enum", t: "DE", families: []string{"==", "<"}},
	{name: "distinct", t: "Id", families: []string{"=="}},
	{name: "program impl", t: "PI", families: []string{"==", "<", "+", "~~~"}},
	{name: "interface value", t: "Named", families: []string{"==", "!="}},
	{name: "several Add impls", t: "Day", r: "Days", o: "Day", families: []string{"+"}},
	{name: "several Add impls, the second", t: "Day", r: "Weeks", o: "Day", families: []string{"+"}},
}

// oneRuleSource is the program a row is asked in: the operator written at
// the row's types (fn written), inside a generic body (fn generic) that
// fn instance calls at them, and its interface's function called by name
// (fn called).
func oneRuleSource(f oneRuleFamily, s oneRuleShape) string {
	t, r, o := s.t, s.r, s.o
	if r == "" {
		r = t
	}
	if o == "" {
		o = t
	}
	params, gparams, args := fmt.Sprintf("a: %s, b: %s", t, r), "a: P, b: R", "a, b"
	expr := "a " + f.op + " b"
	if f.unary {
		params, gparams, args, expr = "a: "+t, "a: P", "a", f.op+"a"
	}
	gresult := f.result("P", "R", "O")
	if f.result(t, r, o) == "Bool" {
		gresult = "Bool"
	}
	header := "fn generic<P>"
	if strings.Contains(f.bound, "R") || strings.Contains(f.bound, "O") {
		header = "fn generic<P, R, O>"
	}
	if f.unary {
		header = "fn generic<P, O>"
	}
	if !strings.Contains(f.bound, "R") && !f.unary {
		gparams = "a: P, b: P"
	}
	return fmt.Sprintf(`%s
fn written(%s) -> %s
    %s
end

%s(%s) -> %s where %s
    %s
end

fn instance(%s) -> %s
    generic(%s)
end

fn called(%s) -> %s
    %s(%s)
end
`, oneRuleDecls, params, f.result(t, r, o), expr,
		header, gparams, gresult, f.bound, expr,
		params, f.result(t, r, o), args,
		params, f.called(t, r, o), f.call, args)
}

// The one rule (interfaceUse): an operator written at a type, the same
// operator in a generic body at an instance of that type, and the
// operator's interface function called by name on values of it are
// answered alike, for every operator family and operand shape. `in` goes
// through no interface and has no record.
func TestOperatorForm_OneRuleWhereverAsked(t *testing.T) {
	lib := loadStd()
	agreed := map[string]bool{}
	for _, s := range oneRuleShapes {
		for _, name := range s.families {
			row := name + " on " + s.name
			f := oneRuleFamilies[name]
			src := oneRuleSource(f, s)
			nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
			fa, _, _ := analysis.BuildProjectWithCache(nodes, lib.Primitives, lib.Modules, lib.Files, t.TempDir(), nil)
			if errs := withoutUnreadParams(append(fa.TypeErrors, analysis.CheckTypes(fa, nodes)...)); len(errs) > 0 {
				t.Errorf("%s: the checker rejects the row's program: %v", row, errs)
				continue
			}
			wuse, wfound := oneRuleOperator(fa, oneRuleFunc(nodes, "written"), f)
			written := oneRuleDescribe(fa, wuse, wfound)
			iuse, ifound := oneRuleAtInstance(t, fa, nodes, f)
			instance := oneRuleDescribe(fa, iuse, ifound)
			called := "no call record"
			if c := oneRuleCall(oneRuleFunc(nodes, "called")); c != nil && fa.Calls[c] != nil {
				called = oneRuleDescribeCallee(fa, fa.Calls[c].Callee)
			}
			if written != instance || instance != called {
				t.Errorf("%s: written %s; at an instance %s; called %s", row, written, instance, called)
			} else {
				agreed[row] = true
			}
			t.Logf("%s: %s", row, written)
		}
	}
	rows := make([]string, 0, len(agreed))
	for r := range agreed {
		rows = append(rows, r)
	}
	sort.Strings(rows)
	t.Logf("%d rows agree", len(rows))
}

// oneRuleFunc is the module-level function name declares.
func oneRuleFunc(nodes []ast.Node, name string) *ast.FuncDef {
	for _, n := range nodes {
		if fd, ok := n.(*ast.FuncDef); ok && fd.Name == name {
			return fd
		}
	}
	return nil
}

// oneRuleOperator is the family's operator in fn.
func oneRuleOperator(fa *analysis.FileAnalysis, fn *ast.FuncDef, f oneRuleFamily) (*analysis.OperatorUse, bool) {
	var found ast.Node
	if fn != nil {
		ast.Inspect(fn, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Binary:
				if !f.unary && n.Op == f.op && found == nil {
					found = n
				}
			case *ast.Unary:
				if f.unary && n.Op == f.op && found == nil {
					found = n
				}
			}
			return found == nil
		})
	}
	if found == nil {
		return nil, false
	}
	return fa.Operators[found], true
}

// oneRuleCall is the call fn's body makes.
func oneRuleCall(fn *ast.FuncDef) *ast.Call {
	var found *ast.Call
	if fn != nil {
		ast.Inspect(fn, func(n ast.Node) bool {
			if c, ok := n.(*ast.Call); ok && found == nil {
				found = c
			}
			return found == nil
		})
	}
	return found
}

// oneRuleAtInstance is the generic body's operator at the instance fn
// instance calls, as OperatorUseAt answers it.
func oneRuleAtInstance(t *testing.T, fa *analysis.FileAnalysis, nodes []ast.Node, f oneRuleFamily) (*analysis.OperatorUse, bool) {
	t.Helper()
	c := oneRuleCall(oneRuleFunc(nodes, "instance"))
	if c == nil || fa.Calls[c] == nil || fa.Calls[c].Callee.Ref == nil {
		return nil, false
	}
	args := fa.Calls[c].Callee.Ref.CallTypeArgs
	var op ast.Node
	ast.Inspect(oneRuleFunc(nodes, "generic"), func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Binary:
			if !f.unary && n.Op == f.op {
				op = n
			}
		case *ast.Unary:
			if f.unary && n.Op == f.op {
				op = n
			}
		}
		return op == nil
	})
	use, resolved := fa.OperatorUseAt(fa, op, func(p *analysis.TypeParam_) (analysis.Type, bool) {
		ty, ok := args[p.Name_]
		return ty, ok && ty != nil
	})
	if !resolved {
		return nil, false
	}
	return use, true
}

var oneRuleFormNames = map[analysis.OperatorForm]string{
	analysis.OperatorByImpl: "by impl", analysis.OperatorStructural: "structural",
	analysis.OperatorDispatched: "dispatched", analysis.OperatorPerInstance: "per instance",
}

// oneRuleDescribe names an operator's answer: its form, and for an impl
// the function, by name, position and std identity.
func oneRuleDescribe(fa *analysis.FileAnalysis, use *analysis.OperatorUse, found bool) string {
	switch {
	case !found:
		return "not found"
	case use == nil:
		return "no record"
	case use.Form == analysis.OperatorByImpl && use.Impl != nil:
		return "by impl " + oneRuleMethod(fa, use.Impl.Method)
	}
	return oneRuleFormNames[use.Form]
}

// oneRuleDescribeCallee names a call's answer as oneRuleDescribe names an
// operator's.
func oneRuleDescribeCallee(fa *analysis.FileAnalysis, c analysis.Callee) string {
	switch {
	case c.Form == 0:
		return "no record"
	case c.Form == analysis.OperatorByImpl:
		m, _ := c.Decl.(ast.ImplMethodDecl)
		return "by impl " + oneRuleMethod(fa, m)
	}
	return oneRuleFormNames[c.Form]
}

func oneRuleMethod(fa *analysis.FileAnalysis, m ast.ImplMethodDecl) string {
	switch d := m.(type) {
	case *ast.FuncDef:
		synth := ""
		if analysis.IsSynthesizedLine(d.Line) {
			synth = " derived"
		}
		return fmt.Sprintf("fn %s %d:%d%s %v", d.Name, d.Line, d.Col, synth, fa.StdFuncOf(d))
	case *ast.ExternFunc:
		return fmt.Sprintf("host fn %s %d:%d %v", d.Name, d.Line, d.Col, fa.StdFuncOf(d))
	}
	return fmt.Sprintf("%T", m)
}

// An operator over a type parameter whose instance's type implements the
// operator's interface at several type arguments reaches, at the
// instance, the block of the arguments its operands and result have
// there: `a + b` over `P: Add<R, O>` at Day, Weeks and Day is Day's
// `impl Add<Weeks, Day>`, not its `impl Add<Days, Day>`.
func TestOperatorUseAt_NarrowsByTheInterfacesArguments(t *testing.T) {
	src := oneRuleSource(oneRuleFamilies["+"], oneRuleShape{t: "Day", r: "Weeks", o: "Day"})
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	lib := loadStd()
	fa, _, _ := analysis.BuildProjectWithCache(nodes, lib.Primitives, lib.Modules, lib.Files, t.TempDir(), nil)
	if errs := withoutUnreadParams(append(fa.TypeErrors, analysis.CheckTypes(fa, nodes)...)); len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	var want *ast.FuncDef
	for _, n := range nodes {
		if ib, ok := n.(*ast.ImplBlock); ok && ib.Interface.TypeString() == "Add<Weeks, Day>" {
			want, _ = ib.Items[0].(*ast.FuncDef)
		}
	}
	if want == nil {
		t.Fatal("no `impl Add<Weeks, Day> for Day` in the source")
	}
	use, resolved := oneRuleAtInstance(t, fa, nodes, oneRuleFamilies["+"])
	if !resolved || use == nil || use.Form != analysis.OperatorByImpl || use.Impl == nil || use.Impl.Method != want {
		t.Fatalf("`a + b` at Day, Weeks and Day: %+v (resolved %v), want the `fn add` of `impl Add<Weeks, Day> for Day` (line %d)", use, resolved, want.Line)
	}
}

// `==` and `!=` between an interface value and a concrete value compare
// the two as interface values, on either side (spec §12 *Interface
// values*): the record is OperatorDispatched whichever operand is the
// interface value, as the builder lowers it. The one-rule table cannot
// ask these: `Equatable.equal` is not called on an interface value, and a
// generic body's two operands have one type. An ordering operator there
// is an error.
func TestOperatorForm_InterfaceValueAndConcreteValueDispatch(t *testing.T) {
	var src strings.Builder
	src.WriteString(oneRuleDecls)
	concretes := []struct{ name, t string }{
		{"tuple", "(Int, String)"}, {"Int", "Int"}, {"derived struct", "DS"}, {"program impl", "PI"},
	}
	type row struct{ name, fn string }
	var rows []row
	for i, c := range concretes {
		for j, op := range []string{"==", "!="} {
			for k, order := range []string{"a %s b", "b %s a"} {
				fn := fmt.Sprintf("f%d_%d_%d", i, j, k)
				expr := fmt.Sprintf(order, op)
				fmt.Fprintf(&src, "\nfn %s(a: Equatable, b: %s) -> Bool\n    %s\nend\n", fn, c.t, expr)
				rows = append(rows, row{fmt.Sprintf("%s with a: Equatable, b: %s", expr, c.name), fn})
			}
		}
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src.String()))
	lib := loadStd()
	fa, _, _ := analysis.BuildProjectWithCache(nodes, lib.Primitives, lib.Modules, lib.Files, t.TempDir(), nil)
	if errs := withoutUnreadParams(append(fa.TypeErrors, analysis.CheckTypes(fa, nodes)...)); len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	for _, r := range rows {
		var op *ast.Binary
		ast.Inspect(oneRuleFunc(nodes, r.fn), func(n ast.Node) bool {
			if b, ok := n.(*ast.Binary); ok && op == nil {
				op = b
			}
			return op == nil
		})
		if got := oneRuleDescribe(fa, fa.Operators[op], op != nil); got != "dispatched" {
			t.Errorf("%s: %s, want dispatched", r.name, got)
		}
	}
	t.Logf("%d rows dispatched", len(rows))

	ordering := oneRuleDecls + "\nfn f(a: Equatable, b: Int) -> Bool\n    b < a\nend\n"
	nodes, _ = parser.ParseWithRecovery(lexer.Lex(ordering))
	fa, _, _ = analysis.BuildProjectWithCache(nodes, lib.Primitives, lib.Modules, lib.Files, t.TempDir(), nil)
	errs := withoutUnreadParams(append(fa.TypeErrors, analysis.CheckTypes(fa, nodes)...))
	if len(errs) == 0 {
		t.Fatal("the checker admits `b < a` between an Int and an Equatable")
	}
	const want = "`a` is an `Equatable`, and Equatable does not require Comparable of its implementors: values of different types have no order"
	if errs[0].Message != want {
		t.Errorf("`b < a`: %v, want %q", errs, want)
	}
}

// A comparison between an embedded type's value and the enum that embeds
// it is answered at the enum, on either side, since the narrower value
// widens into it: `c == s` and `s != c` both record Shape's Equatable, as
// the builder compares the two as Shapes. The impls are written, since a
// derived one writes no function and is answered by shape.
func TestOperatorForm_AnEmbeddedValueComparesAtItsEnum(t *testing.T) {
	fa, errs := checkEntry(t, `struct Circle
    radius: Float
end

enum Shape
    embeds Circle
    Point
end

// Circle's Equatable is written below.

// Shape's Equatable is written below.

fn left(c: Circle, s: Shape) -> Bool
    c == s
end

fn right(c: Circle, s: Shape) -> Bool
    s != c
end

fn both(a: Circle, b: Circle) -> Bool
    a == b
end

impl Equatable for Circle
    fn equal(a: Circle, b: Circle) -> Bool
        a.radius == b.radius
    end
end

impl Equatable for Shape
    fn equal(a: Shape, b: Shape) -> Bool
        Debug.inspect(a) == Debug.inspect(b)
    end
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	at := map[string]string{}
	for n, use := range fa.Operators {
		b, ok := n.(*ast.Binary)
		if ok && b.Line > 24 {
			// The impls' own comparisons.
			continue
		}
		if !ok || use == nil || use.Form != analysis.OperatorByImpl || use.Impl == nil {
			t.Fatalf("%v: %+v, want OperatorByImpl", n, use)
		}
		at[fmt.Sprintf("line %d %s", b.Line, b.Op)] = use.Impl.Receiver.String()
	}
	want := map[string]string{"line 15 ==": "Shape", "line 19 !=": "Shape", "line 23 ==": "Circle"}
	for op, recv := range want {
		if at[op] != recv {
			t.Errorf("%s: the impl is for %q, want %q (all: %v)", op, at[op], recv, at)
		}
	}
}
