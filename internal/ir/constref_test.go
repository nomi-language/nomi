package ir_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// TestConst_EveryKindIsConstructibleAndReadsNothing covers the `const` class.
//
// The invariant that makes it a class rather than a grab bag: a constant reads
// no temporary. If any of these appended a use, it would be a `make`.
func TestConst_EveryKindIsConstructibleAndReadsNothing(t *testing.T) {
	pos := ir.At("c.nomi", 4, 9)
	elem := ir.NewSymbol("Int")
	marker := ir.NewSymbol("std/app.Marker")

	cases := []struct {
		kind  ir.ConstKind
		node  *ir.Const
		check func(*testing.T, *ir.Const)
	}{
		{ir.ConstUnit, ir.NewUnit(pos, 1), nil},
		{ir.ConstBool, ir.NewBool(pos, 1, true), func(t *testing.T, c *ir.Const) {
			if !c.Bool() {
				t.Error("NewBool(true).Bool() is false")
			}
			if ir.NewBool(pos, 1, false).Bool() {
				t.Error("NewBool(false).Bool() is true")
			}
		}},
		{ir.ConstInt, ir.NewInt(pos, 1, -9223372036854775808), func(t *testing.T, c *ir.Const) {
			// MinInt64 round-trips. A Decimal or Float payload here would lose
			// it, which is exactly why the payloads are separate fields.
			if c.Int() != -9223372036854775808 {
				t.Errorf("Int() = %d, want MinInt64", c.Int())
			}
		}},
		{ir.ConstFloat, ir.NewFloat(pos, 1, 0.5), func(t *testing.T, c *ir.Const) {
			if c.Float() != 0.5 {
				t.Errorf("Float() = %v", c.Float())
			}
		}},
		{ir.ConstDecimal, ir.NewDecimal(pos, 1, "0.1"), func(t *testing.T, c *ir.Const) {
			// A Decimal is carried as EXACT TEXT. `0.1` has no float64, so a
			// Decimal that round-trips through one is a wrong number printed
			// without complaint.
			if c.Text() != "0.1" {
				t.Errorf("Text() = %q, want the exact source text", c.Text())
			}
			if c.Float() != 0 {
				t.Errorf("a Decimal literal populated the float payload with %v", c.Float())
			}
		}},
		{ir.ConstString, ir.NewString(pos, 1, "hi"), func(t *testing.T, c *ir.Const) {
			if c.Text() != "hi" {
				t.Errorf("Text() = %q", c.Text())
			}
		}},
		{ir.ConstMarker, ir.NewMarker(pos, 1, marker), func(t *testing.T, c *ir.Const) {
			if c.Type() != marker {
				t.Error("marker lost its type")
			}
		}},
		{ir.ConstEmptyList, ir.NewEmptyList(pos, 1, elem), nil},
		{ir.ConstEmptySet, ir.NewEmptySet(pos, 1, elem), nil},
		{ir.ConstEmptyVector, ir.NewEmptyVector(pos, 1, elem), nil},
	}

	seen := map[ir.ConstKind]bool{}
	for _, c := range cases {
		if c.node.Kind() != c.kind {
			t.Errorf("%s: Kind() = %s", c.kind, c.node.Kind())
		}
		if got := c.node.AppendUses(nil); len(got) != 0 {
			t.Errorf("%s reads %v; a constant that reads a temporary is a `make`", c.kind, got)
		}
		if c.node.Dst() != 1 {
			t.Errorf("%s: Dst() = %s", c.kind, c.node.Dst())
		}
		if c.node.Pos() != pos {
			t.Errorf("%s: Pos() = %s", c.kind, c.node.Pos())
		}
		if got := c.node.String(); !strings.Contains(got, c.kind.String()) {
			t.Errorf("%s renders as %q, which does not name its kind", c.kind, got)
		}
		if c.check != nil {
			c.check(t, c.node)
		}
		seen[c.kind] = true
	}
	if len(seen) != len(cases) {
		t.Errorf("%d distinct kinds across %d cases; two cases share a kind",
			len(seen), len(cases))
	}
	t.Logf("%d const kinds", len(seen))

	// An empty container may be built at no element type: the builder's three
	// owners build it at the Unit instantiation, because the type argument is
	// not knowable at those positions. So a container's type ARGUMENT may be
	// absent and Undischarged says so, while a marker's DECLARATION may not.
	for _, c := range []*ir.Const{
		ir.NewEmptyList(pos, 1, nil),
		ir.NewEmptySet(pos, 1, nil),
		ir.NewEmptyVector(pos, 1, nil),
	} {
		if !c.Undischarged() {
			t.Errorf("%s with a nil element type does not report Undischarged, so a "+
				"consumer cannot tell a container at no type argument from one at a type",
				c.Kind())
		}
		if c.Type() != nil {
			t.Errorf("%s at no element type reports a type", c.Kind())
		}
		if !strings.Contains(c.String(), "undischarged") {
			t.Errorf("%s renders as %q, which does not say its type argument is absent",
				c.Kind(), c.String())
		}
	}
	// PLANT THE POSITIVE ON BOTH SIDES. Undischarged must SEPARATE cases, or
	// `return true` would satisfy the loop above; and the container kinds must
	// be the only members, or `return c.typ == nil` on every kind would make a
	// scalar undischarged.
	for _, c := range []*ir.Const{
		ir.NewEmptyList(pos, 1, elem),
		ir.NewEmptySet(pos, 1, elem),
		ir.NewEmptyVector(pos, 1, elem),
		ir.NewUnit(pos, 1),
		ir.NewInt(pos, 1, 3),
		ir.NewString(pos, 1, "s"),
		ir.NewMarker(pos, 1, marker),
	} {
		if c.Undischarged() {
			t.Errorf("%s reports Undischarged", c.Kind())
		}
	}
	// A marker names a declaration, so it cannot name none.
	if got := recovered(func() { ir.NewMarker(pos, 1, nil) }); got == "" {
		t.Error("NewMarker accepted a nil declaration")
	}
	if got := recovered(func() { ir.NewInt(pos, ir.NoTemp, 1) }); got == "" {
		t.Error("NewInt accepted NoTemp as a destination; a constant nothing reads is dead")
	}
}

