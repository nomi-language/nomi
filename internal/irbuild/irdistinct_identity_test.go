package irbuild

// A VM-built distinct carries its module-qualified identity, and the wrapping
// distinct rule retains the shapes it should. These tests check both.

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// TestIRDistinctIdentityIsQualified checks, over the corpus and std
// populations, that every distinct identity the graph records and the VM
// builds is module-qualified.
//
// A distinct record carries its type name, and dispatch, `rt.Hash` and
// `rt.Equal` key on it. Getting it wrong is silent: a value without the
// qualified name misses every impl and `==` falls back to structural
// comparison, a wrong answer with no error. The identity is the
// module-qualified name (`duration.Duration`, `instant.Instant`,
// `codepoints.Codepoint`), so a VM that stamps a bare `Duration` builds the
// wrong value.
//
// Every check here passes vacuously if nothing is built, which is why the two
// counts are asserted first. The failing side is planted on a graph by
// TestIRDistinct_ABareIdentityOnAGraphIsReported, so a zero here cannot be a
// walk that stopped finding distinct nodes.
func TestIRDistinctIdentityIsQualified(t *testing.T) {
	checked, built, bad := 0, 0, []string(nil)
	for _, fn := range append(irDistinctCorpusFuncs(t), irDistinctStdFuncs(t)...) {
		c, b, v := irDistinctIdentities(fn)
		checked += c
		built += b
		bad = append(bad, v...)
	}

	t.Logf("%d distinct identities checked over both populations, %d of them on a value "+
		"the VM BUILT and returned", checked, built)
	for _, v := range bad {
		t.Errorf("%s carries no module qualifier. The declared identity is "+
			"`duration.Duration`; a bare name misses every impl SILENTLY", v)
	}
	if checked == 0 {
		t.Fatal("no distinct node was found in either population, so every check above " +
			"is vacuous rather than passing")
	}
	if built == 0 {
		t.Fatal("the VM returned no distinct record at all, so the check that the " +
			"name reaches the value is vacuous; only the graph half ran")
	}
}

// irDistinctIdentities reads every distinct identity one retained function
// carries: the symbol on each `MakeDistinct` and `ProjInner`, and the
// `TypeName` on the value the VM returns when it returns one.
//
// The returned value is checked separately from the graph because they are
// two different claims. The graph half says the producer recorded a qualified
// name; the value half says that name reaches the built value, which is the
// only thing a consumer keying dispatch on it cares about. A machine that
// built a distinct and dropped the name would pass the first and fail the
// second.
func irDistinctIdentities(fn *ir.Func) (checked, built int, bad []string) {
	see := func(where, name string) {
		checked++
		if !irQualifiedRuntimeName(name) {
			bad = append(bad, fmt.Sprintf("%s: %s = %q", fn.Name(), where, name))
		}
	}
	for _, b := range fn.Blocks() {
		for _, in := range b.Instrs() {
			switch n := in.(type) {
			case *ir.Make:
				if n.Kind() == ir.MakeDistinct {
					see("make", n.Typ().Name())
				}
			case *ir.Proj:
				if n.Kind() == ir.ProjInner {
					see("inner", n.Sym().Name())
				}
			}
		}
	}
	mod := ir.NewModule("vmid " + fn.Name())
	mod.AddFunc(fn)
	m := vm.New(mod, io.Discard)
	for _, args := range vmArgShapes(mod, fn) {
		out, err := vmRunSymV(m, fn.Sym(), args...)
		if err != nil {
			continue
		}
		if r, name, _, isRecord := vmRecord(out); isRecord && r.Desc.Kind == rt.KindDistinct {
			built++
			see("returned", name)
		}
		break
	}
	return checked, built, bad
}

