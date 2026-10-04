package irbuild

// The VM checks the shape of an instruction's operands at execution: a branch
// condition must be a Bool, an Int arithmetic's operands Ints, a field
// projection's subject a struct, and so on. `ir.Lint`'s RuleOperandShape
// checks the same conditions at the gate.
//
// This file holds an independent derivation of an operand's shape, a def-use
// query over `ir.Func.Def`, and uses it two ways: over the retained corpus and
// std populations, where every answered position must have the shape its
// condition requires; and on planted graphs, where it must agree with
// RuleOperandShape.
//
// What the derivation answers from, all of it already on the node:
//
//	*ir.Const    Kind()        Bool/Int/Float/Decimal/String/...
//	*ir.Arith    Domain()      Int/Float/Decimal
//	*ir.Concat   the operation String
//	*ir.Render   the operation String
//	*ir.Match    the operation Bool
//	*ir.Compare  the operation Bool
//	*ir.Make     Kind()        struct/variant/distinct/tuple/...
//	*ir.Copy     Src(), transitively
//	*ir.Bind     Src(), transitively
//	a parameter  the shape its declaration records
//
// What it cannot answer: a `*ir.Proj` result (the node's own `Shape` answers
// that, and this derivation deliberately does not read it), a `*ir.Call`
// result, and a `*ir.Ref` other than a function or a parameter.
//
// `*ir.Type` answers no shape question: it is a name plus a `TypeForm`, and
// table.go forbids reading the name as an identity, so "is this an Int" off
// an `*ir.Type` would be the forbidden `Name() == "Int"` comparison.

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// axis13Shape is the coarse value shape the VM's operand conditions ask about.
// Not a type: `branches on %T, which is not a Bool` and `reads a field off
// %T, not a struct` are shape questions, and a shape is what the graph can
// answer.
type axis13Shape uint8

const (
	// a13Unknown is "the graph does not say".
	a13Unknown axis13Shape = iota
	a13Unit
	a13Bool
	a13Int
	a13Float
	a13Decimal
	a13String
	a13Struct
	a13Tuple
	a13Variant
	a13Distinct
	a13Container
	a13Func
)

func (s axis13Shape) String() string {
	switch s {
	case a13Unit:
		return "Unit"
	case a13Bool:
		return "Bool"
	case a13Int:
		return "Int"
	case a13Float:
		return "Float"
	case a13Decimal:
		return "Decimal"
	case a13String:
		return "String"
	case a13Struct:
		return "struct"
	case a13Tuple:
		return "tuple"
	case a13Variant:
		return "variant"
	case a13Distinct:
		return "distinct"
	case a13Container:
		return "container"
	case a13Func:
		return "function"
	}
	return "UNKNOWN"
}

// axis13ShapeOf derives the shape of the value temporary t holds, from the
// instruction that defines it and nothing else.
//
// Bounded, because Copy/Bind chains can in principle cycle in a non-SSA graph
// and a `Func.Def` that answers the FIRST writer is not a proof there is only
// one.
func axis13ShapeOf(f *ir.Func, t ir.Temp, depth int) axis13Shape {
	if t == ir.NoTemp || depth > 16 {
		return a13Unknown
	}
	def := f.Def(t)
	if def == nil {
		// A parameter, whose declaration records a shape, or storage a Slot
		// declared, which carries an `*ir.Type` and so answers no shape
		// question.
		for _, p := range f.Params() {
			if p.Temp == t {
				return axis13Of(p.Shape)
			}
		}
		return a13Unknown
	}
	switch n := def.(type) {
	case *ir.Const:
		switch n.Kind() {
		case ir.ConstUnit:
			return a13Unit
		case ir.ConstBool:
			return a13Bool
		case ir.ConstInt:
			return a13Int
		case ir.ConstFloat:
			return a13Float
		case ir.ConstDecimal:
			return a13Decimal
		case ir.ConstString:
			return a13String
		case ir.ConstEmptyList, ir.ConstEmptySet, ir.ConstEmptyVector:
			return a13Container
		}
		return a13Unknown
	case *ir.Arith:
		switch n.Domain() {
		case ir.DomainInt:
			return a13Int
		case ir.DomainFloat:
			return a13Float
		case ir.DomainDecimal:
			return a13Decimal
		}
		return a13Unknown
	case *ir.Concat:
		return a13String
	case *ir.Render:
		return a13String
	case *ir.Match:
		return a13Bool
	case *ir.Compare:
		// A comparison's answer is a Bool whatever its operands are. A branch
		// on a comparison (`fib`'s tail `if`) reads its condition's shape
		// through here.
		return a13Bool
	case *ir.Make:
		switch n.Kind() {
		case ir.MakeStruct, ir.MakeRecord:
			return a13Struct
		case ir.MakeVariant:
			return a13Variant
		case ir.MakeDistinct:
			return a13Distinct
		case ir.MakeTuple:
			return a13Tuple
		case ir.MakeList, ir.MakeSet, ir.MakeVector, ir.MakeMap, ir.MakeRange:
			return a13Container
		}
		return a13Unknown
	case *ir.Copy:
		return axis13ShapeOf(f, n.Src(), depth+1)
	case *ir.Bind:
		return axis13ShapeOf(f, n.Src(), depth+1)
	case *ir.Ref:
		if n.Kind() == ir.RefFunc {
			return a13Func
		}
		if n.Kind() == ir.RefLocal {
			// Resolved by declaration identity to a parameter, whose shape
			// the declaration records.
			for _, p := range f.Params() {
				if p.Sym == n.Sym() {
					return axis13Of(p.Shape)
				}
			}
		}
		return a13Unknown
	}
	return a13Unknown
}

