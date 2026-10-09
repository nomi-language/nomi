package irbuild

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestChannelSpecShapeChecksAreLoadBearing is the NEGATIVE half, and it exists
// because a positive-only guard cannot tell a shape check from a name check.
//
// Every clause of both `matches` functions decides REPRESENTATION, and each is
// supposed to fail toward NO ANCHOR rather than toward lowering against a layout
// nobody wrote. `TestChannelSpecsMatchStdSource` only says the real declaration
// matches; that passes just as well if every clause is deleted. So each clause
// is mutated here, one at a time, from the real declaration.
//
// The `pub type X Int` and `pub type X SomeStruct` rows are the two the marker
// gate exists for: stdgenhost.go's table says a row must not lean on
// `scalarKind(inner) == kindInvalid`, because scalarKind answers kindInvalid for
// a NON-scalar inner just as readily as for no inner at all.
func TestChannelSpecShapeChecksAreLoadBearing(t *testing.T) {
	lib := std.Load()
	fa := lib.Files["channels"]
	if fa == nil {
		t.Fatal("std/channels did not load")
	}

	// --- the generic host family -------------------------------------------
	spec := genHostSpecFor("std/channels", "Sender")
	if spec == nil {
		t.Fatal("no Sender spec row")
	}
	real := stdGenHostDeclIn(fa, spec)
	if real == nil || !spec.matches(real) {
		t.Fatal("the real Sender declaration does not match; every mutation below is vacuous")
	}
	hostMutations := []struct {
		name string
		bend func(d *ast.ExternType)
	}{
		{"a private declaration", func(d *ast.ExternType) { d.Public = false }},
		{"a renamed declaration", func(d *ast.ExternType) { d.Name = "Producer" }},
		{"no type parameter", func(d *ast.ExternType) { d.TypeParams = nil }},
		{"a renamed type parameter", func(d *ast.ExternType) { d.TypeParams = []ast.TypeParam{{Name: "E"}} }},
		{"a second type parameter", func(d *ast.ExternType) {
			d.TypeParams = []ast.TypeParam{{Name: "T"}, {Name: "U"}}
		}},
		{"a where bound the spec does not name", func(d *ast.ExternType) {
			d.WhereClauses = []ast.WhereConstraint{{Name: "T", Bounds: []ast.TypeExpr{&ast.SimpleType{Name: "Hashable"}}}}
		}},
		{"an inline bound the spec does not name", func(d *ast.ExternType) {
			d.TypeParams = []ast.TypeParam{{Name: "T", Bounds: []ast.TypeExpr{&ast.SimpleType{Name: "Hashable"}}}}
		}},
		{"a foreign Go binding", func(d *ast.ExternType) { d.ForeignName = "chan.Sender" }},
		{"a body item", func(d *ast.ExternType) { d.HasBody = true; d.Items = []ast.Node{&ast.FuncDef{Name: "x"}} }},
		{"a decorator that is not derive", func(d *ast.ExternType) {
			d.Decorators = []ast.Decorator{{Name: "inline"}}
		}},
	}
	for _, m := range hostMutations {
		bent := *real
		m.bend(&bent)
		if spec.matches(&bent) {
			t.Errorf("Sender: the spec still matches %s — that clause is not load-bearing", m.name)
		}
	}
	// And the one decorator that must NOT break the anchor, because it is
	// metadata about work already done rather than a gap.
	derived := *real
	derived.Decorators = []ast.Decorator{{Name: "derive"}}
	if !spec.matches(&derived) {
		t.Error("Sender: `derive` broke the anchor; it is synthesized into an ordinary impl block")
	}
	// An attached `//!` test must not break it either. A `//!` is a line in a
	// doc comment and cannot affect layout, and its own refusal is recorded at
	// its own position by tests.go's attachedTestCases, so charging it to the
	// type as well would double-charge it and cascade to every function over
	// the type. See stdStructSpec.matches.
	prompted := *real
	prompted.AttachedTests = []ast.AttachedTest{{}}
	if !spec.matches(&prompted) {
		t.Error("Sender: an attached `//!` test broke the anchor; a doc example cannot change a type's layout")
	}

	// --- the marker family --------------------------------------------------
	mSpec := &stdMarkerSpecs[0]
	if mSpec.nomi != "ChannelClosed" {
		t.Fatalf("expected the first marker row to be ChannelClosed, got %q", mSpec.nomi)
	}
	sym := fa.ModuleScope.Lookup("ChannelClosed")
	if sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	mReal, isDecl := sym.Node.(*ast.TypeDef)
	if !isDecl || !mSpec.matches(mReal) {
		t.Fatal("the real ChannelClosed declaration does not match; every mutation below is vacuous")
	}
	markerMutations := []struct {
		name string
		bend func(d *ast.TypeDef)
	}{
		{"a private declaration", func(d *ast.TypeDef) { d.Public = false }},
		{"a renamed declaration", func(d *ast.TypeDef) { d.Name = "Closed" }},
		{"the OPAQUE clause the row does not name", func(d *ast.TypeDef) { d.Opaque = true }},
		{"a SCALAR inner", func(d *ast.TypeDef) { d.InnerTypeExpr = &ast.SimpleType{Name: "Int"} }},
		{"a NON-SCALAR inner", func(d *ast.TypeDef) {
			d.InnerTypeExpr = &ast.GenericType{Name: "List", Params: []ast.TypeExpr{&ast.SimpleType{Name: "Int"}}}
		}},
		{"a body item", func(d *ast.TypeDef) { d.HasBody = true; d.Items = []ast.Node{&ast.FuncDef{Name: "x"}} }},
		{"a decorator that is not derive", func(d *ast.TypeDef) { d.Decorators = []ast.Decorator{{Name: "inline"}} }},
	}
	for _, m := range markerMutations {
		bent := *mReal
		m.bend(&bent)
		if mSpec.matches(&bent) {
			t.Errorf("ChannelClosed: the spec still matches %s — that clause is not load-bearing", m.name)
		}
	}
	// And the two things that must NOT break the marker anchor.
	mDerived := *mReal
	mDerived.Decorators = []ast.Decorator{{Name: "derive"}}
	if !mSpec.matches(&mDerived) {
		t.Error("ChannelClosed: `derive` broke the anchor; it is synthesized into an ordinary impl block")
	}
	mPrompted := *mReal
	mPrompted.AttachedTests = []ast.AttachedTest{{}}
	if !mSpec.matches(&mPrompted) {
		t.Error("ChannelClosed: an attached `//!` test broke the anchor; a doc example cannot change a type's layout")
	}
}