// TestIRDistinct_ABareIdentityOnAGraphIsReported is the failing side of the
// check above, on a graph the producer could have built.
//
// `fn f(n: Int): Meters { Meters(n) }` with the symbol interned bare. Two
// violations are expected and both are asserted: the node's own symbol, and
// the `TypeName` on the value the machine returns, so a check that only read
// the graph would report one and this says which.
func TestIRDistinct_ABareIdentityOnAGraphIsReported(t *testing.T) {
	build := func(name string) *ir.Func {
		pos := ir.At("meters.nomi", 2, 3)
		f := ir.NewFuncFor(pos, ir.NewSymbol("f"))
		tbl := ir.NewTable()
		param := f.AddParam(tbl.Symbol(new(int), "n"), ir.ValUnknown)
		f.SetType(param, ir.IntType)
		entry := f.NewBlock(pos, "entry")
		typ := tbl.Symbol(new(int), name)
		m := ir.NewMakeDistinct(pos, f.NewTemp(), typ, param)
		f.SetType(m.Dst(), ir.NewDistinctType(typ, ir.IntType))
		entry.Append(m)
		entry.SetTerm(ir.NewReturn(pos, m.Dst()))
		if err := ir.Lint(f); err != nil {
			t.Fatalf("the plant is not a well-formed graph, so it measures nothing: %v", err)
		}
		return f
	}

	checked, built, bad := irDistinctIdentities(build("Meters"))
	if checked != 2 || built != 1 {
		t.Fatalf("the plant produced %d identities and %d built values, want 2 and 1; "+
			"the check below would be measuring the wrong thing", checked, built)
	}
	if len(bad) != 2 {
		t.Fatalf("a BARE `Meters` produced %d violations, want 2 (the node's symbol and "+
			"the returned value's TypeName): %v", len(bad), bad)
	}

	// The clean control: the same graph with the qualified spelling.
	if _, _, ok := irDistinctIdentities(build("meters.Meters")); len(ok) != 0 {
		t.Fatalf("the qualified control reported %v, so the rule fires on a name it "+
			"must accept", ok)
	}
}

// irQualifiedRuntimeName reports whether name is a MODULE-QUALIFIED runtime
// type name, by `rt.ShortTypeName`'s own rule.
//
// That rule, quoted from it: "Module names come from file names and are
// lower-case; declared names are upper-case. So the qualifier is exactly the
// leading run of lower-case segments". So a qualified name has at least one
// leading lower-case segment and an upper-case segment after it, and
// `Day.Hours` (a NAMESPACED declaration, which owns both segments) is not
// qualified even though it contains a dot.
func irQualifiedRuntimeName(name string) bool {
	segs := strings.Split(name, ".")
	if len(segs) < 2 {
		return false
	}
	lead := []rune(segs[0])
	return len(lead) > 0 && unicode.IsLower(lead[0])
}

// TestIRQualifiedRuntimeName checks the predicate the identity checks above
// rely on.
//
// The cases are the ones the rule has to separate, and the third is why
// "contains a dot" is not the rule: `type Day.Hours Int` is a NAMESPACED
// declaration whose Nomi name owns both segments, so its qualified runtime
// name is `add_test.Day.Hours` and its bare one already has a dot in it.
func TestIRQualifiedRuntimeName(t *testing.T) {
	for _, c := range []struct {
		name      string
		qualified bool
	}{
		{"duration.Duration", true},
		{"Duration", false},
		{"Day.Hours", false},
		{"add_test.Day.Hours", true},
		{"", false},
	} {
		if got := irQualifiedRuntimeName(c.name); got != c.qualified {
			t.Errorf("%q: qualified=%v, want %v", c.name, got, c.qualified)
		}
	}
}

// TestIRDistinct_WhichShapesAreRetained is the witness set for the distinct
// rule, over real source, in both directions.
//
// A two-level distinct is not a program at all: `type A Int; type B A;
// fn f(b: B): A { A(b) }` is refused by `distinctCallNamed` (`argument type
// mismatch (A wraps Int, got B)`), and the unwrap spelling is refused by
// `scalarUnwrap` for the same reason. So that edge is checked in the predicate
// table of TestIRDistinct_ThePredicateEdges, because no lowerable source
// reaches it.
func TestIRDistinct_WhichShapesAreRetained(t *testing.T) {
	cases := []struct {
		name     string
		decl     string
		body     string
		retained bool
	}{
		{"construct", "type Meters Int", "fn f(n: Int): Meters {\n  Meters(n * 3)\n}\n", true},
		{"unwrap", "type Meters Int", "fn f(m: Meters): Int {\n  Int(m) + 1\n}\n", true},
		{"round trip", "type Meters Int", "fn f(m: Meters): Meters {\n  Meters(Int(m) * 2)\n}\n", true},
		{"over String", "type Tag String", "fn f(s: String): Tag {\n  Tag(s)\n}\n", true},
		{"a marker", "type Expired", "fn f(): Expired {\n  Expired\n}\n", true},
		// A composite inner is retained through irCompositeDistinct.
		{"tuple inner", "type Pair (Int, Int)", "fn f(p: Pair): Pair {\n  p\n}\n", true},
		{"list inner", "type Ns List<Int>", "fn f(p: Ns): Ns {\n  p\n}\n", true},
		{"a list of functions inner", "type Fs List<(Int) -> Int>", "fn f(p: Fs): Fs {\n  p\n}\n", true},
		{"an enum inner", "type Wrapped Maybe<Int>", "fn f(p: Wrapped): Wrapped {\n  p\n}\n", true},
		// An inner that reaches the distinct back is not decided.
		{"an inner that reaches it back", "struct Node {\n  next: Maybe<Link>\n}\n\ntype Link Node", "fn f(p: Link): Link {\n  p\n}\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var seen []string
			prev := irFuncObserved
			irFuncObserved = func(_ irFuncOrigin, name string, f *ir.Func, _ bool) {
				if f != nil {
					seen = append(seen, name)
				}
			}
			defer func() { irFuncObserved = prev }()
			lowerIROnly(t, c.decl+"\n\n"+c.body)
			got := slicesContain(seen, "f")
			if got != c.retained {
				t.Fatalf("`f` retained=%v, the row declares %v (observed %v)", got, c.retained, seen)
			}
		})
	}
}

