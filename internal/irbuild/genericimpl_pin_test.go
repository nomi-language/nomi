package irbuild

import "testing"

// The pin for per-instance impl registration.
//
// `registerImpl` refuses both impls when `byRecv[d.recv]` is already taken, on
// the rule that two impls of one interface for one type is a coherence error
// and picking one would let ordering decide. Impls are registered per generic
// instance, so that map holds one entry per instantiation of the same
// template.
//
// So the mechanism rests on `Box<Int>` and `Box<String>` not being the same
// `kind`. If they were, every template with more than one instantiation would
// refuse under `duplicate impl block`.
//
// TestGenericInstanceIsInternedPerInstantiation compares names; this test uses
// the instance kinds as map keys, which is the operation `registerImpl`
// performs. `kind` is `{tag, *typeDef}` compared by Go equality, so the two
// claims are different: interning by rendered name, as `monoInstKey` and
// `genericInstKey` do for their intern keys, could make two kinds compare
// equal while their names differ.
//
// The fixture contains exactly two instantiations of one template and nothing
// else, so if they coincide the assertion cannot pass by accident.
func TestGenericInstanceKindsAreDistinctMapKeys(t *testing.T) {
	src := "pub struct Box<T> {\n  item: T\n}\n\n" +
		"fn a(b: Box<Int>): Int {\n  b.item\n}\n\n" +
		"fn c(d: Box<String>): String {\n  d.item\n}\n"
	g := lowerableGen(t, src)
	if n := len(g.genericInstOrder); n != 2 {
		t.Fatalf("built %d instances, want exactly 2: the claim is about TWO instantiations of one "+
			"template being two receiver keys, and any other count makes the comparison vacuous", n)
	}
	// The exact operation registerImpl performs: an instance kind used as a
	// `map[kind]*implDef` key.
	byRecv := map[kind]string{}
	for _, d := range g.genericInstOrder {
		k := kind{tag: tagNamed, def: d}
		if prior, dup := byRecv[k]; dup {
			t.Fatalf("Box<Int> and Box<String> are ONE map[kind] key (%s and %s collide). "+
				"Per-instance impl registration would report `duplicate impl block` for every "+
				"template with more than one instantiation", prior, d.nomi)
		}
		byRecv[k] = d.nomi
	}
	if len(byRecv) != 2 {
		t.Fatalf("two instance kinds gave %d map keys, want 2", len(byRecv))
	}
}

// TestGenericInstanceNomiNamesCollideOnPurpose pins that two instances of one
// template share a bare `nomi` name.
//
// An instance's `nomi` is the bare declaration name ("Box", never
// "Box<Inner>") because that is what `Debug.inspect` renders. So two instances
// of one template produce the same `implGoName` base
// (`NomiI_<Iface>_<recv.nomi()>_<method>`), and `ifaceGoName`'s `_2` suffix is
// what keeps the two names distinct.
//
// This test pins the collision as intended, so that:
//
//   - a change making `nomi` carry the arguments fails here, rather than
//     silently re-rendering every `Debug.inspect` of a generic instance; and
//   - impl names are disambiguated by minting and never by derivation.
//     Anything that reconstructs `NomiI_<Iface>_<nomi>_<method>` instead of
//     reading the minted name picks whichever instance came first.
func TestGenericInstanceNomiNamesCollideOnPurpose(t *testing.T) {
	src := "pub struct Box<T> {\n  item: T\n}\n\n" +
		"fn a(b: Box<Int>): Int {\n  b.item\n}\n\n" +
		"fn c(d: Box<String>): String {\n  d.item\n}\n"
	g := lowerableGen(t, src)
	if len(g.genericInstOrder) != 2 {
		t.Fatalf("built %d instances, want 2", len(g.genericInstOrder))
	}
	a, b := g.genericInstOrder[0], g.genericInstOrder[1]
	if a.nomi != b.nomi {
		t.Fatalf("two instances of one template render as %q and %q. generictype.go's header "+
			"records `Box{item: Inner{value: 7}}` as Debug.inspect's answer "+
			"-- no type arguments -- so both must be the BARE name %q. If this "+
			"is a deliberate change, qualifiedNomiName's dispatch identity has to move "+
			"in the SAME commit",
			a.nomi, b.nomi, "Box")
	}
	if a == b {
		t.Fatal("two instantiations share one def")
	}
}

// TestExpansiveImplMemberIsWithheldFromEachInstance pins expansiveImplItem.
//
// `gather` names `Box<List<T>>`. Built as a member of every instance, it would
// mint `Box<List<Int>>` for `Box<Int>`, whose block would build it again for
// `Box<List<List<Int>>>`, without end: before the check this source never
// finished lowering. Withheld, the one instance the source names is the only
// one built, and the block records why `gather` is absent.
func TestExpansiveImplMemberIsWithheldFromEachInstance(t *testing.T) {
	src := "enum Box<T> {\n  Full T\n  Empty\n}\n\n" +
		"impl Box<T> {\n" +
		"  fn gather<T>(_boxes: List<Box<T>>): Box<List<T>> {\n    Box.Empty\n  }\n\n" +
		"  fn full?<T>(b: Box<T>): Bool {\n    case b {\n      .Full(_) -> True\n      .Empty -> False\n    }\n  }\n" +
		"}\n\n" +
		"fn f(b: Box<Int>): Bool {\n  Box.full?(b)\n}\n"
	g := lowerableGen(t, src)
	if n := len(g.genericInstOrder); n != 1 {
		t.Fatalf("built %d instances, want exactly 1 (Box<Int>): gather's Box<List<T>> must not mint a "+
			"larger instance while its block is registered", n)
	}
	var withheld, kept bool
	for _, d := range g.implsByIface[""] {
		if gap, ok := d.gaps["gather"]; ok && gap.why == "expansive impl function" {
			withheld = true
		}
		if d.items["full?"] != nil {
			kept = true
		}
	}
	if !withheld || !kept {
		t.Fatalf("want gather withheld as an expansive impl function and full? kept on Box<Int>'s block; "+
			"withheld=%v kept=%v", withheld, kept)
	}
}
