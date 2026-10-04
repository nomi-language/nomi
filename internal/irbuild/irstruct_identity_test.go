package irbuild

// A VM-built struct carries its identity at two levels: the module-qualified
// type name, and the bare declared field names. These tests check both, over
// the retained corpus and std populations and over planted graphs.

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// TestIRStructIdentityIsQualified checks, over both populations, that every
// struct identity the graph records and the VM builds is correct.
//
// Level one is the type name. Impl dispatch, `Hash` and `Equal` key on it,
// and a bare one is silent: the value misses every impl and `==` falls back to
// structural comparison, a wrong answer with no error. The identity is the
// module-qualified spelling, and every `*typeDef` symbol in this package goes
// through `irTypeSymName`.
//
// Level two is the field names. A struct's fields are keyed by the bare
// declared name, and Display prints them that way, so a wrong or missing key
// is the same silent wrong answer one level in.
//
// The counts are asserted first because every check here passes vacuously if
// nothing is built.
func TestIRStructIdentityIsQualified(t *testing.T) {
	checked, built, fields, bad := 0, 0, 0, []string(nil)
	for _, fn := range append(irStructCorpusFuncs(t), irStructStdFuncs(t)...) {
		c, b, fl, v := irStructIdentities(fn)
		checked += c
		built += b
		fields += fl
		bad = append(bad, v...)
	}

	t.Logf("%d struct identities checked over both populations, %d of them on a value "+
		"the VM BUILT and returned, carrying %d field keys", checked, built, fields)
	for _, v := range bad {
		t.Errorf("%s. The declared identity is `shapes.Status`, with fields keyed by "+
			"the bare declared name; a wrong one misses every impl SILENTLY", v)
	}
	if checked == 0 {
		t.Fatal("no MakeStruct node was found in either population, so every check " +
			"above is vacuous rather than passing")
	}
	if built == 0 {
		t.Fatal("the VM returned no struct record at all, so the check that the " +
			"name and the field keys reach the value is vacuous; only the graph half ran")
	}
	if fields == 0 {
		t.Fatal("every value the VM returned carried zero fields, so the field-key " +
			"half of this check is vacuous")
	}
}

// irStructIdentities reads every struct identity one retained function
// carries: the symbol and the field names on each `MakeStruct`, and the
// `TypeName` and field keys on the value the VM returns when it returns one.
//
// The returned value is checked separately from the graph, for
// `irDistinctIdentities`' reason: the graph half says the producer recorded
// the right names, the value half says they reach the built value. A machine
// that built a struct and dropped either would pass the first and fail the
// second.
//
// The field names are checked against the node rather than against a
// declaration, because a consumer reading the graph has no declaration, which
// is why they are on the node. What the node says is therefore what the value
// must carry, exactly.
func irStructIdentities(fn *ir.Func) (checked, built, fields int, bad []string) {
	var want []*ir.Make
	for _, b := range fn.Blocks() {
		for _, in := range b.Instrs() {
			if n, isMake := in.(*ir.Make); isMake && n.Kind() == ir.MakeStruct {
				checked++
				want = append(want, n)
				if !irQualifiedRuntimeName(n.Typ().Name()) {
					bad = append(bad, fmt.Sprintf("%s: make struct %q carries no module "+
						"qualifier", fn.Name(), n.Typ().Name()))
				}
				if len(n.Names()) != n.Arity() {
					bad = append(bad, fmt.Sprintf("%s: make struct %s names %d field(s) "+
						"for %d operand(s)", fn.Name(), n.Typ().Name(),
						len(n.Names()), n.Arity()))
				}
				for _, name := range n.Names() {
					if name == "" {
						bad = append(bad, fmt.Sprintf("%s: make struct %s carries an "+
							"empty field name", fn.Name(), n.Typ().Name()))
					}
				}
			}
		}
	}
	if len(want) == 0 {
		return checked, built, fields, bad
	}

	mod := ir.NewModule("vmstructid " + fn.Name())
	mod.AddFunc(fn)
	m := vm.New(mod, io.Discard)
	for _, args := range vmArgShapes(mod, fn) {
		out, err := vmRunSymV(m, fn.Sym(), args...)
		if err != nil {
			continue
		}
		sv, isStruct := out.(*rt.Record)
		if !isStruct || sv.Desc.Kind != rt.KindStruct {
			break
		}
		built++
		if !irQualifiedRuntimeName(sv.Desc.Name) {
			bad = append(bad, fmt.Sprintf("%s: the returned struct's type name %q "+
				"carries no module qualifier", fn.Name(), sv.Desc.Name))
		}
		// The LAST MakeStruct is the one whose value was returned for every
		// body this shape admits (the final expression is the construction),
		// and asserting against the node is what makes this a check on the
		// machine rather than on the declaration.
		n := want[len(want)-1]
		if sv.Desc.Name != n.Typ().Name() {
			bad = append(bad, fmt.Sprintf("%s: returned %q, the node says %q",
				fn.Name(), sv.Desc.Name, n.Typ().Name()))
		}
		if sv.NumFields() != len(n.Names()) {
			bad = append(bad, fmt.Sprintf("%s: returned %d field key(s), the node "+
				"names %d", fn.Name(), sv.NumFields(), len(n.Names())))
		}
		for _, name := range n.Names() {
			fields++
			if _, found := sv.FieldNamed(name); !found {
				bad = append(bad, fmt.Sprintf("%s: the returned struct has no key "+
					"%q, which the node names", fn.Name(), name))
			}
		}
		break
	}
	return checked, built, fields, bad
}