// TestChannelSpecsMatchStdSource asserts the anchors are really built from the
// std source in this build, for all three tables channels needs: the two
// stdgenhost.go adds and the stdGenStructSpecs row. Without it, a rename in std/channels.nomi,
// a changed clause, a moved declaration or an added bound all produce a silent
// loss of capability rather than a failure.
func TestChannelSpecsMatchStdSource(t *testing.T) {
	lib := std.Load()
	fa := lib.Files["channels"]
	if fa == nil || fa.ModuleScope == nil {
		t.Fatal("std/channels did not load; every assertion below would be vacuous")
	}

	hostValidated := stdGenHostValidated()
	if len(hostValidated) != len(stdGenHostSpecs) || len(stdGenHostSpecs) == 0 {
		t.Fatalf("stdGenHostSpecs is %d rows and validated is %d", len(stdGenHostSpecs), len(hostValidated))
	}
	// The table is not homogeneous (it holds `std/vectors.Vector` beside the
	// channel halves), so each row resolves in the module its own `origin`
	// names, and a row whose module did not load is a failure rather than a
	// skip. Otherwise a typo in
	// `origin` would silently stop checking that row, which is exactly the
	// silent-loss-of-capability this test exists to prevent.
	for i := range stdGenHostSpecs {
		s := &stdGenHostSpecs[i]
		if !hostValidated[i] {
			t.Errorf("%s.%s: std does not declare it in the shape the spec describes", s.origin, s.nomi)
		}
		mod := strings.TrimPrefix(s.origin, "std/")
		rowFa := lib.Files[mod]
		if rowFa == nil || rowFa.ModuleScope == nil {
			t.Errorf("%s.%s: std/%s did not load, so this row is unchecked rather than checked",
				s.origin, s.nomi, mod)
			continue
		}
		if stdGenHostDeclIn(rowFa, s) == nil {
			t.Errorf("%s.%s: the spec's name does not resolve to a host type declaration in %s",
				s.origin, s.nomi, s.origin)
		}
	}

	markerValidated := stdMarkerValidated()
	if len(stdMarkerSpecs) == 0 {
		t.Fatal("stdMarkerSpecs is empty")
	}
	for i := range stdMarkerSpecs {
		s := &stdMarkerSpecs[i]
		if !markerValidated[i] {
			t.Errorf("%s.%s: std does not declare it in the shape the spec describes", s.origin, s.nomi)
		}
		if !rtNamed(s.goType) {
			t.Errorf("%s.%s: goType is not a named top-level type in rt", s.origin, s.nomi)
		}
	}

	if channelSpec == nil {
		t.Fatal("the stdGenStructSpecs row for std/channels.Channel is missing")
	}
	genValidated := stdGenStructValidated()
	for i := range stdGenStructSpecs {
		if &stdGenStructSpecs[i] != channelSpec {
			continue
		}
		if !genValidated[i] {
			t.Error("std/channels.Channel: std does not declare it in the shape the spec row describes")
		}
	}
}

