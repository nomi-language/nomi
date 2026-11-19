package irbuild

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/std"
)

// operatorFunctions are the interface functions an operator goes
// through, with the operator a body spells for its own (Equatable's
// `==`, Add's `+`): operIfaceSpecs' operators and `~~~`, and the
// comparisons. Comparable's compare answers an Ordering, which no
// operator spells.
func operatorFunctions() map[[2]string]string {
	out := map[[2]string]string{
		{"Equatable", "equal"}:    "==",
		{"Comparable", "compare"}: "",
	}
	for _, s := range operIfaceSpecs {
		if s.op != "" || s.unary {
			out[[2]string{s.nomi, s.method}] = s.op
		}
	}
	return out
}

// selfOperatorBody reports whether fd's body is its operator op over its
// own two parameters, as `fn equal(a: List<T>, b: List<T>) -> Bool` is
// `a == b`: a call of it reaches the operator again.
func selfOperatorBody(fd *ast.FuncDef, op string) bool {
	if op == "" || fd.Body == nil || len(fd.Body.Stmts) != 1 || len(fd.Params) != 2 {
		return false
	}
	e := fd.Body.Stmts[0]
	if s, isStmt := e.(*ast.ExprStmt); isStmt {
		e = s.Expr
	}
	b, isBinary := e.(*ast.Binary)
	if !isBinary || b.Op != op {
		return false
	}
	l, lok := b.Left.(*ast.Ident)
	r, rok := b.Right.(*ast.Ident)
	return lok && rok && l.Name == fd.Params[0].Name && r.Name == fd.Params[1].Name
}