// axis13Of is `ir.ValShape` in this file's vocabulary.
//
// A second enumeration rather than a reuse, on purpose: this derivation is an
// instrument independent of the rule it checks. `internal/ir`'s `shapeOfTemp`
// walks every writer and this walks `Func.Def`; if the two ever disagree about
// one graph, one of them is wrong, and the plant tests below are where that
// shows up. The mapping is written out so a reader can see the independence.
func axis13Of(s ir.ValShape) axis13Shape {
	switch s {
	case ir.ValUnit:
		return a13Unit
	case ir.ValBool:
		return a13Bool
	case ir.ValInt:
		return a13Int
	case ir.ValFloat:
		return a13Float
	case ir.ValDecimal:
		return a13Decimal
	case ir.ValString:
		return a13String
	case ir.ValStruct:
		return a13Struct
	case ir.ValTuple:
		return a13Tuple
	case ir.ValVariant:
		return a13Variant
	case ir.ValDistinct:
		return a13Distinct
	case ir.ValContainer:
		return a13Container
	case ir.ValFunc:
		return a13Func
	}
	return a13Unknown
}

// axis13Blocker names why the derivation cannot answer for t. Empty when the
// derivation answers.
//
// It is the same walk as axis13ShapeOf and it stops where that one gives up,
// so the two cannot disagree about which sites are unknown.
func axis13Blocker(f *ir.Func, t ir.Temp, depth int) string {
	if t == ir.NoTemp {
		return "NoTemp"
	}
	if depth > 16 {
		return "copy chain deeper than 16"
	}
	def := f.Def(t)
	if def == nil {
		// Nothing in the function writes it: a parameter, or Slot-declared
		// storage.
		for _, p := range f.Params() {
			if p.Temp == t {
				if p.Shape != ir.ValUnknown {
					return ""
				}
				// The parameter's kind is one `irParamShape` states no shape
				// for: an existential, a bound-free type parameter, an
				// rtOpaque leaf or a generic std instance. See
				// internal/irbuild/irparamshape.go for the closed list.
				return "a PARAMETER whose kind states no shape (irParamShape declined)"
			}
		}
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				if s, isSlot := in.(*ir.Slot); isSlot && s.Slot() == t {
					return "SLOT-declared storage (ir.Slot HAS an *ir.Type)"
				}
			}
		}
		return "no definition and not a parameter"
	}
	switch n := def.(type) {
	case *ir.Copy:
		return axis13Blocker(f, n.Src(), depth+1)
	case *ir.Bind:
		return axis13Blocker(f, n.Src(), depth+1)
	case *ir.Proj:
		return "*ir.Proj (carries the FIELD's identity, not its type)"
	case *ir.Call:
		return "*ir.Call (Callee() is a *Symbol; no signature, and ir.Func has no result type)"
	case *ir.Ref:
		if n.Kind() == ir.RefFunc {
			return ""
		}
		if n.Kind() != ir.RefLocal {
			return "*ir.Ref " + n.Kind().String() + " (the Symbol carries no type)"
		}
		// A RefLocal resolves to a parameter or to one of this function's
		// own Binds (`RuleLocalDeclared` establishes that). A Bind's shape is
		// `shapeOfTemp(f, bind.Src())`; a parameter's is what its declaration
		// records.
		for _, p := range f.Params() {
			if p.Sym == n.Sym() {
				if p.Shape != ir.ValUnknown {
					return ""
				}
				return "*ir.Ref local -> a PARAMETER whose kind states no shape"
			}
		}
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				if bd, isBind := in.(*ir.Bind); isBind && bd.Sym() == n.Sym() {
					return "*ir.Ref local -> a BIND (derivable today; the edge is unwired)"
				}
			}
		}
		return "*ir.Ref local -> neither (RuleLocalDeclared should have caught this)"
	case *ir.Const:
		if n.Kind() == ir.ConstMarker {
			return "*ir.Const ConstMarker"
		}
	}
	if axis13ShapeOf(f, t, 0) == a13Unknown {
		return "unclassified"
	}
	return ""
}

