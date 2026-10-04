package ir_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// arithCase is one constructible arithmetic operation, its expected shape and
// its expected fault set.
type arithCase struct {
	op     ir.ArithOp
	kind   ir.ArithKind
	label  string
	shape  ir.Shape
	faults ir.Faults
}

// arithCases is every operator-and-domain combination the model admits.
//
// The fault column is not a restatement of the model: it is read off the
// operations' behaviour — rt's overflow predicates and Decimal operations,
// and the VM's arithmetic over them — operator by operator.
func arithCases() []arithCase {
	checked := ir.IntArith(ir.OverflowFaults)
	wrapping := ir.IntArith(ir.OverflowWraps)
	flt := ir.FloatArith()
	dec := ir.DecimalArith()
	return []arithCase{
		// Int, checked. `+ - *` and unary `-` fault on overflow; `/` faults on a
		// zero divisor AND on MinInt64 / -1; `%` faults on a zero divisor only,
		// because Go's `%` answers MinInt64 % -1 without a panic (the answer
		// is 0).
		{ir.OpAdd, checked, "Int +", ir.ShapeIntChecked, ir.FaultOverflow},
		{ir.OpSub, checked, "Int -", ir.ShapeIntChecked, ir.FaultOverflow},
		{ir.OpMul, checked, "Int *", ir.ShapeIntChecked, ir.FaultOverflow},
		{ir.OpDiv, checked, "Int /", ir.ShapeIntChecked, ir.FaultDivByZero | ir.FaultOverflow},
		{ir.OpRem, checked, "Int %", ir.ShapeIntChecked, ir.FaultDivByZero},
		{ir.OpNeg, checked, "Int neg", ir.ShapeIntChecked, ir.FaultOverflow},

		// Int, modular. `derive Hashable`'s synthesized mixers. Three
		// operators and no others, because rt has three wrapping functions.
		{ir.OpAdd, wrapping, "Int wrapping +", ir.ShapeIntWrapping, 0},
		{ir.OpSub, wrapping, "Int wrapping -", ir.ShapeIntWrapping, 0},
		{ir.OpMul, wrapping, "Int wrapping *", ir.ShapeIntWrapping, 0},

		// Float. IEEE has an answer for every input including a zero divisor,
		// so nothing here faults except `%`, which has no answer at all.
		{ir.OpAdd, flt, "Float +", ir.ShapeFloatNative, 0},
		{ir.OpSub, flt, "Float -", ir.ShapeFloatNative, 0},
		{ir.OpMul, flt, "Float *", ir.ShapeFloatNative, 0},
		{ir.OpNeg, flt, "Float neg", ir.ShapeFloatNative, 0},
		{ir.OpDiv, flt, "Float /", ir.ShapeFloatDiv, 0},
		{ir.OpRem, flt, "Float %", ir.ShapeFloatMod, ir.FaultUndefined},

		// Decimal. Exact, so `+ - *` and unary `-` never fail; `/` faults on a
		// zero divisor; `%` is undefined.
		{ir.OpAdd, dec, "Decimal +", ir.ShapeDecimal, 0},
		{ir.OpSub, dec, "Decimal -", ir.ShapeDecimal, 0},
		{ir.OpMul, dec, "Decimal *", ir.ShapeDecimal, 0},
		{ir.OpNeg, dec, "Decimal neg", ir.ShapeDecimal, 0},
		{ir.OpDiv, dec, "Decimal /", ir.ShapeDecimal, ir.FaultDivByZero},
		{ir.OpRem, dec, "Decimal %", ir.ShapeDecimal, ir.FaultUndefined},
	}
}

func buildArith(t *testing.T, c arithCase) *ir.Arith {
	t.Helper()
	pos := ir.At("a.nomi", 7, 11)
	if c.op.Unary() {
		return ir.NewUnaryArith(pos, 1, c.op, c.kind, 2)
	}
	return ir.NewArith(pos, 1, c.op, c.kind, 2, 3)
}

