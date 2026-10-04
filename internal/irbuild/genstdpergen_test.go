package irbuild

import (
	"testing"
)

// A GENERIC STD type instantiated at a PACKAGE-RELATIVE type argument —
// `Channel<Request>`, `Set<Point>`, `Sender<Request>` — interned PER GEN.
//
// A program such as `16-concurrency/message_loop/message_loop_test.nomi`
// instantiates `Channel<Request>`, where `Request` is a struct one module
// declares.
//
// # A representation, not a widening of `kind`
//
// An instance is the same `*typeDef` shape as a shared one (`genStructOf` +
// `genStructArgs`, `rtDeclared` because rt declares `rt.Channel[T]`); the only
// thing that differs is which table it is interned in. The prelude family has
// the same split: `rt.Maybe[int64]` is process-wide and `rt.Maybe[NomiT_Point]`
// is per gen (prelude.go's preludeInstanceOf). `project()` stays subtractive:
// an argument it cannot represent comes back kindInvalid and the
// instantiation refuses by name.
//
// # The three invariants that make the split safe, each asserted below
//
//  1. `kind.packageNeutral` must answer false for a per-gen instance, or a
//     shared table elsewhere would adopt it.
//  2. Two gens' instances of the same Nomi type must be DIFFERENT defs, because
//     each names its own package's Go type. Pointer identity is the builder's
//     type identity, so sharing one would name a type the other package never
//     declared.
//  3. A field kind that cannot be built must sink the whole instantiation.
//     `Set<T>`'s field is `Map<T, Bool>`, so an unhashable argument has no field
//     kind, and a def carrying kindInvalid while `lowerable` is true emits Go
//     naming nothing.

// TestGenStructPerGenInstanceIsNotPackageNeutral is invariant (1), for BOTH
// families, with the paired positive that makes the negative readable.
//
// The pairing is the point: an assertion that `Channel<Request>` is not neutral
// says nothing on its own, because a door that returned no instance at all would
// pass it the same way. So each half asserts an instance WAS produced first.
func TestGenStructPerGenInstanceIsNotPackageNeutral(t *testing.T) {
	local := &typeDef{nomi: "Request", lowerable: true}
	g := &gen{}

	for _, tc := range []struct {
		name string
		mk   func() (kind, bool)
	}{
		{"Channel<Request>", func() (kind, bool) { return g.genStructInstance(channelSpecForTest(t), named(local)) }},
		{"Sender<Request>", func() (kind, bool) { return g.genHostInstance(senderSpec, named(local)) }},
		{"Set<Point>", func() (kind, bool) { return g.genStructInstance(setSpec, kindInt) }},
	} {
		k, ok := tc.mk()
		if !ok {
			t.Errorf("%s: the per-gen door produced no instance, so every assertion about it is vacuous", tc.name)
			continue
		}
		if k.tag != tagNamed || k.def == nil {
			t.Errorf("%s: kind is %v, want a tagNamed def", tc.name, k.tag)
			continue
		}
		if !k.def.rtDeclared {
			t.Errorf("%s: rtDeclared is false, so typeDecl would emit a declaration for a type rt owns", tc.name)
		}
	}

	// The negative half. `Set<Int>` above is the CONTROL: its argument is
	// neutral, so it must come back from the SHARED table and must be neutral.
	setInt, ok := g.genStructInstance(setSpec, kindInt)
	if !ok {
		t.Fatal("Set<Int> did not intern; the control is vacuous")
	}
	if !setInt.packageNeutral() {
		t.Error("Set<Int> is not package-neutral, so the control is wrong and the negative below proves nothing")
	}
	chReq, ok := g.genStructInstance(channelSpecForTest(t), named(local))
	if !ok {
		t.Fatal("Channel<Request> did not intern")
	}
	if chReq.packageNeutral() {
		t.Error("Channel<Request> reports package-neutral. A shared table elsewhere would then adopt a def " +
			"whose Go text names NomiT_Request, a type one generated package declares. " +
			"kind.packageNeutral's genStructOf arm exists to answer false here")
	}
	sndReq, ok := g.genHostInstance(senderSpec, named(local))
	if !ok {
		t.Fatal("Sender<Request> did not intern")
	}
	if sndReq.packageNeutral() {
		t.Error("Sender<Request> reports package-neutral; kind.packageNeutral's genHostOf arm exists to answer false here")
	}
}