// axis13Site is one operand condition at one position in one graph.
type axis13Site struct {
	cond string
	fn   string
	why  string
	// temp is the operand temporary this position is about, recorded so a
	// second derivation reads the same position the first did.
	temp  ir.Temp
	shape axis13Shape
	want  axis13Shape
}

// axis13Sites enumerates every position in f at which one of the VM's
// operand-shape conditions is checked, and what the derivation answers there.
//
// Operand conditions:
//
//	 1 Branch.Cond            must be Bool
//	 2 Arith Int Lhs          must be Int
//	 3 Arith Int Rhs          must be Int
//	 4 Arith Float Lhs        must be Float
//	 5 Arith Float Rhs        must be Float
//	 6 Concat.Part(i)         must be String
//	 7 Proj Field subject     must be a struct
//	 8 Proj Slot subject      must be a tuple
//	 9 Proj Payload subject   must be a variant
//	10 Proj Inner subject     must be a distinct
//	11 Compare Lhs and Rhs    must both be the node's own shape
//	12 io.print operand       must be String
//	13 Arith Decimal Lhs      must be Decimal
//	14 Arith Decimal Rhs      must be Decimal
//
// The twelfth is not a position in the graph. The VM's io.print check is on a
// host function's argument, and which Go function a `Crosses()` call lands on
// is the consumer's own binding, so it has no row here.
func axis13Sites(f *ir.Func) []axis13Site {
	var out []axis13Site
	add := func(cond string, t ir.Temp, want axis13Shape) {
		out = append(out, axis13Site{cond: cond, fn: f.Name(), why: axis13Blocker(f, t, 0),
			temp: t, shape: axis13ShapeOf(f, t, 0), want: want})
	}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			switch n := in.(type) {
			case *ir.Arith:
				var lhs, rhs string
				var want axis13Shape
				switch n.Domain() {
				case ir.DomainInt:
					lhs, rhs, want = "02 arith Int lhs", "03 arith Int rhs", a13Int
				case ir.DomainFloat:
					lhs, rhs, want = "04 arith Float lhs", "05 arith Float rhs", a13Float
				case ir.DomainDecimal:
					lhs, rhs, want = "13 arith Decimal lhs", "14 arith Decimal rhs", a13Decimal
				default:
					continue
				}
				add(lhs, n.Lhs(), want)
				if n.Rhs() != ir.NoTemp {
					add(rhs, n.Rhs(), want)
				}
			case *ir.Concat:
				for i := range n.NumParts() {
					add("06 concat part", n.Part(i), a13String)
				}
			case *ir.Proj:
				switch n.Kind() {
				case ir.ProjField, ir.ProjRecordField:
					add("07 proj field subject", n.Subject(), a13Struct)
				case ir.ProjSlot:
					add("08 proj tuple subject", n.Subject(), a13Tuple)
				case ir.ProjPayload:
					add("09 proj payload subject", n.Subject(), a13Variant)
				case ir.ProjInner:
					add("10 proj inner subject", n.Subject(), a13Distinct)
				}
			case *ir.Compare:
				// Both operands against the NODE's shape, which is what
				// `compareInstr` checks: it type-asserts each operand against
				// the arm `n.Shape()` selected. So `want` comes off the node,
				// where the arith rows above take it from the domain.
				want := axis13Of(n.Shape())
				add("11 compare lhs", n.Lhs(), want)
				add("11 compare rhs", n.Rhs(), want)
			}
		}
		if br, isBranch := b.Term().(*ir.Branch); isBranch {
			add("01 branch cond", br.Cond(), a13Bool)
		}
	}
	return out
}