// TestArith_SixShapesAllReachableAndNoneStored: every shape of Nomi's
// primitive `a + b` is produced by some constructible node here, and none of
// them is a field: Shape is computed from the operator, the domain and the
// overflow discipline.
func TestArith_SixShapesAllReachableAndNoneStored(t *testing.T) {
	seen := map[ir.Shape][]string{}
	for _, c := range arithCases() {
		a := buildArith(t, c)
		if got := a.Shape(); got != c.shape {
			t.Errorf("%s: Shape() = %s, want %s", c.label, got, c.shape)
		}
		seen[a.Shape()] = append(seen[a.Shape()], c.label)
	}
	all := []ir.Shape{
		ir.ShapeIntChecked, ir.ShapeIntWrapping, ir.ShapeFloatNative,
		ir.ShapeFloatDiv, ir.ShapeFloatMod, ir.ShapeDecimal,
	}
	for _, s := range all {
		if len(seen[s]) == 0 {
			t.Errorf("no constructible operation has shape %s, so the model cannot express "+
				"one of the six", s)
			continue
		}
		t.Logf("%-13s %v", s, seen[s])
	}
	if len(seen) != len(all) {
		t.Errorf("the cases produce %d distinct shapes, want exactly %d", len(seen), len(all))
	}

	// PLANT A POSITIVE on the distinguishability claim: two operations that
	// differ ONLY in the overflow discipline must land on different shapes,
	// and two that differ only in the operator within one domain must not
	// necessarily. Without the first, Shape could be a constant per domain.
	add := ir.NewArith(ir.At("a.nomi", 1, 1), 1, ir.OpAdd, ir.IntArith(ir.OverflowFaults), 2, 3)
	wrap := ir.NewArith(ir.At("a.nomi", 1, 1), 1, ir.OpAdd, ir.IntArith(ir.OverflowWraps), 2, 3)
	if add.Shape() == wrap.Shape() {
		t.Error("a checked Int `+` and a modular Int `+` have the same shape; the first " +
			"faults on overflow and the second wraps")
	}
}

// TestArith_FaultPointIsRecordedWithoutADeliveryChoice is the fault/delivery
// division (point 1 of ir.go's package header).
func TestArith_FaultPointIsRecordedWithoutADeliveryChoice(t *testing.T) {
	for _, c := range arithCases() {
		a := buildArith(t, c)
		if got := a.Faults(); got != c.faults {
			t.Errorf("%s: Faults() = %s, want %s", c.label, got, c.faults)
		}
		if a.Faults().Any() != (c.faults != 0) {
			t.Errorf("%s: Faults().Any() disagrees with Faults()", c.label)
		}
		// The fault point IS the node's position. Nothing else names it.
		if !a.Pos().IsValid() {
			t.Errorf("%s: no position, so a fault has nowhere to be blamed", c.label)
		}
	}

	// PLANT A POSITIVE on the fault predicate: it must separate cases, or
	// `return 0` would satisfy every row that expects no fault.
	faulting, clean := 0, 0
	for _, c := range arithCases() {
		if buildArith(t, c).Faults().Any() {
			faulting++
		} else {
			clean++
		}
	}
	if faulting == 0 || clean == 0 {
		t.Fatalf("Faults() answers the same way for every case (%d faulting, %d clean), so "+
			"the table above cannot have discriminated anything", faulting, clean)
	}
	t.Logf("%d of %d operations can fault at their own position", faulting, faulting+clean)

	// The overflow discipline is the ONE fact a producer supplies that the
	// front end's type answer does not force, and it is a property of the
	// SOURCE CONSTRUCT rather than of a consumer: `derive Hashable`
	// synthesizes modular mixers, a programmer's `+` is checked.
	if got := ir.IntArith(ir.OverflowWraps); got.Domain() != ir.DomainInt {
		t.Errorf("IntArith reports domain %s", got.Domain())
	}
	if got := recovered(func() { ir.IntArith(0) }); got == "" {
		t.Error("IntArith accepted an unset discipline, so an Int operation can be built " +
			"without saying whether overflow is a fault")
	}
}