// TestIRDistinct_ThePredicateEdges checks `irWrappingDistinct` at the shapes a
// `*typeDef` can be that the rule has to separate, including the two-level one
// no lowerable program reaches.
func TestIRDistinct_ThePredicateEdges(t *testing.T) {
	inner := &typeDef{nomi: "A", isDistinct: true, inner: kindInt, lowerable: true}
	for _, c := range []struct {
		name string
		d    *typeDef
		want bool
	}{
		{"over Int", inner, true},
		{"over String", &typeDef{nomi: "T", isDistinct: true, inner: kindString, lowerable: true}, true},
		{"over Bool", &typeDef{nomi: "T", isDistinct: true, inner: kindBool, lowerable: true}, true},
		{"a marker", &typeDef{nomi: "E", isDistinct: true, inner: kindInvalid, lowerable: true}, false},
		{"two levels", &typeDef{nomi: "B", isDistinct: true, inner: named(inner), lowerable: true}, false},
		{"a struct", &typeDef{nomi: "P", fields: []fieldDef{{k: kindInt}}, lowerable: true}, false},
		{"unlowerable", &typeDef{nomi: "U", isDistinct: true, inner: kindInt}, false},
		{"nil", nil, false},
	} {
		if got := irWrappingDistinct(c.d); got != c.want {
			t.Errorf("%s: irWrappingDistinct=%v, want %v", c.name, got, c.want)
		}
	}
	// The leaf domain is the scalars plus the wrapping distincts, and the
	// scalar half must keep answering.
	for _, k := range []kind{kindInt, kindFloat, kindBool, kindString} {
		if !irRetainedLeafKind(k) {
			t.Errorf("%s left the retained domain", k.nomi())
		}
	}
	if irRetainedLeafKind(kindUnit) || irRetainedLeafKind(kindInvalid) {
		t.Error("Unit or kindInvalid entered the retained domain")
	}
}

// irDistinctCorpusFuncs is every retained `ir.Func` over the corpus that
// mentions a distinct node. Filtered so the identity check above walks tens of
// functions rather than the whole population twice.
func irDistinctCorpusFuncs(t *testing.T) []*ir.Func {
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
				if irDistinctMentions(fn) {
					out = append(out, fn)
				}
			}
		}
	}
	return out
}

// irDistinctStdFuncs is the same over `std/`.
func irDistinctStdFuncs(t *testing.T) []*ir.Func {
	t.Helper()
	buildStdlibIndex()
	var out []*ir.Func
	prev := irFuncObserved
	irFuncObserved = func(origin irFuncOrigin, _ string, f *ir.Func, _ bool) {
		if origin == irFromStd && f != nil && irDistinctMentions(f) {
			out = append(out, f)
		}
	}
	buildStdlibIndex()
	irFuncObserved = prev
	return out
}

func irDistinctMentions(f *ir.Func) bool {
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			switch n := in.(type) {
			case *ir.Make:
				if n.Kind() == ir.MakeDistinct {
					return true
				}
			case *ir.Proj:
				if n.Kind() == ir.ProjInner {
					return true
				}
			}
		}
	}
	return false
}