// TestIROperandShapesMatchTheirConditions checks every operand-shape
// condition at every site in the retained corpus and std populations: where
// the derivation answers, the answer must be the shape the condition
// requires. It also logs how many sites are answered and why the rest are
// not.
//
// The comparison's condition occupies two rows, one per operand, because
// `compareInstr` checks both against the same `n.Shape()` and a merged row
// could not say which operand was unanswerable.
func TestIROperandShapesMatchTheirConditions(t *testing.T) {
	fns := axis13Population(t)
	if len(fns) == 0 {
		t.Fatal("no retained function was collected, so every count below is vacuous")
	}

	type tally struct{ answered, unknown, wrong int }
	per := map[string]*tally{}
	var unknownWitness = map[string]string{}
	total := tally{}
	for _, f := range fns {
		for _, s := range axis13Sites(f) {
			row := per[s.cond]
			if row == nil {
				row = &tally{}
				per[s.cond] = row
			}
			switch {
			case s.shape == a13Unknown:
				row.unknown++
				total.unknown++
				if unknownWitness[s.cond] == "" {
					unknownWitness[s.cond] = s.fn
				}
			case s.shape == s.want:
				row.answered++
				total.answered++
			default:
				row.wrong++
				total.wrong++
				t.Errorf("the derivation answered %s where %s requires %s, in %s",
					s.shape, s.cond, s.want, s.fn)
			}
		}
	}

	conds := make([]string, 0, len(per))
	for k := range per {
		conds = append(conds, k)
	}
	sort.Strings(conds)
	t.Logf("%d retained functions", len(fns))
	t.Logf("%-26s %8s %8s   %s", "condition", "answered", "unknown", "an unknown's function")
	for _, c := range conds {
		t.Logf("%-26s %8d %8d   %s", c, per[c].answered, per[c].unknown, unknownWitness[c])
	}
	t.Logf("TOTAL SITES %d: %d answered, %d unknown",
		total.answered+total.unknown, total.answered, total.unknown)

	// Why each unknown is unknown, overall and per condition.
	blockers := map[string]int{}
	byCond := map[string]map[string]int{}
	for _, f := range fns {
		for _, s := range axis13Sites(f) {
			if s.shape != a13Unknown {
				continue
			}
			blockers[s.why]++
			if byCond[s.cond] == nil {
				byCond[s.cond] = map[string]int{}
			}
			byCond[s.cond][s.why]++
		}
	}
	bk := make([]string, 0, len(blockers))
	for k := range blockers {
		bk = append(bk, k)
	}
	sort.Slice(bk, func(i, j int) bool { return blockers[bk[i]] > blockers[bk[j]] })
	t.Logf("WHY THE %d UNKNOWNS ARE UNKNOWN:", total.unknown)
	for _, k := range bk {
		t.Logf("  %4d  %s", blockers[k], k)
	}
	for _, c := range conds {
		if len(byCond[c]) == 0 {
			continue
		}
		parts := make([]string, 0, len(byCond[c]))
		for w, n := range byCond[c] {
			parts = append(parts, fmt.Sprintf("%dx %s", n, w))
		}
		sort.Strings(parts)
		t.Logf("  %-26s %s", c, strings.Join(parts, "; "))
	}

	// The condition rows with a population at all, and those the derivation
	// answers at every site.
	withPop, fullyAnswered := 0, []string{}
	for _, c := range conds {
		withPop++
		if per[c].unknown == 0 && per[c].answered > 0 {
			fullyAnswered = append(fullyAnswered, c)
		}
	}
	t.Logf("%d condition rows have a retained population; "+
		"%d are answered at EVERY site: %v", withPop, len(fullyAnswered), fullyAnswered)
}