// TestGenStructPerGenInstanceIsPerGen is invariant (2): two gens produce two
// defs for one Nomi type, and one gen produces ONE.
//
// Both directions, because each failure is silent and they are opposite bugs.
// Sharing across gens names a type the other package never declared; failing to
// intern WITHIN a gen makes `Channel<Request>` unequal to itself, and kind
// equality is pointer equality on the def.
func TestGenStructPerGenInstanceIsPerGen(t *testing.T) {
	spec := channelSpecForTest(t)
	local := &typeDef{nomi: "Request", lowerable: true}
	g1, g2 := &gen{}, &gen{}

	a, ok1 := g1.genStructInstance(spec, named(local))
	b, ok2 := g1.genStructInstance(spec, named(local))
	c, ok3 := g2.genStructInstance(spec, named(local))
	if !ok1 || !ok2 || !ok3 {
		t.Fatal("Channel<Request> did not intern; every assertion below is vacuous")
	}
	if a.def != b.def {
		t.Error("two instantiations in ONE gen produced two defs, so `Channel<Request>` is not equal to itself")
	}
	if a.def == c.def {
		t.Error("two gens SHARE one Channel<Request> def. Its Go text names NomiT_Request, which only " +
			"one generated package declares, so the other would name a type it never wrote")
	}
	if len(g1.genStructOrder) != 1 {
		t.Errorf("g1.genStructOrder has %d entries, want 1; settleLowerable walks this slice, so an "+
			"absent entry is an instance whose unlowerable argument is never propagated", len(g1.genStructOrder))
	}
}

// TestGenHostPerGenInstanceKeepsTheOpaqueLeafShape guards a wrong answer
// rather than a refusal.
//
// A `stdGenHostSpecs` row is a `pub host type` whose contents this builder may
// not look inside: a `Sender<T>` holds a channel, a `Task<T>` holds a running
// task. Without `rtOpaque`, arms written for a zero-sized marker answer for a
// value with a payload, so `a == b` on one answers true.
//
// The per-gen door is a second constructor of the same def as the shared
// door, so the two can drift, and the failure is silent. Asserted over every
// row rather than over `Sender`, so a row added later gets the check for free.
func TestGenHostPerGenInstanceKeepsTheOpaqueLeafShape(t *testing.T) {
	local := &typeDef{nomi: "Probe", lowerable: true}
	for i := range stdGenHostSpecs {
		s := &stdGenHostSpecs[i]
		if len(s.params) != 1 {
			t.Errorf("%s.%s has %d type parameters; this guard constructs single-argument "+
				"instantiations and would skip it silently", s.origin, s.nomi, len(s.params))
			continue
		}
		g := &gen{}
		k, ok := g.genHostInstance(s, named(local))
		if !ok {
			t.Errorf("%s.%s<Probe>: the per-gen door produced no instance, so every assertion "+
				"below it is vacuous", s.origin, s.nomi)
			continue
		}
		d := k.def
		if d == nil {
			t.Errorf("%s.%s<Probe>: no def", s.origin, s.nomi)
			continue
		}
		// The paired control: the process-wide instance of the same row.
		// Comparing against it rather than against a hand-written list of four
		// booleans means a row whose shared shape changes cannot leave this
		// guard asserting the old one.
		ctl, ctlOK := sharedGenHostInstance(s, []kind{kindInt})
		if !ctlOK || ctl.def == nil {
			t.Errorf("%s.%s<Int>: the process-wide control did not intern, so there is nothing "+
				"to compare the per-gen shape against", s.origin, s.nomi)
			continue
		}
		c := ctl.def
		if d.rtOpaque != c.rtOpaque || d.isDistinct != c.isDistinct ||
			d.inner != c.inner || d.rtDeclared != c.rtDeclared {
			t.Errorf("%s.%s: the per-gen instance's def shape differs from the process-wide one "+
				"(per-gen rtOpaque=%v isDistinct=%v rtDeclared=%v; shared rtOpaque=%v "+
				"isDistinct=%v rtDeclared=%v). Losing rtOpaque makes `a == b` on two of these "+
				"emit `return true`, which is a wrong answer rather than a refusal",
				s.origin, s.nomi, d.rtOpaque, d.isDistinct, d.rtDeclared,
				c.rtOpaque, c.isDistinct, c.rtDeclared)
		}
		if !c.rtOpaque {
			t.Errorf("%s.%s: the process-wide def is NOT rtOpaque, so the comparison above "+
				"would pass for a per-gen def that is not either. Fix the row, not this test",
				s.origin, s.nomi)
		}
	}
}