// TestRef_MobilityIsTwoIndependentQuestions covers the `ref` class and the two
// predicates that decide whether a read may be moved.
//
// Forces is "reading this may RUN something", true for a `once` cell whose
// initializer fires on first read. Volatile covers what Forces cannot carry
// for RefAppField: an app-field write rebinds an app field for the rest of a
// block, so two reads may ANSWER differently while running nothing.
//
// Neither implies the other, and the table below is what says so. A `once`
// read is forcing and NOT volatile — the first read runs the initializer and
// every later read loads the same value — so a consumer may reuse a read it
// has already performed. An app-field read is volatile and NOT forcing, so a
// consumer may perform it freely and may not reuse it across a rebinding.
func TestRef_MobilityIsTwoIndependentQuestions(t *testing.T) {
	pos := ir.At("r.nomi", 12, 3)
	sym := ir.NewSymbol("x")

	cases := []struct {
		kind     ir.RefKind
		node     *ir.Ref
		forces   bool
		volatile bool
	}{
		{ir.RefLocal, ir.NewRefLocal(pos, 1, sym), false, false},
		{ir.RefOnce, ir.NewRefOnce(pos, 1, sym), true, false},
		{ir.RefAppField, ir.NewRefAppField(pos, 1, sym), false, true},
		{ir.RefFunc, ir.NewRefFunc(pos, 1, sym), false, false},
		{ir.RefTypeWitness, ir.NewRefTypeWitness(pos, 1, sym), false, false},
	}

	forcing, volatile, stable := 0, 0, 0
	for _, c := range cases {
		if c.node.Kind() != c.kind {
			t.Errorf("%s: Kind() = %s", c.kind, c.node.Kind())
		}
		if c.node.Sym() != sym {
			t.Errorf("%s lost its symbol", c.kind)
		}
		if got := c.node.AppendUses(nil); len(got) != 0 {
			t.Errorf("%s reads %v; a Ref names a declaration, not a temporary", c.kind, got)
		}
		if got := c.node.Forces(); got != c.forces {
			t.Errorf("%s: Forces() = %v, want %v", c.kind, got, c.forces)
		}
		if got := c.node.Volatile(); got != c.volatile {
			t.Errorf("%s: Volatile() = %v, want %v", c.kind, got, c.volatile)
		}
		// Stable is the conjunction, and it is what `internal/irbuild` reads for
		// `expr.pure` at all eight of its sites.
		if got, want := c.node.Stable(), !c.forces && !c.volatile; got != want {
			t.Errorf("%s: Stable() = %v, want %v", c.kind, got, want)
		}
		if c.node.Forces() {
			forcing++
		}
		if c.node.Volatile() {
			volatile++
		}
		if c.node.Stable() {
			stable++
		}
	}
	// PLANT A POSITIVE ON EACH. `return false` would satisfy four of five rows
	// for either predicate, and `return true` would satisfy three of five for
	// Stable, so each needs a count that says it SEPARATES cases.
	if forcing != 1 {
		t.Errorf("%d of %d ref kinds force; want exactly one (`once`)", forcing, len(cases))
	}
	if volatile != 1 {
		t.Errorf("%d of %d ref kinds are volatile; want exactly one (an app field)",
			volatile, len(cases))
	}
	if stable != 3 {
		t.Errorf("%d of %d ref kinds are stable; want three — every kind that neither "+
			"forces nor is volatile", stable, len(cases))
	}
	// AND THE TWO ARE DISJOINT HERE, which is the claim the header makes. A
	// predicate pair where one implied the other would need only one of them.
	for _, c := range cases {
		if c.node.Forces() && c.node.Volatile() {
			t.Errorf("%s both forces and is volatile; no kind should, and if one ever "+
				"does the two predicates need a shared consumer rule rather than two", c.kind)
		}
	}

	if got := recovered(func() { ir.NewRefLocal(pos, 1, nil) }); got == "" {
		t.Error("NewRefLocal accepted a nil symbol; a read with no declaration names nothing")
	}
	if got := recovered(func() { ir.NewRefOnce(pos, ir.NoTemp, sym) }); got == "" {
		t.Error("NewRefOnce accepted NoTemp as a destination")
	}
}