// TestIROperandShape_TheDerivationCatchesAPlant checks that the derivation
// calls a planted wrong operand wrong and a control right.
//
// Without it, a derivation that answered `a13Unknown` for everything would
// produce the same "0 contradictions" as a correct one.
//
// It also cross-checks the shipped rule. `axis13ShapeOf` derives from
// `ir.Func.Def` (the FIRST writer) and `internal/ir`'s `shapeOfTemp` derives
// from EVERY writer and declines on disagreement. Two independent derivations
// agreeing on the plant is a stronger reading than either alone; if they
// disagree, one of them is wrong about the graph.
func TestIROperandShape_TheDerivationCatchesAPlant(t *testing.T) {
	pos := ir.At("plant.nomi", 2, 3)

	// CONTROL: an Int arithmetic over two Int constants. The derivation must
	// be silent and `ir.Lint` must accept it.
	clean := ir.NewFunc(pos, "clean")
	cb := clean.NewBlock(pos, "entry")
	a, b, d := clean.NewTemp(), clean.NewTemp(), clean.NewTemp()
	cb.Append(ir.NewInt(pos, a, 2))
	cb.Append(ir.NewInt(pos, b, 3))
	cb.Append(ir.NewArith(pos, d, ir.OpAdd, ir.IntArith(ir.OverflowFaults), a, b))
	cb.SetTerm(ir.NewReturn(pos, d))
	if err := ir.Lint(clean); err != nil {
		t.Fatalf("THE CONTROL FAILED, so the plant below measures nothing: %v", err)
	}
	for _, s := range axis13Sites(clean) {
		if s.shape != s.want {
			t.Fatalf("CONTROL: %s derived %s, want %s", s.cond, s.shape, s.want)
		}
	}

	// PLANT: the same shape with a STRING on the left.
	bad := ir.NewFunc(pos, "bad")
	bb := bad.NewBlock(pos, "entry")
	sa, sb, sd := bad.NewTemp(), bad.NewTemp(), bad.NewTemp()
	bb.Append(ir.NewString(pos, sa, "two"))
	bb.Append(ir.NewInt(pos, sb, 3))
	bb.Append(ir.NewArith(pos, sd, ir.OpAdd, ir.IntArith(ir.OverflowFaults), sa, sb))
	bb.SetTerm(ir.NewReturn(pos, sd))

	found := false
	for _, s := range axis13Sites(bad) {
		if s.cond != "02 arith Int lhs" {
			continue
		}
		found = true
		if s.shape != a13String {
			t.Fatalf("PLANT: this derivation answered %s for a String operand, want String",
				s.shape)
		}
	}
	if !found {
		t.Fatal("PLANT: the site enumerator never reached the Int lhs condition")
	}

	// The shipped rule must agree.
	ruleSees, other := shapeRuleFires(t, bad)
	if other != "" {
		t.Fatalf("the plant trips another rule (%s), so it does not isolate the operand shape", other)
	}
	if !ruleSees {
		t.Fatal("this derivation sees the plant and ir.Lint's RuleOperandShape does not; " +
			"two derivations over one graph disagree and one of them is wrong")
	}

	// The VM's own check still fires on the same graph.
	mod := ir.NewModule("plant")
	mod.AddFunc(bad)
	if _, err := vm.New(mod, io.Discard).Run("bad"); err == nil {
		t.Fatal("the VM ran the planted graph, so it has no runtime operand check here")
	} else {
		t.Logf("ir.Lint refuses the plant at the gate; the VM refuses it at execution: %v", err)
	}
}