// TestArith_ThereIsNoDeliveryFieldAnywhere scans every identifier this package
// declares for a delivery-shaped name.
//
// A `trap bool` was the rejected design, and it would be an UNEXPORTED field,
// so an exported-API scan would not see it. This reads every declared name in
// the package: type names, function and method names, constant names, and
// struct field names.
func TestArith_ThereIsNoDeliveryFieldAnywhere(t *testing.T) {
	names := declaredNames(t)

	// PLANT A POSITIVE: the scan must find names it is expected to find, or
	// its empty answer below would be the answer for an empty scan.
	for _, want := range []string{"Faults", "Arith", "over", "OverflowWraps", "dom"} {
		if !names[want] {
			t.Fatalf("the identifier scan did not find %q, which package ir declares. Its "+
				"answer about absent names carries no information", want)
		}
	}
	t.Logf("%d identifiers declared in package ir", len(names))

	// The rejected design, and its neighbours. A field or method whose name
	// contains any of these is a consumer's control flow smuggled into the
	// representation.
	for _, banned := range []string{"trap", "panic", "boxed", "throw", "delivery"} {
		for name := range names {
			if strings.Contains(strings.ToLower(name), banned) {
				t.Errorf("package ir declares %q, whose name contains %q. The IR carries no "+
					"delivery flag on the node: a producer must not have to know how its "+
					"consumer delivers a fault. Record the FAULT POINT",
					name, banned)
			}
		}
	}
}

// TestArith_UnrepresentableCombinationsAreRejected pins the constraints that
// come from rt rather than from taste.
func TestArith_UnrepresentableCombinationsAreRejected(t *testing.T) {
	pos := ir.At("a.nomi", 1, 1)
	wrapping := ir.IntArith(ir.OverflowWraps)

	// rt has WrapAddInt, WrapSubInt and WrapMulInt and nothing else, so a
	// modular `/`, `%` or unary `-` has no emitted shape. Measured, not
	// assumed: rtArithSignatures below would find them if they existed.
	sigs := rtArithSignatures(t)
	for _, name := range []string{"WrapQuoInt", "WrapRemInt", "WrapNegInt"} {
		if _, ok := sigs[name]; ok {
			t.Errorf("rt declares %s, so the modular discipline now covers more operators "+
				"than this package admits", name)
		}
	}
	for _, op := range []ir.ArithOp{ir.OpDiv, ir.OpRem} {
		if got := recovered(func() { ir.NewArith(pos, 1, op, wrapping, 2, 3) }); got == "" {
			t.Errorf("a modular Int `%s` was accepted; rt has no wrapping form of it",
				op.Symbol())
		}
	}
	if got := recovered(func() { ir.NewUnaryArith(pos, 1, ir.OpNeg, wrapping, 2) }); got == "" {
		t.Error("a modular Int unary `-` was accepted; rt has no WrapNegInt")
	}
	// PLANT A POSITIVE: the three that DO exist must be accepted.
	for _, op := range []ir.ArithOp{ir.OpAdd, ir.OpSub, ir.OpMul} {
		if got := recovered(func() { ir.NewArith(pos, 1, op, wrapping, 2, 3) }); got != "" {
			t.Errorf("a modular Int `%s` was rejected: %s", op.Symbol(), got)
		}
	}

	// Arity. OpNeg reads one operand and everything else reads two, and the
	// two constructors refuse each other's operators rather than silently
	// leaving a NoTemp behind for a consumer to interpret.
	if got := recovered(func() {
		ir.NewArith(pos, 1, ir.OpNeg, ir.FloatArith(), 2, 3)
	}); got == "" {
		t.Error("NewArith accepted a unary operator")
	}
	if got := recovered(func() {
		ir.NewUnaryArith(pos, 1, ir.OpAdd, ir.FloatArith(), 2)
	}); got == "" {
		t.Error("NewUnaryArith accepted a binary operator")
	}
	if got := recovered(func() {
		ir.NewArith(pos, 1, ir.OpAdd, ir.FloatArith(), ir.NoTemp, 3)
	}); got == "" {
		t.Error("NewArith accepted a missing left operand")
	}
	if got := recovered(func() {
		ir.NewArith(pos, ir.NoTemp, ir.OpAdd, ir.FloatArith(), 2, 3)
	}); got == "" {
		t.Error("NewArith accepted a missing destination; an arithmetic result nothing reads " +
			"is dead, and building it hides the producer's mistake")
	}
	if got := recovered(func() {
		ir.NewArith(pos, 1, ir.OpAdd, ir.ArithKind{}, 2, 3)
	}); got == "" {
		t.Error("NewArith accepted a zero ArithKind, so the Int case could be built without " +
			"an overflow discipline by going around the factories")
	}

	// Uses and operands.
	bin := ir.NewArith(pos, 1, ir.OpAdd, ir.FloatArith(), 2, 3)
	if got := bin.AppendUses(nil); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Errorf("a binary operation reads %v, want [t2 t3]", got)
	}
	un := ir.NewUnaryArith(pos, 1, ir.OpNeg, ir.FloatArith(), 2)
	if got := un.AppendUses(nil); len(got) != 1 || got[0] != 2 {
		t.Errorf("a unary operation reads %v, want [t2]", got)
	}
	if un.Rhs() != ir.NoTemp {
		t.Errorf("a unary operation has a right operand %s", un.Rhs())
	}
}