// TestTable_SymbolInterningMakesTheIdentityRuleDeliverable is the other half
// of the rule TestSymbol_IdentityIsThePointerNotTheText pins.
//
// That test says two same-named declarations are two symbols, which
// `NewSymbol` gives for free because it MINTS. It says nothing about the
// converse, and the converse is what a consumer actually needs: two reads of
// ONE declaration must resolve to ONE symbol, or a symbol is a receipt for a
// lookup rather than an identity. `Table.Declare`'s comment names the same
// failure for `Decl`, and `Table.Symbol` closes it for `Symbol`.
func TestTable_SymbolInterningMakesTheIdentityRuleDeliverable(t *testing.T) {
	tab := ir.NewTable()
	type decl struct{ n string }
	first, second := &decl{"Error"}, &decl{"Error"}

	a := tab.Symbol(first, "Error")
	again := tab.Symbol(first, "a different name entirely")
	b := tab.Symbol(second, "Error")

	if a != again {
		t.Error("two lookups of one producer identity returned two symbols, so a Symbol " +
			"is a receipt rather than an identity and two reads of one `once` would not " +
			"compare equal")
	}
	if a.Name() != "Error" {
		t.Errorf("a repeat call replaced the name: %q. One declaration has one name for "+
			"the life of the table, exactly as Declare guarantees", a.Name())
	}
	if a == b {
		t.Error("two same-named declarations interned to one symbol, which is the " +
			"compare-by-printed-name defect the whole identity rule exists to prevent")
	}
	if tab.Symbols() != 2 {
		t.Errorf("Symbols() = %d after interning two declarations three times, want 2",
			tab.Symbols())
	}
	if !strings.Contains(tab.TableSummary(), "2 symbols") {
		t.Errorf("TableSummary() = %q, which does not report the symbol count — so a "+
			"test that wanted to say the table was populated could not", tab.TableSummary())
	}
	if got := recovered(func() { tab.Symbol(nil, "x") }); got == "" {
		t.Error("Table.Symbol accepted a nil token; a declaration with no producer " +
			"identity cannot be interned")
	}
}