// TestChannelHalvesAreTwoTypesOverOneObject is the DESIGN rule as an assertion.
//
// std deliberately splits `Channel<T>` into `sender`/`receiver` and puts every
// operation on a HALF: there is no `Channel.send`, no `Channel.receive` and no
// `Channel.close`, and `close` is on `Sender` ALONE because a consumer closing
// what it reads ends the stream for every producer still writing. Adding an
// operation reachable from the whole channel would make the split bypassable,
// and being bypassable is the only way the split could fail to pay — so the rule
// is asserted rather than left in a comment.
//
// The population is derived from `channelFuncs` itself for the negative half,
// and from STD'S OWN SOURCE for the positive half. That direction matters: a
// check that only read `channelFuncs` would pass if somebody deleted a row, and
// a check that only read std would pass if somebody added an arm std does not
// declare. Both are asserted.
func TestChannelHalvesAreTwoTypesOverOneObject(t *testing.T) {
	for key, fn := range channelFuncs {
		owner, method, found := strings.Cut(key, ".")
		if !found {
			t.Fatalf("channelFuncs key %q is not Owner.method", key)
		}
		if fn.owner != owner {
			t.Errorf("%s: row's owner is %q", key, fn.owner)
		}
		if owner == "Channel" && (method == "send" || method == "receive" || method == "close") {
			t.Errorf("%s: an operation reachable from the whole channel makes std's split bypassable; "+
				"send and receive belong on the halves and close on Sender alone", key)
		}
	}
	if _, onSender := channelFuncs["Sender.close"]; !onSender {
		t.Error("Sender.close is missing; close is the operation the split exists to keep off Receiver")
	}
	if _, onReceiver := channelFuncs["Receiver.close"]; onReceiver {
		t.Error("Receiver.close exists; a consumer closing what it reads is the exact bug the split prevents")
	}

	// ASK THE GRAPH: every `pub host fn` std declares in an `impl` block of
	// std/channels, read off the source, must be either lowered by a
	// channelFuncs row under the SAME owner or absent from the table entirely.
	// A row whose owner disagrees with std's impl receiver would lower a call
	// against the wrong half.
	lib := std.Load()
	decls := map[string]bool{}
	for _, n := range lib.Nodes["channels"] {
		ib, isImpl := n.(*ast.ImplBlock)
		if !isImpl {
			continue
		}
		recv := analysis.TypeExprBaseName(ib.Receiver)
		for _, item := range ib.Items {
			ef, isExtern := item.(*ast.ExternFunc)
			if !isExtern {
				continue
			}
			decls[recv+"."+ef.Name] = true
		}
	}
	if len(decls) == 0 {
		t.Fatal("read no host fn declarations out of std/channels; the assertions below would be vacuous")
	}
	for key := range channelFuncs {
		if !decls[key] {
			t.Errorf("channelFuncs lowers %q and std/channels declares no such function on that owner; "+
				"std declares %v", key, sortedKeys(decls))
		}
	}
}

// TestStdGenHostChannelsAreAllGeneric evaluates the precondition stdgenhost.go's
// header states rather than restating it.
//
// The header says the stdlib-SIGNATURE arm is absent deliberately and costs
// nothing, because EVERY declaration in std/channels is a `pub host fn ...<T>`
// and so short-circuits at `stdCandidateFor`'s `case generic:` before any
// signature question is asked. If std ever declares a non-generic channel
// function, that reasoning stops holding and this fails instead of the comment
// quietly going stale.
func TestStdGenHostChannelsAreAllGeneric(t *testing.T) {
	lib := std.Load()
	nodes := lib.Nodes["channels"]
	if len(nodes) == 0 {
		t.Fatal("std/channels has no nodes; this assertion would be vacuous")
	}
	seen := 0
	for _, n := range nodes {
		ib, isImpl := n.(*ast.ImplBlock)
		if !isImpl {
			continue
		}
		for _, item := range ib.Items {
			ef, isExtern := item.(*ast.ExternFunc)
			if !isExtern {
				continue
			}
			seen++
			if len(ef.TypeParams) == 0 {
				t.Errorf("%s.%s is NOT generic, so it reaches the stdlib signature path "+
					"and stdgenhost.go's stated reason for having no stdTypeKind arm does not hold",
					analysis.TypeExprBaseName(ib.Receiver), ef.Name)
			}
		}
	}
	if seen != len(channelFuncs) {
		t.Errorf("std/channels declares %d host fns and channelFuncs has %d rows; "+
			"an undeclared row lowers nothing and an unlowered declaration should be a named refusal", seen, len(channelFuncs))
	}
}