// TestIROperandShape_TheDerivationAndTheRuleAgreeThroughAParameter covers the
// arm the plant above does not take: an operand whose shape comes from a
// parameter's recorded shape, directly or through a RefLocal read.
//
// This derivation and `internal/ir`'s `shapeWritten` each have their own
// RefLocal-to-parameter lookup. The two must agree on it, or an answered count
// here would describe this file's derivation rather than the rule.
func TestIROperandShape_TheDerivationAndTheRuleAgreeThroughAParameter(t *testing.T) {
	pos := ir.At("plant.nomi", 5, 1)

	for _, arm := range []struct {
		name string
		// read answers the temporary the Arith's lhs should be: the
		// parameter's own, or a RefLocal's destination.
		read func(f *ir.Func, b *ir.Block, pt ir.Temp) ir.Temp
	}{
		{"direct", func(_ *ir.Func, _ *ir.Block, pt ir.Temp) ir.Temp { return pt }},
		{"through a RefLocal", func(f *ir.Func, b *ir.Block, _ ir.Temp) ir.Temp {
			r := f.NewTemp()
			b.Append(ir.NewRefLocal(pos, r, f.Params()[0].Sym))
			return r
		}},
	} {
		build := func(shape ir.ValShape) *ir.Func {
			f := ir.NewFunc(pos, "planted")
			pt := f.AddParam(ir.NewSymbol("n"), shape)
			ty := ir.IntType
			if shape == ir.ValString {
				ty = ir.StringType
			}
			f.SetType(pt, ty)
			b := f.NewBlock(pos, "entry")
			one, sum := f.NewTemp(), f.NewTemp()
			lhs := arm.read(f, b, pt)
			b.Append(ir.NewInt(pos, one, 1))
			b.Append(ir.NewArith(pos, sum, ir.OpAdd, ir.IntArith(ir.OverflowFaults),
				lhs, one))
			b.SetTerm(ir.NewReturn(pos, sum))
			return f
		}

		t.Run(arm.name, func(t *testing.T) {
			// THE CONTROL. A parameter recorded Int at an Int position: both
			// derivations must answer Int and the rule must be silent.
			good := build(ir.ValInt)
			if err := ir.Lint(good); err != nil {
				t.Fatalf("THE CONTROL FAILED, so the plant measures nothing: %v", err)
			}
			sawGood := false
			for _, s := range axis13Sites(good) {
				if s.cond != "02 arith Int lhs" {
					continue
				}
				sawGood = true
				if s.shape != a13Int {
					t.Fatalf("CONTROL: this derivation answered %s for a parameter "+
						"recorded Int, so it is not reading the recorded shape at all",
						s.shape)
				}
			}
			if !sawGood {
				t.Fatal("CONTROL: the site enumerator never reached the Int lhs condition")
			}

			// THE PLANT. The same graph with String recorded.
			bad := build(ir.ValString)
			sawBad := false
			for _, s := range axis13Sites(bad) {
				if s.cond != "02 arith Int lhs" {
					continue
				}
				sawBad = true
				if s.shape != a13String {
					t.Fatalf("PLANT: this derivation answered %s for a parameter "+
						"recorded String, want String", s.shape)
				}
			}
			if !sawBad {
				t.Fatal("PLANT: the site enumerator never reached the Int lhs condition")
			}

			// And the two must agree.
			fires, other := shapeRuleFires(t, bad)
			if other != "" {
				t.Fatalf("the plant trips another rule (%s), so it does not isolate "+
					"the recorded shape", other)
			}
			if !fires {
				t.Fatal("this derivation reads the recorded parameter shape and " +
					"ir.Lint's RuleOperandShape does not; two derivations over one " +
					"graph disagree, so every answered count in this package " +
					"describes this file rather than the rule")
			}
		})
	}
}

// shapeRuleFires splits ir.Lint's answer into RuleOperandShape and the rest,
// so a plant that trips another rule cannot be credited to it.
func shapeRuleFires(t *testing.T, f *ir.Func) (shapeRule bool, otherRule string) {
	t.Helper()
	err := ir.Lint(f)
	if err == nil {
		return false, ""
	}
	le, isLint := err.(*ir.LintError)
	if !isLint {
		t.Fatalf("ir.Lint returned %T: %v", err, err)
	}
	for _, v := range le.Violations {
		if v.Rule == ir.RuleOperandShape {
			shapeRule = true
			continue
		}
		if v.Rule == ir.RuleTempType {
			// A wrong operand shape is also a wrong operand TYPE, so the
			// stored-type rule fires on every plant here by construction.
			// It has its own plants in internal/ir/typelint_test.go.
			continue
		}
		otherRule = string(v.Rule) + ": " + v.Why
	}
	return shapeRule, otherRule
}

// axis13Population collects the corpus's and std's retained functions.
func axis13Population(t *testing.T) []*ir.Func {
	t.Helper()
	var fns []*ir.Func

	_, files := corpusAnalysis(t)
	for _, f := range files {
		if f.Prog == nil || f.Res == nil {
			continue
		}
		res, _, err := GenerateIR(f.Prog)
		if err != nil {
			t.Fatalf("%s refused on a second Generate: %v", f.Rel, err)
		}
		for _, m := range res.IR {
			fns = append(fns, m.Funcs()...)
		}
	}
	corpus := len(fns)

	buildStdlibIndex()
	prev := irFuncObserved
	irFuncObserved = func(origin irFuncOrigin, _ string, f *ir.Func, _ bool) {
		if origin == irFromStd && f != nil {
			fns = append(fns, f)
		}
	}
	buildStdlibIndex()
	irFuncObserved = prev
	t.Logf("population: %d corpus, %d std", corpus, len(fns)-corpus)
	return fns
}