// TestIRStruct_ABareIdentityOnAGraphIsReported is the failing side of the
// check above, on graphs the producer could have built.
//
// Two plants and a clean control, because the check makes two separate claims
// and a single plant would only show one of them firing:
//
//	a bare TypeName       the type-name level
//	an EMPTY field name   the field-key level
//
// The second is the harder one to catch. `ir` has no declaration, so nothing
// in the graph can say a field name is misspelled, only that the value the
// machine built from it disagrees with the node, or that the name is not a
// name at all. A misspelling is caught one layer up, by `irMakeStruct` taking
// the names off `d.fields`, and by TestIRStruct_TheVMKeysFieldsByTheDeclaredName.
func TestIRStruct_ABareIdentityOnAGraphIsReported(t *testing.T) {
	build := func(typeName string, fieldNames []string) *ir.Func {
		pos := ir.At("status.nomi", 2, 3)
		f := ir.NewFuncFor(pos, ir.NewSymbol("f"))
		tbl := ir.NewTable()
		param := f.AddParam(tbl.Symbol(new(int), "n"), ir.ValUnknown)
		f.SetType(param, ir.IntType)
		entry := f.NewBlock(pos, "entry")
		typ := tbl.Symbol(new(int), typeName)
		m := ir.NewMakeStruct(pos, f.NewTemp(), typ, fieldNames, []ir.Temp{param})
		f.SetType(m.Dst(), ir.NewStructType(typ))
		entry.Append(m)
		entry.SetTerm(ir.NewReturn(pos, m.Dst()))
		if err := ir.Lint(f); err != nil {
			t.Fatalf("the plant is not a well-formed graph, so it measures nothing: %v", err)
		}
		return f
	}

	// The clean control first, so a plant that reports nothing cannot be
	// confused with a check that reports nothing about anything.
	checked, built, fields, bad := irStructIdentities(build("shapes.Status", []string{"code"}))
	if checked != 1 || built != 1 || fields != 1 {
		t.Fatalf("the control produced %d nodes, %d built values and %d field keys, "+
			"want 1, 1 and 1; the plants below would be measuring the wrong thing",
			checked, built, fields)
	}
	if len(bad) != 0 {
		t.Fatalf("the qualified control reported %v, so the rule fires on a graph it "+
			"must accept", bad)
	}

	for _, c := range []struct {
		what  string
		typ   string
		names []string
		want  int
	}{
		// The TypeName is wrong on the node AND on the value the machine
		// returns from it, so a check that read only the graph would report
		// one and this says which.
		{"a bare TypeName", "Status", []string{"code"}, 2},
		// An empty name is reported once, on the node.
		{"an empty field name", "shapes.Status", []string{""}, 1},
	} {
		_, _, _, got := irStructIdentities(build(c.typ, c.names))
		if len(got) != c.want {
			t.Errorf("%s produced %d violation(s), want %d: %v",
				c.what, len(got), c.want, got)
		}
	}
}