// TestArith_OperatorSpellingMatchesTheTrapMessage guards the one string this
// class shares with rt's trap text.
//
// `rt.overflowText` formats "line %d: integer overflow: %d %s %d" with the
// operator spelled the way the source spells it. The trap text is right only
// if the spelling agrees, and the golden files police the rest.
func TestArith_OperatorSpellingMatchesTheTrapMessage(t *testing.T) {
	for _, tc := range []struct {
		op   ir.ArithOp
		want string
	}{
		{ir.OpAdd, "+"}, {ir.OpSub, "-"}, {ir.OpMul, "*"},
		{ir.OpDiv, "/"}, {ir.OpRem, "%"},
		// Unary minus reports as `-`, because the VM checks it as the
		// subtraction `0 - a` and the message names the subtraction it
		// performed.
		{ir.OpNeg, "-"},
	} {
		if got := tc.op.Symbol(); got != tc.want {
			t.Errorf("%s spells itself %q, want %q", tc.op, got, tc.want)
		}
	}
	// The operator strings rt.OverflowError is called with, read from the
	// VM's own source, must be a subset of what ArithOp can spell.
	spellable := map[string]bool{}
	for _, op := range []ir.ArithOp{ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpDiv, ir.OpRem, ir.OpNeg} {
		spellable[op.Symbol()] = true
	}
	found := 0
	for _, lit := range vmOverflowOperatorLiterals(t) {
		if !spellable[lit] {
			t.Errorf("internal/vm reports an overflow of the operator %q, which no ArithOp spells", lit)
			continue
		}
		found++
	}
	if found == 0 {
		t.Fatal("no operator literal was found in internal/vm's rt.OverflowError calls, so the " +
			"agreement above was not measured")
	}
	t.Logf("%d operator spellings in internal/vm's overflow reports, all spellable by ArithOp", found)
}

// vmOverflowOperatorLiterals collects the operator strings internal/vm's
// non-test sources pass to rt.OverflowError.
func vmOverflowOperatorLiterals(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "vm"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join("..", "vm", e.Name())
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "OverflowError" {
				return true
			}
			for _, a := range call.Args {
				lit, ok := a.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				out = append(out, strings.Trim(lit.Value, `"`))
			}
			return true
		})
	}
	return out
}

// --- instruments ------------------------------------------------------------

// rtArithSignatures maps each function declared in rt/arith.go to whether it
// takes a `line int` parameter.
func rtArithSignatures(t *testing.T) map[string]bool {
	t.Helper()
	f := parseRTArith(t)
	out := map[string]bool{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Type.Params == nil {
			continue
		}
		carries := false
		for _, p := range fn.Type.Params.List {
			id, ok := p.Type.(*ast.Ident)
			if !ok || id.Name != "int" {
				continue
			}
			for _, n := range p.Names {
				if n.Name == "line" {
					carries = true
				}
			}
		}
		out[fn.Name.Name] = carries
	}
	return out
}

func parseRTArith(t *testing.T) *ast.File {
	t.Helper()
	path := filepath.Join(rtDir(t), "arith.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// declaredNames is every identifier package ir declares: types, functions,
// methods, constants, variables and struct fields.
func declaredNames(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	files := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), e.Name(), src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files++
		ast.Inspect(f, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.FuncDecl:
				out[d.Name.Name] = true
			case *ast.TypeSpec:
				out[d.Name.Name] = true
			case *ast.ValueSpec:
				for _, id := range d.Names {
					out[id.Name] = true
				}
			case *ast.Field:
				for _, id := range d.Names {
					out[id.Name] = true
				}
			}
			return true
		})
	}
	if files == 0 {
		t.Fatal("no non-test .go files found in the package directory")
	}
	return out
}