// TestGenStructFieldGuardDeclinesAKindlessField is invariant (3), asserted
// over a constructed spec because the guard's population over the real spec
// table is empty.
//
// `mapKindIn` interns a map compKind for any key kind, and hashability is
// checked where a map is operated on, not where its type is formed. So over
// the real rows every field kind is buildable for every argument the doors
// admit:
//
//	Set.items       Map<T, Bool>   mapKindIn interns for any T
//	Range.start     T              the argument itself
//	Range.end       Maybe<T>       preludeInstanceOf is shared-then-per-gen
//	Range.inclusive Bool           a scalar
//	Channel.sender  Sender<T>      genHostInstance is shared-then-per-gen
//	Channel.receiver Receiver<T>   likewise
//
// So the guard is a belt: its population is empty rather than firing on real
// input. It is kept because it turns a wrong-code class into an ordinary
// decline: without it, a kindless field lands in a def whose `lowerable` is
// true, which names nothing and fails nowhere.
//
// This stops being true when a row in `stdGenStructSpecs` (stdgenstruct.go)
// gains a `kindOf` that can answer kindInvalid for an argument the doors
// admit. TestGenStructSpecFieldsBuildForEveryAdmittedArgument below notices.
func TestGenStructFieldGuardDeclinesAKindlessField(t *testing.T) {
	// The FIRING, on a spec whose field has no kind by construction.
	kindless := &stdGenStructSpec{
		origin: "test/probe", nomi: "Kindless", rtType: "rt.Kindless",
		params: []string{"T"},
		fields: []stdGenStructField{{
			nomi: "absent", decl: "T",
			kindOf: func(*gen, []kind) kind { return kindInvalid },
		}},
	}
	if d, ok := buildGenStructDef(&gen{}, kindless, []kind{kindInt}); ok {
		t.Errorf("buildGenStructDef admitted a def with a kindless field (%d fields, lowerable=%v). "+
			"Such a def emits Go naming nothing and Go does not reject it",
			len(d.fields), d.lowerable)
	}
	// The PAIRED CONTROL, so a guard that declined everything would fail here.
	if _, ok := buildGenStructDef(&gen{}, setSpec, []kind{kindInt}); !ok {
		t.Error("buildGenStructDef declined Set<Int>, whose every field is buildable. The guard is " +
			"rejecting everything, so the firing above proves nothing")
	}
}

// TestGenStructSpecFieldsBuildForEveryAdmittedArgument checks the claim in
// the paragraph above: it asserts that every real spec row's every field is
// buildable for a package-relative argument, which is the property that makes
// the field guard's population empty.
//
// It fails, rather than the claim silently rotting, if a spec row grows a
// `kindOf` that can decline. That is the positive form: the claim "the guard
// does not fire on real input" is checked by asking every row.
func TestGenStructSpecFieldsBuildForEveryAdmittedArgument(t *testing.T) {
	local := &typeDef{nomi: "Probe", lowerable: true}
	for i := range stdGenStructSpecs {
		s := &stdGenStructSpecs[i]
		if len(s.params) != 1 {
			// Every row is single-parameter; a multi-parameter row needs
			// its own argument tuple and this loop would be silently vacuous
			// for it, so it says so instead.
			t.Errorf("%s.%s has %d type parameters; this guard only constructs single-argument "+
				"instantiations and would skip it silently", s.origin, s.nomi, len(s.params))
			continue
		}
		g := &gen{}
		for _, arg := range []kind{kindInt, named(local)} {
			for _, f := range s.fields {
				if f.kindOf(g, []kind{arg}) == kindInvalid {
					t.Errorf("%s.%s at <%s>: field %q has no kind. The field guard's population is "+
						"not empty, so the claim above TestGenStructFieldGuardDeclinesAKindlessField "+
						"is wrong and this row's refusal needs its own name at its own position",
						s.origin, s.nomi, arg.nomi(), f.nomi)
				}
			}
		}
	}
}

// TestGenStructSignatureBoundaryStaysProcessWide pins the ONE door that must NOT
// grow the per-gen fallback.
//
// A stdFunc's kinds are built once and compared BY POINTER against a call site's
// argument kinds in a different gen (stdprelude.go's header). A per-gen instance
// in a std SIGNATURE would be a kind no call site in any gen could match, so
// `genStructSigKind` calls sharedGenStructInstance directly and a std signature
// mentioning `Channel<Request>` stays outside the subset.
//
// Asserted through the nil-gen path, which is the same statement made where a
// signature has no gen to intern into: `g == nil` must decline rather than
// panic, and it must decline only for the non-neutral case.
func TestGenStructSignatureBoundaryStaysProcessWide(t *testing.T) {
	var g *gen // the stdlib signature boundary: no module to intern into
	if _, ok := g.genStructInstance(setSpec, kindInt); !ok {
		t.Error("a nil gen declined Set<Int>, whose argument is package-neutral. The shared table " +
			"must still answer at the signature boundary or every std signature mentioning Set refuses")
	}
	local := &typeDef{nomi: "Request", lowerable: true}
	if _, ok := g.genStructInstance(channelSpecForTest(t), named(local)); ok {
		t.Error("a nil gen produced Channel<Request>. There is no module to intern it into, so the " +
			"def could not be matched by pointer from any gen and the signature would silently " +
			"admit a kind nothing can call")
	}
	if _, ok := g.genHostInstance(senderSpec, named(local)); ok {
		t.Error("a nil gen produced Sender<Request>; same reason")
	}
}

// channelSpecForTest is the `Channel` row, resolved by (origin, name) so a
// reorder of the spec table cannot repoint these tests silently.
func channelSpecForTest(t *testing.T) *stdGenStructSpec {
	t.Helper()
	for i := range stdGenStructSpecs {
		s := &stdGenStructSpecs[i]
		if s.origin == "std/channels" && s.nomi == "Channel" {
			return s
		}
	}
	t.Fatal("no std/channels.Channel row in stdGenStructSpecs")
	return nil
}