// stdOperatorImpls is every function of a std impl of an operator's
// interface (operatorFunctions), by identity, with its declaration and
// its body (std.StdLib.Bodies).
func stdOperatorImpls(t *testing.T) map[analysis.StdFunc]ast.Node {
	t.Helper()
	fns := operatorFunctions()
	lib := std.All()
	out := map[analysis.StdFunc]ast.Node{}
	for module, fa := range lib.Files {
		bodies := false
		for n, id := range fa.StdFuncs() {
			iface, _, _ := strings.Cut(id.Impl, "<")
			if _, op := fns[[2]string{iface, id.Name}]; op {
				out[id] = n
				bodies = true
			}
		}
		if bodies {
			if err := lib.Bodies(module); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(out) < 50 {
		t.Fatalf("found %d std impls of an operator's interface, which is too few to check anything", len(out))
	}
	return out
}

// machineComputed reports whether row is one the machine computes itself.
func machineComputed(route operatorRoute) bool {
	return route == routeNative || route == routeStructural
}

// Every std impl of an operator's interface that only the machine can
// compute has a row in machineImpls: a `host fn` no host table binds, and
// a body that is its own operator over its parameters. And every std row
// names such an impl, so the table names nothing std does not declare.
// Adding one to std without a row fails here.
func TestMachineImplsAreComplete(t *testing.T) {
	impls := stdOperatorImpls(t)
	fns := operatorFunctions()
	g := &gen{std: loadStdlibIndex()}
	var missing []string
	hosts, selves := 0, 0
	for id, n := range impls {
		iface, _, _ := strings.Cut(id.Impl, "<")
		why := ""
		switch d := n.(type) {
		case *ast.ExternFunc:
			if f := g.stdFuncByID(id); f == nil || !f.lowerable() {
				hosts++
				why = "a host fn no host table binds"
			}
		case *ast.FuncDef:
			if selfOperatorBody(d, fns[[2]string{iface, id.Name}]) {
				selves++
				why = "its body is its own operator over its parameters"
			}
		}
		if why != "" && !machineComputed(machineImpls[machineImpl{std: id}]) {
			missing = append(missing, fmt.Sprintf("%+v: %s", id, why))
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("machineImpls has no native or structural row for %s, so an operator through it has nothing to lower to", m)
	}
	if hosts < 10 || selves < 5 {
		t.Fatalf("found %d unbound host fns and %d self-operator bodies, which is too few to check anything", hosts, selves)
	}
	t.Logf("%d std operator impls: %d unbound host fns, %d self-operator bodies", len(impls), hosts, selves)
	for row, route := range machineImpls {
		if row.std.IsZero() || route == routeImpl {
			continue
		}
		if _, declared := impls[row.std]; !declared {
			t.Errorf("machineImpls has a row for %+v, which no std module declares as an impl of an operator's interface", row.std)
		}
	}
}

// machineCase runs one row of machineImpls: the operator over each pair
// of values beside a call of the impl's function, made through a
// function value so that the call reaches the impl's body.
type machineCase struct {
	row machineImpl
	// typ is the operands' type, and fn the impl's function as a value.
	typ, fn string
	// values are expressions of typ; every ordered pair is compared.
	values []string
}

// machineCases cover every row machineImpls lists whose impl has a body
// that is not its own operator (machineCaseRows), and a sample of the
// rest.
var machineCases = []machineCase{
	{stdRow("std/int", "Int", "Comparable", "compare"), "Int", "Int.compare", []string{"-3", "0", "7"}},
	{stdRow("std/int", "Int", "Equatable", "equal"), "Int", "Int.equal", []string{"1", "2"}},
	{stdRow("std/float", "Float", "Comparable", "compare"), "Float", "Float.compare", []string{"-0.5", "0.0", "2.5", "Float.positive_infinity()"}},
	{stdRow("std/float", "Float", "Equatable", "equal"), "Float", "Float.equal", []string{"0.5", "Float.nan()"}},
	{stdRow("std/strings", "String", "Equatable", "equal"), "String", "String.equal", []string{`"a"`, `"b"`}},
	{stdRow("std/bool", "Bool", "Equatable", "equal"), "Bool", "Bool.equal", []string{"False", "True"}},
	{stdRow("std/bool", "Bool", "Comparable", "compare"), "Bool", "Bool.compare", []string{"False", "True"}},
	{stdRow("std/unit", "Unit", "Equatable", "equal"), "Unit", "Unit.equal", []string{"Unit"}},
	{stdRow("std/lists", "List", "Equatable", "equal"), "List<Int>", "List.equal", []string{"[]", "[1]", "[1, 2]"}},
	{stdRow("std/lists", "List", "Add<List<T>, List<T>>", "add"), "List<Int>", "List.add", []string{"[]", "[1]", "[2, 3]"}},
	{stdRow("std/maps", "Map", "Equatable", "equal"), "Map<String, Int>", "Map.equal", []string{`{}`, `{"a" => 1}`, `{"a" => 2}`}},
	{stdRow("std/bytes", "Bytes", "Equatable", "equal"), "Bytes", "Bytes.equal", []string{`String.to_bytes("a")`, `String.to_bytes("b")`}},
	{machineImpl{derived: derivedImpl{"Equatable", derivedEnum}}, "Tag", "Tag.equal",
		[]string{"Tag.Plain", "Tag.Named(Name{text: \"a\"})", "Tag.Named(Name{text: \"A\"})", "Tag.Named(Name{text: \"b\"})", "Tag.Measured(Float.nan())", "Tag.Measured(1.0)"}},
	{machineImpl{derived: derivedImpl{"Equatable", derivedDistinct}}, "Alias", "Alias.equal",
		[]string{"Alias(Name{text: \"a\"})", "Alias(Name{text: \"A\"})", "Alias(Name{text: \"b\"})"}},
	{machineImpl{derived: derivedImpl{"Equatable", derivedDistinct}}, "Meters", "Meters.equal", []string{"Meters(1)", "Meters(2)"}},
	{machineImpl{derived: derivedImpl{"Equatable", derivedEnum}}, "Maybe<Float>", "Maybe.equal", []string{"None", "Some(1.0)", "Some(Float.nan())"}},
}

// machineCaseTypes are the program's types the cases compare: a struct
// whose hand-written Equatable ignores case, an enum and a distinct that
// derive theirs over it, and a distinct over an Int.
const machineCaseTypes = `import std::io

struct Name
    text: String
end

impl Equatable for Name
    fn equal(a: Name, b: Name) -> Bool
        String.lowercase(a.text) == String.lowercase(b.text)
    end
end

enum Tag
    Plain
    Named(Name)
    Measured(Float)
end

derive Equatable for Tag

type Alias Name

derive Equatable for Alias

type Meters Int

derive Equatable for Meters
`

// machineCaseProgram is a program that prints, for each case and each
// ordered pair of its values, the operators' answers and the same
// answers from a call of the impl's function: `==` and `!=` beside
// `equal`, `<`, `<=`, `>` and `>=` beside `compare`, or an arithmetic
// operator (List's `+`) beside its function.
func machineCaseProgram() string {
	var b strings.Builder
	b.WriteString(machineCaseTypes)
	var main strings.Builder
	fns := operatorFunctions()
	for i, c := range machineCases {
		iface, _, _ := strings.Cut(c.row.std.Impl, "<")
		var result, operator, called string
		switch iface {
		case "Comparable":
			result = "Ordering"
			operator = `"${a < b} ${a <= b} ${a > b} ${a >= b}"`
			called = `"${r == Ordering.Less} ${r != Ordering.Greater} ${r == Ordering.Greater} ${r != Ordering.Less}"`
		case "Equatable", "":
			// A derived row names no std function; each is an Equatable.
			result = "Bool"
			operator = `"${a == b} ${a != b}"`
			called = `"${r} ${r == False}"`
		default:
			result = c.typ
			operator = fmt.Sprintf(`"${a %s b}"`, fns[[2]string{iface, c.row.std.Name}])
			called = `"${r}"`
		}
		fmt.Fprintf(&b, "\nfn operator_%d(a: %s, b: %s) -> String\n    %s\nend\n", i, c.typ, c.typ, operator)
		fmt.Fprintf(&b, "\nfn called_%d(a: %s, b: %s) -> String\n    f: (%s, %s) -> %s = %s\n    r = f(a, b)\n    %s\nend\n", i, c.typ, c.typ, c.typ, c.typ, result, c.fn, called)
		for x, l := range c.values {
			for y, r := range c.values {
				fmt.Fprintf(&main, "    a: %s = %s\n    b: %s = %s\n", c.typ, l, c.typ, r)
				fmt.Fprintf(&main, "    io.print(\"%d %d %d|${operator_%d(a, b)}|${called_%d(a, b)}\")\n", i, x, y, i, i)
			}
		}
	}
	b.WriteString("\nfn main()\n")
	b.WriteString(main.String())
	b.WriteString("end\n")
	return b.String()
}

// uncalledMachineRows are the rows machineCases cannot run, with the
// reason. A value of std's `True` or `False`, Bool's variant types, is
// not lowered on its own (a binding `a: True = True` declines), so the
// derived equal of either has nothing to be called with; `True == True`
// compares two constants, and a derived equal of a type with one value
// answers True.
var uncalledMachineRows = map[machineImpl]string{
	stdRow("std/bool", "True", "Equatable", "equal"):  "a True value is not lowered on its own",
	stdRow("std/bool", "False", "Equatable", "equal"): "a False value is not lowered on its own",
}

// machineCaseRows are the rows of machineImpls the machine computes
// whose impl has a body other than its own operator: the rows whose
// equivalence with the machine's operation is a claim to test.
func machineCaseRows(t *testing.T) []machineImpl {
	impls := stdOperatorImpls(t)
	fns := operatorFunctions()
	var rows []machineImpl
	for row, route := range machineImpls {
		if !machineComputed(route) {
			continue
		}
		if row.std.IsZero() {
			rows = append(rows, row)
			continue
		}
		fd, isFn := impls[row.std].(*ast.FuncDef)
		iface, _, _ := strings.Cut(row.std.Impl, "<")
		if isFn && !selfOperatorBody(fd, fns[[2]string{iface, row.std.Name}]) {
			rows = append(rows, row)
		}
	}
	return rows
}

// The machine computes what each row of machineImpls's impl computes:
// over a sample of values, each operator answers as a call of the impl's
// function does, and every row whose impl has a body of its own (Int's
// compare, Unit's equal, List's `+`, Bool's and a derived enum's and
// distinct's impls) is in the sample. The call is made through a function value,
// which lowers to a call of the impl's body, never to the operator.
//
// One difference is the language's: `<` and its siblings over Floats
// are IEEE 754, where Float's compare puts NaN highest (spec §5 *NaN and
// the three comparison behaviours*). Float's row stands for the
// operator the spec defines, and the Float case leaves NaN out of its
// ordering; TestMachineImpls_FloatOrderingIsTheSpecsNotCompare holds the
// difference.
func TestMachineImplsComputeTheirBodies(t *testing.T) {
	covered := map[machineImpl]bool{}
	for _, c := range machineCases {
		if !machineComputed(machineImpls[c.row]) {
			t.Errorf("a case runs %+v, which machineImpls does not compute", c.row)
		}
		covered[c.row] = true
	}
	for row := range uncalledMachineRows {
		if !machineComputed(machineImpls[row]) {
			t.Errorf("uncalledMachineRows names %+v, which machineImpls does not compute", row)
		}
	}
	for _, row := range machineCaseRows(t) {
		if _, uncalled := uncalledMachineRows[row]; !covered[row] && !uncalled {
			t.Errorf("no case runs %+v, whose body machineImpls claims the machine computes", row)
		}
	}
	src := machineCaseProgram()
	p, err := AnalyzeSource("entry.nomi", src)
	if err != nil {
		t.Fatalf("the front end rejects the cases' program, so nothing is tested: %v\n%s", err, src)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	for _, mod := range res.IR {
		for _, f := range mod.Funcs() {
			if !strings.HasPrefix(f.Name(), "called_") {
				continue
			}
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					if c, isCall := in.(*ir.Call); isCall && c.Form() == ir.CalleeIndirect {
						called++
					}
				}
			}
		}
	}
	if called != len(machineCases) {
		t.Fatalf("%d of the %d called_ functions call a function value, so a call does not reach each impl's body", called, len(machineCases))
	}
	obs := vmReference(writeTemp(t, src))
	if obs.exit != 0 || obs.stderr != "" {
		t.Fatalf("the cases' program fails: %s", obs)
	}
	lines := strings.Split(strings.TrimSuffix(obs.stdout, "\n"), "\n")
	want := 0
	for _, c := range machineCases {
		want += len(c.values) * len(c.values)
	}
	if len(lines) != want {
		t.Fatalf("the cases' program printed %d lines, want %d:\n%s", len(lines), want, obs.stdout)
	}
	for _, line := range lines {
		parts := strings.Split(line, "|")
		var i, x, y int
		if _, err := fmt.Sscanf(parts[0], "%d %d %d", &i, &x, &y); err != nil || len(parts) != 3 {
			t.Fatalf("the cases' program printed %q", line)
		}
		if parts[1] != parts[2] {
			c := machineCases[i]
			t.Errorf("%+v over %s and %s: the operators answer %q and the impl %q", c.row, c.values[x], c.values[y], parts[1], parts[2])
		}
	}
	t.Logf("%d cases, %d pairs", len(machineCases), want)
}

// `<` and its siblings over Floats are the machine's IEEE 754
// comparison, which machineImpls gives the operators Float's compare
// answers, and no NaN is below, above or equal to anything there; Float's
// compare is a total order with NaN highest (spec §5 *NaN and the three
// comparison behaviours*). The difference is the spec's, so Float's row
// stands for the operator it defines and not for a call of compare.
func TestMachineImpls_FloatOrderingIsTheSpecsNotCompare(t *testing.T) {
	irRunSource(t, `import std::io

fn main()
    nan = Float.nan()
    compare: (Float, Float) -> Ordering = Float.compare
    io.print("${nan < 1.0} ${nan > 1.0} ${nan <= nan}")
    io.print("${compare(nan, 1.0) == Ordering.Greater} ${compare(nan, nan) == Ordering.Equal}")
end
`, "False False False\nTrue True\n")
}