// TestSymbol_IdentityIsThePointerNotTheText pins the identity rule.
//
// Comparing declarations by printed name is a defect this repository has
// already had: two same-named `Error` types compared equal and produced the
// diagnostic `expected Error, got Error`, which names no actionable
// difference. Two symbols with one name are two declarations.
func TestSymbol_IdentityIsThePointerNotTheText(t *testing.T) {
	a := ir.NewSymbol("Error")
	b := ir.NewSymbol("Error")
	if a == b {
		t.Fatal("two symbols minted separately with the same name are equal, so the IR " +
			"compares declarations by printed name")
	}
	if a.Name() != b.Name() {
		t.Fatal("two symbols with the same name report different names, so the test above " +
			"could have passed for the wrong reason")
	}
	// PLANT A POSITIVE: the same symbol must equal itself, or inequality above
	// would be meaningless.
	same := a
	if same != a {
		t.Fatal("a symbol does not equal itself")
	}

	// Nodes carry the symbol, not its text, so two reads of same-named
	// declarations in different modules stay distinguishable.
	pos := ir.At("s.nomi", 1, 1)
	ra := ir.NewRefLocal(pos, 1, a)
	rb := ir.NewRefLocal(pos, 2, b)
	if ra.Sym() == rb.Sym() {
		t.Error("two reads of same-named declarations resolve to one symbol")
	}
	if (*ir.Symbol)(nil).Name() != "" {
		t.Error("a nil symbol's Name panics or is non-empty; String is used in panic messages")
	}
}

// TestCopy_IsTheMultipleWriterPrimitive pins the one instruction that exists
// because this IR is not SSA.
func TestCopy_IsTheMultipleWriterPrimitive(t *testing.T) {
	pos := ir.At("c.nomi", 2, 2)
	c := ir.NewCopy(pos, 5, 7)
	if c.Dst() != 5 || c.Src() != 7 {
		t.Errorf("Copy lost an operand: %s", c)
	}
	if got := c.AppendUses(nil); len(got) != 1 || got[0] != 7 {
		t.Errorf("Copy reads %v, want [t7]", got)
	}
	for _, bad := range [][2]ir.Temp{{ir.NoTemp, 7}, {5, ir.NoTemp}} {
		if got := recovered(func() { ir.NewCopy(pos, bad[0], bad[1]) }); got == "" {
			t.Errorf("NewCopy accepted %v", bad)
		}
	}

	// Two instructions writing one temporary is legal and is the whole reason
	// Copy exists: the arms of a branch assign one destination, which is what
	// gen.slot / gen.fixSlot already does.
	f := ir.NewFunc(pos, "f")
	entry := f.NewBlock(pos, "entry")
	dst := f.NewTemp()
	entry.Append(ir.NewInt(pos, dst, 1))
	entry.Append(ir.NewInt(pos, dst, 2))
	if len(entry.Instrs()) != 2 {
		t.Fatalf("two writes to one temporary were rejected; %d instructions",
			len(entry.Instrs()))
	}
	if entry.Instrs()[0].Dst() != entry.Instrs()[1].Dst() {
		t.Error("the two writes did not target the same temporary")
	}
}

// TestTemp_RenderingIsStableAndNoTempIsDistinct guards the strings that appear
// in panic messages and in the witness's logs.
func TestTemp_RenderingIsStableAndNoTempIsDistinct(t *testing.T) {
	if got := ir.NoTemp.String(); got != "_" {
		t.Errorf("NoTemp renders as %q", got)
	}
	if got := ir.Temp(3).String(); got != "t3" {
		t.Errorf("Temp(3) renders as %q", got)
	}
	if got := ir.BlockID(2).String(); got != "b2" {
		t.Errorf("BlockID(2) renders as %q", got)
	}
	f := ir.NewFunc(ir.At("t.nomi", 1, 1), "f")
	first, second := f.NewTemp(), f.NewTemp()
	if first == ir.NoTemp {
		t.Error("the first allocated temporary is NoTemp, so an absent operand and a real " +
			"one are the same value")
	}
	if first == second {
		t.Error("NewTemp returned the same temporary twice")
	}
	if got := f.Name(); got != "f" {
		t.Errorf("Func.Name() = %q", got)
	}
}