// TestChannelDefsArePackageNeutral is the precondition for sharing these defs
// process-wide, asserted rather than assumed.
//
// A `*typeDef` reached from two gens must render to the same Go type text in
// every generated package, because a stdFunc's kinds are built once and compared
// by POINTER against a call site's kinds in a different gen. opaque.go's
// TestOpaqueDefsArePackageNeutral is this assertion for the scalar family; the
// clause `packageNeutral` needs for THIS family is the one added beside
// genStructOf's, and without it `Sender<SomeUserType>` would answer true on the
// strength of the rt type's name alone.
func TestChannelDefsArePackageNeutral(t *testing.T) {
	for i := range stdMarkerSpecs {
		if !stdMarkerKind(i).packageNeutral() {
			t.Errorf("%s is not package-neutral; a shared def must be", stdMarkerSpecs[i].nomi)
		}
	}
	senderInt, ok := sharedGenHostInstance(senderSpec, []kind{kindInt})
	if !ok {
		t.Fatal("Sender<Int> did not intern; every assertion below is vacuous")
	}
	if !senderInt.packageNeutral() {
		t.Error("Sender<Int> is not package-neutral")
	}
	// The NEGATIVE half, which is the half the rtDeclared arm alone gets wrong.
	// A per-gen def is not package-neutral, so an instance over one must not be.
	//
	// Spelled against sharedGenHostInstance rather than through the gen-scoped
	// door, because this table is what the assertion is about: with the
	// per-gen fallback, `g.genHostInstance` answers true for
	// `Sender<Point>` by design and would make this row read backwards. The
	// paired positive — that the per-gen def it hands back is NOT neutral —
	// is TestGenStructPerGenInstanceIsNotPackageNeutral.
	local := &typeDef{nomi: "Point", lowerable: true}
	if _, shared := sharedGenHostInstance(senderSpec, []kind{named(local)}); shared {
		t.Error("Sender<Point> interned a shared def over a per-package type argument")
	}
}

