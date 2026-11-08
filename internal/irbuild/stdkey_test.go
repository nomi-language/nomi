package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// TestStdOverloadSetRefusesAnIndistinguishablePair tests the MECHANISM, and it
// exists because a mutation proved the mechanism was untested.
//
// Deleting `markAmbiguous` from addOverload passed every test in this package,
// including the whole 213-file sweep. The reason is not that the refusal is
// pointless: it is that every indistinguishable group std carries TODAY is
// refused by its own signature as well (`Date.add`'s four impls all take an
// unsupported operand), so the ambiguity mark is currently belt and braces and
// nothing observable depends on it. That is precisely the state in which a fence
// gets removed by a future refactor as dead code.
//
// So it is exercised directly. Two declarations a call site cannot tell apart
// must BOTH stop lowering; two it can must both survive and each route to
// itself. Built by hand rather than found in std, because the interesting input
// — an indistinguishable pair that would otherwise lower — is one std does not
// currently contain, and waiting for std to grow one is how the fence goes
// untested until it matters.
func TestStdOverloadSetRefusesAnIndistinguishablePair(t *testing.T) {
	decl := func(tag string, params ...kind) *stdFunc {
		return &stdFunc{
			key: "m.T." + tag, module: "m", recv: "T", name: "f",
			params: params, result: kindInt, body: true,
		}
	}
	// Indistinguishable: same parameter kinds, both lowering on arrival.
	a, b := decl("a", kindInt, kindInt), decl("b", kindInt, kindInt)
	fs := addOverload(addOverload(nil, a, "ambiguous stdlib method"), b, "ambiguous stdlib method")
	if len(fs) != 2 {
		t.Fatalf("the set holds %d member(s), want 2; an overload set must not drop a declaration", len(fs))
	}
	for _, f := range []*stdFunc{a, b} {
		if f.lowerable() {
			t.Errorf("%s still lowers (body=%v) after colliding with an identical signature; "+
				"a call site would be routed to whichever won", f.key, f.body)
		}
		if f.why != "ambiguous stdlib method" {
			t.Errorf("%s refused as %q, want the ambiguity reason", f.key, f.why)
		}
	}
	// Distinguishable: a different second parameter, so both survive and
	// selection separates them.
	c, d := decl("c", kindInt, kindInt), decl("d", kindInt, kindString)
	set := addOverload(addOverload(nil, c, "ambiguous stdlib method"), d, "ambiguous stdlib method")
	if !c.lowerable() || !d.lowerable() {
		t.Fatalf("a distinguishable pair was refused: c.why=%q d.why=%q", c.why, d.why)
	}
	if got := stdPick(set, []kind{kindInt, kindInt}); got != c {
		t.Errorf("(Int, Int) selected %v, want %s", got, c.key)
	}
	if got := stdPick(set, []kind{kindInt, kindString}); got != d {
		t.Errorf("(Int, String) selected %v, want %s", got, d.key)
	}
	// And a signature no member takes selects nothing rather than the nearest.
	if got := stdPick(set, []kind{kindString, kindString}); got != nil {
		t.Errorf("(String, String) selected %s; no member takes it", got.key)
	}
}

// TestStdlibKeysCarryTheInstantiation is the injectivity check over the
// population that motivated instantiation-qualified keys: the eleven
// `impl Add<X, NaiveDateTime>` rungs must produce eleven distinct keys, and
// the two that differ only in their type ARGUMENT are the pair a
// non-injective key would merge. The keys also name the arity wrappers and
// slot accessors in the IR (stdArityName, stdSlotName).
func TestStdlibKeysCarryTheInstantiation(t *testing.T) {
	// Injectivity over the population the mangling exists for: eleven rungs,
	// eleven names. Read from source rather than from the index, because the
	// index holds one entry per key and the point is that there are eleven.
	names := map[string]string{}
	rungs := 0
	for key, fs := range stdKeyClaimantList(std.Load()) {
		if !strings.HasPrefix(key, "calendar.NaiveDateTime.Add<") {
			continue
		}
		for _, f := range fs {
			rungs++
			got := f.key
			if prev, dup := names[got]; dup {
				t.Errorf("%s and %s both mangle to %s; the squash is not injective over the "+
					"Add ladder, and two rungs sharing an emitted name is a wrong answer", prev, key, got)
				continue
			}
			names[got] = key
		}
	}
	if rungs != 11 {
		t.Errorf("the Add<X, NaiveDateTime> ladder has %d rung(s), want 11; either std changed or "+
			"the key stopped separating them", rungs)
	}
	if len(names) != rungs {
		t.Errorf("%d rung(s) produced %d distinct name(s)", rungs, len(names))
	}
}