// TestIRStruct_TheVMKeysFieldsByTheDeclaredName checks the field-key level on
// one witness.
//
// The VM builds a struct value from `Make.Names()`. A producer that put any
// other spelling there (a Go field name such as `F_x`, say) would build a
// value whose fields no `ProjField`, `Display`, `Hash` or `Equal` can find.
//
// The assertion is the literal key set, because nothing weaker discriminates:
// a prefix test cannot tell a stdlib struct's field names apart, since they do
// not share a prefix.
func TestIRStruct_TheVMKeysFieldsByTheDeclaredName(t *testing.T) {
	const src = "struct Point {\n  x: Int\n  y: String\n}\n\n" +
		"fn f(a: Int, b: String): Point {\n  Point{x: a, y: b}\n}\n"
	var fns []*ir.Func
	prev := irFuncObserved
	irFuncObserved = func(_ irFuncOrigin, name string, fn *ir.Func, _ bool) {
		if fn != nil && name == "f" {
			fns = append(fns, fn)
		}
	}
	lowerIROnly(t, src)
	irFuncObserved = prev
	if len(fns) != 1 {
		t.Fatalf("the witness retained %d functions named `f`, want 1; the assertions "+
			"below would be measuring nothing", len(fns))
	}

	mod := ir.NewModule("vmstructwitness")
	mod.AddFunc(fns[0])
	out, err := vmRunV(vm.New(mod, io.Discard), "f", int64(7), "s")
	if err != nil {
		t.Fatalf("the VM refused the witness: %v", err)
	}
	sv, isStruct := out.(*rt.Record)
	if !isStruct || sv.Desc.Kind != rt.KindStruct {
		t.Fatalf("the VM returned %T, not a struct record", out)
	}
	if !irQualifiedRuntimeName(sv.Desc.Name) {
		t.Errorf("type name %q carries no module qualifier", sv.Desc.Name)
	}
	want := map[string]any{"x": int64(7), "y": "s"}
	if sv.NumFields() != len(want) {
		t.Fatalf("the value carries keys %v, want %v", irStructKeys(sv), []string{"x", "y"})
	}
	for name, v := range want {
		got, found := sv.FieldNamed(name)
		if !found {
			t.Errorf("no key %q; the value carries %v. A struct's fields are keyed by "+
				"the BARE declared name, so any other spelling here is a field nothing reads",
				name, irStructKeys(sv))
			continue
		}
		if got != v {
			t.Errorf("key %q holds %v, want %v", name, got, v)
		}
	}
}