// TestChannelKindsAgreeAcrossEveryChannel is the cross-channel check. A pair
// that shares a guard cannot audit itself, so the audit compares two
// independent paths that answer the same question.
//
// The two paths are the ANNOTATION door (`typeOf` over a written type
// expression) and the INFERENCE door (`project` over the type the CHECKER
// solved). They share no code: one walks an `*ast.TypeExpr` through
// stdGenHostTypeOf / stdMarkerNamed / stdGenStructTypeOf, the other walks an
// `analysis.Type` through genHostOfType / stdMarkerOfType / genStructOfType. An
// arm present in one and absent in the other is the defect this catches.
//
// The population is derived from one source of truth, the spec tables, so
// adding a row cannot pass by not being listed.
func TestChannelKindsAgreeAcrossEveryChannel(t *testing.T) {
	src := "import std/channels.{Channel, ChannelClosed, Receiver, Sender}\n" +
		// `Task<T>` is a stdGenHostSpecs row too, and the population below is
		// derived from that table — so the witness has to name it or the
		// ANNOTATION door has nothing in scope to resolve and reads as a refusal.
		"import std/tasks.Task\n\n" +
		"fn a(_x: Channel<Int>): Int { 1 }\n" +
		"fn e(_x: Task<Int>): Int { 1 }\n" +
		"fn b(_x: Sender<Int>): Int { 1 }\n" +
		"fn c(_x: Receiver<Int>): Int { 1 }\n" +
		"fn d(_x: ChannelClosed): Int { 1 }\n\n" +
		"test \"t\" {\n  assert True\n}\n"
	p, err := AnalyzeSource("agree", src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	g := newGen(p.Entry(), "nomimod0", nil, nil, -1, nil)
	g.declareTypes()
	g.declareFuncs()

	// The generic host rows, both doors.
	for i := range stdGenHostSpecs {
		s := &stdGenHostSpecs[i]
		ann := g.typeOf(&ast.GenericType{Name: s.nomi, Params: []ast.TypeExpr{&ast.SimpleType{Name: "Int"}}})
		inf := g.project(&analysis.DistinctType{
			Origin: s.origin, Name: s.nomi, Opaque: true,
			TypeParams: s.params, TypeArgs: []analysis.Type{analysis.TypeInt},
		})
		if ann == kindInvalid {
			t.Errorf("%s<Int>: the ANNOTATION door refuses", s.nomi)
		}
		if ann != inf {
			t.Errorf("%s<Int>: annotation gives %s, inference gives %s — two answers to one question",
				s.nomi, ann.nomi(), inf.nomi())
		}
	}

	// The marker rows, both doors.
	for i := range stdMarkerSpecs {
		s := &stdMarkerSpecs[i]
		ann := g.typeOf(&ast.SimpleType{Name: s.nomi})
		inf := g.project(&analysis.DistinctType{Origin: s.origin, Name: s.nomi})
		if ann == kindInvalid {
			t.Errorf("%s: the ANNOTATION door refuses", s.nomi)
		}
		if ann != inf {
			t.Errorf("%s: annotation gives %s, inference gives %s — two answers to one question",
				s.nomi, ann.nomi(), inf.nomi())
		}
	}

	// The generic std struct rows, both doors. Every row shares the `project`
	// arm, so a Set or a Range regressing is the same defect as a Channel.
	for i := range stdGenStructSpecs {
		s := &stdGenStructSpecs[i]
		if len(s.params) != 1 {
			continue
		}
		args := []kind{kindInt}
		want, shared := sharedGenStructInstance(s, args)
		if !shared {
			continue
		}
		inf := g.project(&analysis.StructType{
			Origin: s.origin, Name: s.nomi,
			TypeParams: s.params, TypeArgs: []analysis.Type{analysis.TypeInt},
		})
		if inf != want {
			t.Errorf("%s<Int>: the INFERENCE door gives %s where the interned instance is %s",
				s.nomi, inf.nomi(), want.nomi())
		}
	}
}

// TestChannelClosedIsAMarkerAndNotALeafWithContents pins the one field that
// distinguishes the two families stdgenhost.go holds, because getting it
// backwards is silent in both directions.
//
// `ChannelClosed` is a genuine zero-sized marker: two of them ARE always equal
// and there is nothing to hash, so `rtOpaque` must be FALSE. `Sender<Int>` is a
// leaf with contents, so `rtOpaque` must be true; without it, `a == b` on such
// a type would answer true for any two values.
func TestChannelClosedIsAMarkerAndNotALeafWithContents(t *testing.T) {
	for i := range stdMarkerSpecs {
		d := stdMarkerDefs()[i]
		if d.rtOpaque {
			t.Errorf("%s is rtOpaque; a nil-inner marker has no contents to protect", stdMarkerSpecs[i].nomi)
		}
		if !d.isDistinct || d.inner != kindInvalid || !d.rtDeclared {
			t.Errorf("%s: def shape is wrong (isDistinct=%v inner=%v rtDeclared=%v)",
				stdMarkerSpecs[i].nomi, d.isDistinct, d.inner, d.rtDeclared)
		}
	}
	k, ok := sharedGenHostInstance(senderSpec, []kind{kindInt})
	if !ok {
		t.Fatal("Sender<Int> did not intern")
	}
	if !k.def.rtOpaque {
		t.Error("Sender<Int> is not rtOpaque; it holds a channel, and every arm written for a " +
			"zero-sized marker would answer for a value with contents")
	}
	if k.def.inner != kindInvalid || !k.def.isDistinct || !k.def.rtDeclared {
		t.Errorf("Sender<Int>: def shape is wrong (isDistinct=%v inner=%v rtDeclared=%v)",
			k.def.isDistinct, k.def.inner, k.def.rtDeclared)
	}
}

// TestChannelRtLayoutMatchesTheSpec is the layout guard: rt's exported field
// names and the spec row's must agree, because the builder spells field
// selectors from the SPEC and nothing else checks that the field it names
// exists in rt.
//
// The pairing of Nomi field to Go field is what the builder reads and nothing
// else states it, so this asserts the names.
func TestChannelRtLayoutMatchesTheSpec(t *testing.T) {
	if channelSpec == nil {
		t.Fatal("no Channel spec row")
	}
	ty := reflect.TypeFor[rtChannelInt]()
	if ty.NumField() != len(channelSpec.fields) {
		t.Fatalf("rt.Channel has %d fields, the spec row has %d", ty.NumField(), len(channelSpec.fields))
	}
}

// rtChannelInt is the instantiation TestChannelRtLayoutMatchesTheSpec reflects
// over. `reflect.TypeFor` needs a concrete type, and the field NAMES are the
// same at every instantiation.
type rtChannelInt = rt.Channel[int64]
