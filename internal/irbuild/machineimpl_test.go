package irbuild

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/std"
)

// machineFunctions are the interface functions an operator, a rendering
// or the machine's hash goes through, with the operator a body spells for
// its own (Equatable's `==`, Add's `+`): operIfaceSpecs' operators and
// `~~~`, the comparisons, Debug's inspect, Display's to_string and
// Hashable's hash. Comparable's compare answers an Ordering, inspect and
// to_string a String and hash an Int, which no operator spells.
func machineFunctions() map[[2]string]string {
	out := map[[2]string]string{
		{"Equatable", "equal"}:    "==",
		{"Comparable", "compare"}: "",
		{"Debug", "inspect"}:      "",
		{"Display", "to_string"}:  "",
		{"Hashable", "hash"}:      "",
	}
	for _, s := range operIfaceSpecs {
		if s.op != "" || s.unary {
			out[[2]string{s.nomi, s.method}] = s.op
		}
	}
	return out
}

// selfOperatorBody reports whether fd's body is its operator op over its
// own two parameters, as `fn equal(a: Int, b: Int) -> Bool` is
// `a == b`: a call of it reaches the operator again. For an interface
// function with no operator, it is whether the body renders its own
// parameter (selfRenderBody).
func selfOperatorBody(fd *ast.FuncDef, op string) bool {
	if op == "" {
		return selfRenderBody(fd)
	}
	if fd.Body == nil || len(fd.Body.Stmts) != 1 || len(fd.Params) != 2 {
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

// selfRenderBody reports whether fd's body renders its own parameter, as
// `fn inspect(w: Type<T>) -> String` is `Debug.inspect(w)`: a call of it
// reaches the rendering again.
func selfRenderBody(fd *ast.FuncDef) bool {
	if fd.Name != "inspect" || fd.Body == nil || len(fd.Body.Stmts) != 1 || len(fd.Params) != 1 {
		return false
	}
	e := fd.Body.Stmts[0]
	if s, isStmt := e.(*ast.ExprStmt); isStmt {
		e = s.Expr
	}
	call, isCall := e.(*ast.Call)
	if !isCall || len(call.Args) != 1 {
		return false
	}
	callee, isField := call.Func.(*ast.FieldAccess)
	if !isField || callee.Field == nil || callee.Field.Name != "inspect" {
		return false
	}
	owner, ownerOK := callee.Object.(*ast.TypeIdent)
	arg, argOK := call.Args[0].(*ast.Ident)
	return ownerOK && argOK && owner.Name == "Debug" && arg.Name == fd.Params[0].Name
}

// stdMachineImpls is every function of a std impl of an operator's or a
// rendering's interface (machineFunctions), by identity, with its
// declaration and its body (std.StdLib.Bodies).
func stdMachineImpls(t *testing.T) map[analysis.StdFunc]ast.Node {
	t.Helper()
	fns := machineFunctions()
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
		t.Fatalf("found %d std impls of an operator's or a rendering's interface, which is too few to check anything", len(out))
	}
	return out
}

// machineComputed reports whether row is one the machine computes itself.
func machineComputed(route operatorRoute) bool {
	return route == routeNative || route == routeStructural
}

// Every std impl of an operator's or a rendering's interface that only
// the machine can compute has a row in machineImpls: a `host fn` no host
// table binds, and a body that is its own operator over its parameters.
// And every std row names an impl std declares, so the table names
// nothing std does not declare. Adding one to std without a row fails
// here.
func TestMachineImplsAreComplete(t *testing.T) {
	impls := stdMachineImpls(t)
	fns := machineFunctions()
	// The process's lowering, every module restored (stdMachineImpls built
	// them all). loadStdlibIndex would replace the process's bundle state,
	// which later decodes link over.
	g := &gen{std: stdlibLowering()}
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
	t.Logf("%d std operator and rendering impls: %d unbound host fns, %d self-operator bodies", len(impls), hosts, selves)
	for row := range machineImpls {
		if _, declared := impls[row.std]; !declared {
			t.Errorf("machineImpls has a row for %+v, which no std module declares as an impl of an operator's or a rendering's interface", row.std)
		}
	}
}

// Every structural row over a generic type is an impl whose uses the
// checker holds to its parts (analysis.ImplAsksParts: std writes it as a
// `host fn`), since the machine compares, hashes or renders each element
// of the type by its shape: List's, Map's, Set's and Vector's Equatable and
// Hashable. A body written in Nomi would tell the checker to ask only the
// impl's where clause, and a List of functions would reach the machine's
// comparison. Debug is left out: every type has it, so asking it of the
// parts refuses nothing.
func TestMachineImpls_ShapeRowsAskTheirParts(t *testing.T) {
	impls := stdMachineImpls(t)
	lib := std.All()
	checked := 0
	for row, route := range machineImpls {
		iface := rowIface(row)
		if route != routeStructural || iface == "Debug" {
			continue
		}
		fn := impls[row.std]
		var block *analysis.ImplTypeArgs
		for _, fa := range lib.Files {
			if _, declares := fa.StdFuncs()[fn]; !declares {
				continue
			}
			for _, info := range fa.ImplBlocks[row.std.Owner][iface] {
				if info != nil && info.Block != nil && slices.Contains(info.Block.Items, fn) {
					block = info
				}
			}
		}
		if block == nil {
			t.Errorf("no impl block of %+v holds its function", row.std)
			continue
		}
		if len(block.TypeParamDefs) == 0 {
			continue
		}
		checked++
		if !analysis.ImplAsksParts(block.Block) {
			t.Errorf("%+v: the machine answers it by its shape, and its block writes a body, so the checker asks nothing of its parts", row.std)
		}
	}
	if checked < 8 {
		t.Fatalf("checked %d structural rows over a generic type, which is too few to check anything", checked)
	}
}

// machineCase runs one row of machineImpls: the operator over each pair
// of values, or for a Debug row the rendering of each value, beside a
// call of the impl's function, made through a function value so that the
// call reaches the impl's body.
type machineCase struct {
	row machineImpl
	// typ is the operands' type, and fn the impl's function as a value.
	typ, fn string
	// values are expressions of typ; every ordered pair is compared, and
	// each one rendered.
	values []string
}

// task reports whether c's values are tasks, each rendered inside a
// `concurrent` block that awaits it, as every task is awaited.
func (c machineCase) task() bool {
	return strings.HasPrefix(c.typ, "Task<")
}

// rowIface is the interface of row's impl.
func rowIface(row machineImpl) string {
	iface, _, _ := strings.Cut(row.std.Impl, "<")
	return iface
}

// unary reports whether c's impl takes one value, as Debug's inspect,
// Display's to_string and Hashable's hash do.
func (c machineCase) unary() bool {
	iface := rowIface(c.row)
	return iface == "Debug" || iface == "Display" || iface == "Hashable"
}

// rendering is the interface function a unary case's operator side
// calls, which the machine answers: `Debug.inspect`,
// `Display.to_string` or `Hashable.hash`, and the type it answers.
func (c machineCase) rendering() (string, string) {
	switch rowIface(c.row) {
	case "Display":
		return "Display.to_string", "String"
	case "Hashable":
		return "Hashable.hash", "Int"
	}
	return "Debug.inspect", "String"
}

// lines is how many lines c's part of the cases' program prints.
func (c machineCase) lines() int {
	if c.unary() {
		return len(c.values)
	}
	return len(c.values) * len(c.values)
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
	{stdRow("std/bool", "Bool", "Comparable", "compare"), "Bool", "Bool.compare", []string{"False", "True"}},
	{stdRow("std/unit", "Unit", "Equatable", "equal"), "Unit", "Unit.equal", []string{"Unit"}},
	{stdRow("std/lists", "List", "Equatable", "equal"), "List<Int>", "List.equal", []string{"[]", "[1]", "[1, 2]"}},
	{stdRow("std/lists", "List", "Add<List<T>, List<T>>", "add"), "List<Int>", "List.add", []string{"[]", "[1]", "[2, 3]"}},
	{stdRow("std/maps", "Map", "Equatable", "equal"), "Map<String, Int>", "Map.equal", []string{`{}`, `{"a" => 1}`, `{"a" => 2}`}},
	{stdRow("std/bytes", "Bytes", "Equatable", "equal"), "Bytes", "Bytes.equal", []string{`String.to_bytes("a")`, `String.to_bytes("b")`}},
	{stdRow("std/decimal", "Decimal", "Equatable", "equal"), "Decimal", "Decimal.equal", []string{"1.5d", "1.50d", "-1.5d", "0d", "0.000d", "2d"}},

	{debugRow("std/int", "Int"), "Int", "Int.inspect",
		[]string{"0", "-7", "9223372036854775807", "92233720368547758070", "-92233720368547758070"}},
	{debugRow("std/float", "Float"), "Float", "Float.inspect",
		[]string{"0.1", "-2.0", "1.0e21", "1.0e-7", "100.0", "1.0 / 3.0", "-0.0", "123456789012345680000.0",
			"Float.nan()", "Float.positive_infinity()", "-Float.positive_infinity()"}},
	{debugRow("std/bool", "Bool"), "Bool", "Bool.inspect", []string{"False", "True"}},
	{debugRow("std/strings", "String"), "String", "String.inspect",
		[]string{`""`, `"a\"b"`, `"t\tn\nr\rb\\"`, `"é ü"`, `"\u{1F600}"`, `"\${x}"`, `"\u{0}\u{7}\u{7F}"`, `"\u{200D}\u{FEFF}"`}},
	{debugRow("std/bytes", "Byte"), "Byte", "Byte.inspect", []string{"0", "255", "0x41"}},
	{debugRow("std/bytes", "Bytes"), "Bytes", "Bytes.inspect",
		[]string{`String.to_bytes("")`, `String.to_bytes("ab\"c")`, "Bytes.from_list([0, 255, 0x7F])"}},
	{debugRow("std/decimal", "Decimal"), "Decimal", "Decimal.inspect",
		[]string{"1.50d", "-0.001d", "0d", "100d", "123456789012345678901234.5d"}},
	{debugRow("std/lists", "List"), "List<Int>", "List.inspect", []string{"[]", "[1, 2]"}},
	{debugRow("std/lists", "List"), "List<List<Int>>", "List.inspect", []string{"[[1], [2, 3], []]"}},
	{debugRow("std/lists", "List"), "List<String>", "List.inspect", []string{`["a", "b\"c"]`}},
	{debugRow("std/lists", "List"), "List<Float>", "List.inspect", []string{"[1.0, 2.5, Float.nan()]"}},
	{debugRow("std/lists", "List"), "List<Decimal>", "List.inspect", []string{"[1.50d, 2d]"}},
	{debugRow("std/lists", "List"), "List<Option<Int>>", "List.inspect", []string{"[Some(1), None]"}},
	{debugRow("std/vectors", "Vector"), "Vector<Int>", "Vector.inspect", []string{"Vector[]", "Vector[1, 2]"}},
	{debugRow("std/vectors", "Vector"), "Vector<List<Int>>", "Vector.inspect", []string{"Vector[[1], []]"}},
	{debugRow("std/vectors", "Vector"), "Vector<Decimal>", "Vector.inspect", []string{"Vector[1.5d]"}},
	{debugRow("std/maps", "Map"), "Map<String, Int>", "Map.inspect", []string{"{}", `{"b" => 2, "a" => 1}`}},
	{debugRow("std/maps", "Map"), "Map<Int, List<String>>", "Map.inspect", []string{`{1 => ["x"], 2 => []}`}},
	{debugRow("std/maps", "Map"), "Map<String, Option<Int>>", "Map.inspect", []string{`{"a" => Some(1), "b" => None}`}},
	{debugRow("std/sets", "Set"), "Set<Int>", "Set.inspect", []string{"Set[]", "Set[3, 1]"}},
	{debugRow("std/sets", "Set"), "Set<String>", "Set.inspect", []string{`Set["b", "a"]`}},
	{debugRow("std/sets", "Set"), "Set<Decimal>", "Set.inspect", []string{"Set[1.5d, 2d]"}},
	{debugRow("std/dynamic", "Dynamic"), "Dynamic", "Dynamic.inspect",
		[]string{`Json.to_dynamic(Json.String("s\"t"))`, "Json.to_dynamic(Json.Int(-4))", "Json.to_dynamic(Json.Float(2.5))",
			"Json.to_dynamic(Json.Null)", `Json.to_dynamic(Json.Arr([Json.Bool(True), Json.Obj({"k" => Json.Int(1)})]))`}},
	{debugRow("std/context", "Context"), "Context", "Context.inspect",
		[]string{"Context.root()", "Context.with_value(Context.root(), 1)"}},
	{debugRow("std/unit", "Unit"), "Unit", "Unit.inspect", []string{"Unit"}},
	{debugRow("std/channels", "Sender"), "Sender<Int>", "Sender.inspect", []string{"Channel.buffered<Int>(1).sender"}},
	{debugRow("std/channels", "Receiver"), "Receiver<String>", "Receiver.inspect", []string{"Channel.buffered<String>(1).receiver"}},
	{debugRow("std/channels", "Arm"), "Arm<Int>", "Arm.inspect", []string{"Arm.after(Duration.seconds(0), fn -> 1)"}},
	{debugRow("std/tasks", "Task"), "Task<Int>", "Task.inspect", []string{"Task.spawn(fn -> 1)"}},
	{debugRow("std/type", "Type"), "Type<Done>", "Type.inspect", []string{"Done"}},
	{debugRow("std/type", "Type"), "Type<Name>", "Type.inspect", []string{"Name"}},
	{hashRow("std/lists", "List"), "List<Int>", "List.hash", []string{"[]", "[1, 2]", "[2, 1]"}},
	{hashRow("std/lists", "List"), "List<List<String>>", "List.hash", []string{`[["a"], []]`}},
	{hashRow("std/vectors", "Vector"), "Vector<Int>", "Vector.hash", []string{"Vector[]", "Vector[1, 2]"}},
	{hashRow("std/maps", "Map"), "Map<String, Int>", "Map.hash", []string{"{}", `{"a" => 1, "b" => 2}`, `{"b" => 2, "a" => 1}`}},
	{hashRow("std/sets", "Set"), "Set<Int>", "Set.hash", []string{"Set[]", "Set[3, 1]", "Set[1, 3]"}},
	{displayRow("std/int", "Int"), "Int", "Int.to_string", []string{"0", "-7", "92233720368547758070"}},
	{displayRow("std/float", "Float"), "Float", "Float.to_string",
		[]string{"0.0", "-0.0", "0.1", "1.0e21", "Float.nan()", "Float.positive_infinity()"}},
	{displayRow("std/strings", "String"), "String", "String.to_string", []string{`""`, `"a\"b\t"`, `"\u{0}"`}},
	{displayRow("std/decimal", "Decimal"), "Decimal", "Decimal.to_string",
		[]string{"1.50d", "-0.001d", "0d", "100d", "123456789012345678901234.5d"}},
}

// displayRow is the row of std's Display of owner, declared in module.
func displayRow(module, owner string) machineImpl {
	return stdRow(module, owner, "Display", "to_string")
}

// hashRow is the row of std's Hashable of owner, declared in module.
func hashRow(module, owner string) machineImpl {
	return stdRow(module, owner, "Hashable", "hash")
}

// debugRow is the row of std's Debug of owner, declared in module.
func debugRow(module, owner string) machineImpl {
	return stdRow(module, owner, "Debug", "inspect")
}

// machineCaseTypes are the program's types the cases compare: a struct
// whose hand-written Equatable ignores case, an enum, a distinct, a
// struct and a generic struct that derive theirs over it (the struct over
// a Float and a Decimal too), and distincts over an Int, a String, a
// Float and a Bool and a marker, each of which derives its Debug, which
// the machine renders by its shape (TestMachineImpls_ADerivedDebugIsTheShape).
const machineCaseTypes = `import
    std::channels.{Arm, Channel, Receiver, Sender}
    std::duration.Duration
    std::dynamic.Dynamic
    std::io
    std::json.Json
    std::tasks.Task
end

struct Name
    text: String
end

type Meters Int

derive Debug for Meters

type Done

derive Debug for Done

type Label String

derive Debug for Label

type Ratio Float

derive Debug for Ratio

type Flag Bool

derive Debug for Flag

type Shown Int

derive Debug for Shown
`

// machineCaseProgram is a program that prints, for each case and each
// ordered pair of its values, the operators' answers and the same
// answers from a call of the impl's function: `==` and `!=` beside
// `equal`, `<`, `<=`, `>` and `>=` beside `compare`, or an arithmetic
// operator (List's `+`) beside its function; and for a Debug case and
// each of its values, `Debug.inspect`, which the machine renders, beside
// a call of the impl's inspect.
func machineCaseProgram() string {
	var b strings.Builder
	b.WriteString(machineCaseTypes)
	var main strings.Builder
	fns := machineFunctions()
	for i, c := range machineCases {
		if c.unary() {
			rendering, result := c.rendering()
			fmt.Fprintf(&b, "\nfn operator_%d(a: %s) -> %s\n    %s(a)\nend\n", i, c.typ, result, rendering)
			fmt.Fprintf(&b, "\nfn called_%d(a: %s) -> %s\n    f: (%s) -> %s = %s\n    f(a)\nend\n", i, c.typ, result, c.typ, result, c.fn)
			for x, v := range c.values {
				if c.task() {
					fmt.Fprintf(&main, "    line_%d_%d = concurrent\n        a: %s = %s\n", i, x, c.typ, v)
					fmt.Fprintf(&main, "        line = \"%d %d 0|${operator_%d(a)}|${called_%d(a)}\"\n", i, x, i, i)
					fmt.Fprintf(&main, "        _ = Task.await(a)\n        line\n    end\n    io.print(line_%d_%d)\n", i, x)
					continue
				}
				fmt.Fprintf(&main, "    a: %s = %s\n", c.typ, v)
				fmt.Fprintf(&main, "    io.print(\"%d %d 0|${operator_%d(a)}|${called_%d(a)}\")\n", i, x, i, i)
			}
			continue
		}
		var result, operator, called string
		switch iface := rowIface(c.row); iface {
		case "Comparable":
			result = "Ordering"
			operator = `"${a < b} ${a <= b} ${a > b} ${a >= b}"`
			called = `"${r == Ordering.Less} ${r != Ordering.Greater} ${r == Ordering.Greater} ${r != Ordering.Less}"`
		case "Equatable":
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

// machineCaseRows are the rows of machineImpls the machine computes
// whose impl has a body other than its own operator, or is a `host fn` a
// host table binds: the rows whose equivalence with the machine's
// operation is a claim to test.
func machineCaseRows(t *testing.T) []machineImpl {
	impls := stdMachineImpls(t)
	fns := machineFunctions()
	g := &gen{std: stdlibLowering()}
	var rows []machineImpl
	for row, route := range machineImpls {
		if !machineComputed(route) {
			continue
		}
		switch d := impls[row.std].(type) {
		case *ast.FuncDef:
			if !selfOperatorBody(d, fns[[2]string{rowIface(row), row.std.Name}]) {
				rows = append(rows, row)
			}
		case *ast.ExternFunc:
			if f := g.stdFuncByID(row.std); f != nil && f.lowerable() {
				rows = append(rows, row)
			}
		}
	}
	return rows
}

// The machine computes what each row of machineImpls's impl computes:
// over a sample of values, each operator answers as a call of the impl's
// function does, and the machine's rendering of each value is the text a
// call of the impl's inspect answers. Every row whose impl has a body of
// its own (Int's compare, Unit's equal, List's `+`, Bool's derived
// Comparable, std's Debug of Int,
// String, List and the others, and Unit's name-only Debug) or is a bound `host fn` (Decimal's equal, over values equal at
// different scales) is in the sample, which holds the values the stage-1
// probe of item 14 compared at every Debug site: big Ints, Float edge
// values, String
// escapes and control characters, Decimal scales, empty and nested
// collections, and Option, Result, Dynamic and Context values. The call
// is made through a function value, which lowers to a call of the impl's
// body, never to the operator or the renderer.
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
	for _, row := range machineCaseRows(t) {
		if !covered[row] {
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
		want += c.lines()
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
		switch c := machineCases[i]; {
		case parts[1] == parts[2]:
		case c.unary():
			t.Errorf("%+v over %s: the machine renders %q and the impl %q", c.row, c.values[x], parts[1], parts[2])
		default:
			t.Errorf("%+v over %s and %s: the operators answer %q and the impl %q", c.row, c.values[x], c.values[y], parts[1], parts[2])
		}
	}
	t.Logf("%d cases, %d lines", len(machineCases), want)
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