func irStructKeys(sv *rt.Record) []string {
	out := make([]string, 0, sv.NumFields())
	for _, f := range sv.Layout().Fields {
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

// TestIRStruct_OneStructNamesOneFieldListEverywhere is the field-key claim over
// the whole production population, as an invariant the graph alone can check.
//
// A struct's field names come from its DECLARATION and not from the literal
// that constructs it (`structValue` walks `d.fields` to assemble the operands
// and `irMakeStruct` takes the names off the same slice), so every
// construction of one struct must name the same fields in the same ORDER,
// whatever order the source wrote them in. A producer that took them off the
// literal would disagree between two literals of one struct: `shapes.Status` is
// constructed many times, and `Point` is written `{x: 1, y: 2}` in one file
// and `{x, y}` in another.
//
// The order is part of the claim, because the operands are declaration-ordered
// and the names are parallel to them. A set comparison would pass a producer
// that paired the right names with the wrong operands.
func TestIRStruct_OneStructNamesOneFieldListEverywhere(t *testing.T) {
	byType := map[string]string{}
	seen := 0
	fns := append(irStructCorpusFuncs(t), irStructStdFuncs(t)...)
	for _, fn := range fns {
		for _, b := range fn.Blocks() {
			for _, in := range b.Instrs() {
				n, isMake := in.(*ir.Make)
				if !isMake || n.Kind() != ir.MakeStruct {
					continue
				}
				seen++
				list := strings.Join(n.Names(), ",")
				typ := n.Typ().Name()
				if prior, had := byType[typ]; had && prior != list {
					t.Errorf("%s: %s is constructed naming [%s] here and [%s] elsewhere. "+
						"A struct's field names are its DECLARATION's, in declaration "+
						"order, so two literals of one struct cannot disagree unless the "+
						"producer took them off the literal", fn.Name(), typ, list, prior)
				}
				byType[typ] = list
			}
		}
	}
	if seen == 0 {
		t.Fatal("no MakeStruct was found in either population, so this check is vacuous")
	}
	t.Logf("%d constructions over %d functions, %d distinct structs, one field list each",
		seen, len(fns), len(byType))
	if len(byType) < 2 {
		t.Fatalf("only %d distinct struct(s) reached this check, so a disagreement "+
			"between two of them could not have been observed", len(byType))
	}
}

// TestIRStruct_ThePredicateEdges checks `irRetainedStructKind` and
// `irRetainedValueKind` at the shapes each has to separate.
//
// The three predicates are asserted to be three: a struct field admits a
// retained struct through `irRetainedFieldKind`, so a struct entering
// `irRetainedLeafKind` would also admit it as an enum payload and a distinct's
// inner with nothing checking it.
func TestIRStruct_ThePredicateEdges(t *testing.T) {
	point := &typeDef{nomi: "Point", lowerable: true, fields: []fieldDef{{nomi: "x", k: kindInt}, {nomi: "y", k: kindInt}}}
	meters := &typeDef{nomi: "Meters", isDistinct: true, inner: kindInt, lowerable: true}
	for _, c := range []struct {
		name string
		d    *typeDef
		want bool
	}{
		{"of scalars", point, true},
		{"of a wrapping distinct",
			&typeDef{nomi: "W", lowerable: true, fields: []fieldDef{{nomi: "m", k: named(meters)}}}, true},
		{"one field", &typeDef{nomi: "T", lowerable: true,
			fields: []fieldDef{{nomi: "v", k: kindString}}}, true},
		{"of a struct", &typeDef{nomi: "Outer", lowerable: true,
			fields: []fieldDef{{nomi: "p", k: named(point)}}}, true},
		{"of a struct with a boxed field", &typeDef{nomi: "Outer", lowerable: true,
			fields: []fieldDef{{nomi: "b", k: named(&typeDef{nomi: "B", lowerable: true,
				fields: []fieldDef{{nomi: "x", k: kindInt, boxed: true}}})}}}, false},
		{"a boxed field", &typeDef{nomi: "B", lowerable: true,
			fields: []fieldDef{{nomi: "x", k: kindInt, boxed: true}}}, false},
		{"no fields", &typeDef{nomi: "E", lowerable: true}, true},
		{"unlowerable", &typeDef{nomi: "U",
			fields: []fieldDef{{nomi: "x", k: kindInt}}}, false},
		{"a generic instantiation", &typeDef{nomi: "G", lowerable: true,
			genericOf: &genericTemplate{}, fields: []fieldDef{{nomi: "x", k: kindInt}}}, true},
		{"an enum", &typeDef{nomi: "C", lowerable: true, isEnum: true,
			variants: []variantDef{{nomi: "A"}}}, false},
		{"a distinct", meters, false},
		{"nil", nil, false},
	} {
		if got := irRetainedStructKind(c.d); got != c.want {
			t.Errorf("%s: irRetainedStructKind=%v, want %v", c.name, got, c.want)
		}
	}

	// The three domains, and the boundary between them. A struct is in the
	// value domain and not in the leaf one; a struct field reaches it through
	// `irRetainedFieldKind` alone, and merging the two would admit structs as
	// enum payloads silently.
	if !irRetainedValueKind(named(point)) {
		t.Error("a struct of scalars is not in the retained VALUE domain")
	}
	if irRetainedLeafKind(named(point)) {
		t.Error("a struct entered the retained LEAF domain, which is an enum payload's " +
			"domain, so structs are admitted as payloads with nothing checking it")
	}
	if irScalarLeafKind(named(meters)) {
		t.Error("a wrapping distinct entered the SCALAR domain, which is `binary`'s " +
			"and `bl.call`'s operand domain")
	}
	for _, k := range []kind{kindInt, kindFloat, kindBool, kindString} {
		if !irRetainedValueKind(k) || !irRetainedLeafKind(k) || !irScalarLeafKind(k) {
			t.Errorf("%s left one of the three domains", k.nomi())
		}
	}
	if irRetainedValueKind(kindUnit) || irRetainedValueKind(kindInvalid) {
		t.Error("Unit or kindInvalid entered the retained value domain")
	}
}

// TestIRStruct_WhichShapesAreRetained is the witness set for the struct rule,
// over real source, in both directions.
//
// A row that lowers and is not retained is a decline the rule makes on
// purpose: a Map of function values is outside the field domain. A field
// that reaches the struct itself is inside it (irKindReaches).
func TestIRStruct_WhichShapesAreRetained(t *testing.T) {
	const point = "struct Point {\n  x: Int\n  y: Int\n}"
	for _, c := range []struct {
		name     string
		decl     string
		body     string
		retained bool
	}{
		{"construct", point, "fn f(n: Int): Point {\n  Point{x: n, y: 2}\n}\n", true},
		{"shorthand", point, "fn f(x: Int, y: Int): Point {\n  Point{x, y}\n}\n", true},
		{"read a struct local", point,
			"fn f(p: Point): Point {\n  p\n}\n", true},
		{"a distinct field", "type Meters Int\n\nstruct Leg {\n  len: Meters\n}",
			"fn f(m: Meters): Leg {\n  Leg{len: m}\n}\n", true},
		{"a field value that is an operator", point,
			"fn f(n: Int): Point {\n  Point{x: n + 1, y: 2}\n}\n", true},
		{"a field the literal omits", "struct D {\n  x: Int\n  y: Int = 9\n}",
			// The declared default fills the omitted field, as fillFieldDefaults does.
			"fn f(n: Int): D {\n  D{x: n}\n}\n", true},
		{"nested", point + "\n\nstruct Outer {\n  p: Point\n}",
			"fn f(p: Point): Outer {\n  Outer{p: p}\n}\n", true},
		{"a List field", point + "\n\nstruct Outer {\n  ps: List<Point>\n}",
			"fn f(p: Point): Outer {\n  Outer{ps: [p]}\n}\n", true},
		{"a field that reaches the struct itself", point + "\n\nstruct Outer {\n  p: Point\n  back: Maybe<Outer> = None\n}",
			"fn f(p: Point): Outer {\n  Outer{p: p}\n}\n", true},
		{"a Map of function values", point + "\n\nstruct Outer {\n  p: Point\n  hooks: Map<String, (Int) -> Int> = Map.empty()\n}",
			"fn f(p: Point): Outer {\n  Outer{p: p}\n}\n", false},
		{"an anonymous struct", "",
			"fn f(n: Int): {x: Int} {\n  {x: n}\n}\n", true},
		{"a struct as a call argument", point +
			"\n\nfn g(_p: Point): Int {\n  1\n}",
			"fn f(n: Int): Int {\n  g(Point{x: n, y: 2})\n}\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var seen []string
			prev := irFuncObserved
			irFuncObserved = func(_ irFuncOrigin, name string, f *ir.Func, _ bool) {
				if f != nil {
					seen = append(seen, name)
				}
			}
			defer func() { irFuncObserved = prev }()
			src := c.body
			if c.decl != "" {
				src = c.decl + "\n\n" + c.body
			}
			lowerIROnly(t, src)
			if got := slicesContain(seen, "f"); got != c.retained {
				t.Fatalf("`f` retained=%v, the row declares %v (observed %v)",
					got, c.retained, seen)
			}
		})
	}
}

// irStructCorpusFuncs is every retained `ir.Func` over the corpus that holds a
// `MakeStruct`. Filtered so the identity checks walk tens of functions rather
// than the whole population twice.
func irStructCorpusFuncs(t *testing.T) []*ir.Func {
	t.Helper()
	_, files := corpusAnalysis(t)
	var out []*ir.Func
	for _, f := range files {
		if f.Prog == nil || f.Res == nil {
			continue
		}
		res, _, err := GenerateIR(f.Prog)
		if err != nil {
			t.Fatalf("%s refused on a second Generate: %v", f.Rel, err)
		}
		for _, m := range res.IR {
			for _, fn := range m.Funcs() {
				if irStructMentions(fn) {
					out = append(out, fn)
				}
			}
		}
	}
	return out
}

// irStructStdFuncs is the same over `std/`.
func irStructStdFuncs(t *testing.T) []*ir.Func {
	t.Helper()
	buildStdlibIndex()
	var out []*ir.Func
	prev := irFuncObserved
	irFuncObserved = func(origin irFuncOrigin, _ string, f *ir.Func, _ bool) {
		if origin == irFromStd && f != nil && irStructMentions(f) {
			out = append(out, f)
		}
	}
	buildStdlibIndex()
	irFuncObserved = prev
	return out
}

func irStructMentions(f *ir.Func) bool {
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if n, isMake := in.(*ir.Make); isMake && n.Kind() == ir.MakeStruct {
				return true
			}
		}
	}
	return false
}
