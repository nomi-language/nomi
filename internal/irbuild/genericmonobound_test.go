package irbuild

import (
	"testing"
)

// A BOUNDED generic function, MONOMORPHIZED because the dictionary declined it.
//
// # The two mechanisms and the ORDER between them, which is the whole design
//
// `signature()` (native.go) asks `dictSignature` FIRST and asks
// `monoTemplateFor` only when the dictionary declined to claim the declaration.
// So the rule is one sentence: THE DICTIONARY LOWERS WHAT IT CAN AND
// MONOMORPHIZATION TAKES ITS RESIDUE. Nothing in `monoTemplateFor` protects the
// dictionary's population — the ordering does — which is why the order is
// asserted here rather than commented.
//
// `dictSeamFor`'s residue is exactly three declines and every one dissolves
// inside an instance rather than being handled:
//
//   - two or more BOUNDED type parameters. The seam declines because a
//     turbofish's positional order is by first appearance, and getting it
//     wrong would be silent. An instance has no dictionary to order.
//   - a bound naming an interface this builder cannot erase — a GENERIC
//     interface, `Iter<T>` and `Add<R, Out>`. In an instance `T` is `Score` and
//     `Add.add(a, b)` is an ordinary concrete dispatch, so there is no erased
//     table to demand.
//   - a bound that resolves to nothing this builder can name.
//
// # Why this is not a semantics change
//
// The dictionary and a monomorphized instance are required to agree on
// OBSERVABLE BEHAVIOUR, which the golden records check, rather than on
// mechanism.
//
// # COUNTEREXAMPLE: why monomorphization cannot be the ONLY mechanism
//
// A fact about the language rather than about this builder:
//
//	fn deep_grow<T>(x: T, depth: Int): Int {
//	  if depth <= 0 { 0 } else { deep_grow(Box{inner: x}, depth - 1) + 1 }
//	}
//
// which `nomi check`s clean and has an answer, and whose instantiation set is
// infinite: the recursive call binds `T := Box<T>`, giving `Box<Int>`,
// `Box<Box<Int>>`, … without end. A monomorphizing backend cannot terminate on
// it. That is why `monoInstCap` exists and why the dictionary is the
// PRIMARY path rather than a fallback — and it is why the erasure path in
// generic.go is kept rather than subsumed.

// TestMonoBound_TheDictionaryStillWinsWhenItCan asserts the order, which an
// outcome-only test cannot.
//
// Two mechanisms cover overlapping ground, so "it lowers" does not say which
// lowered it, and a fallback that quietly took over the dictionary's
// population would still produce the right answers while lowering N copies of
// a function that should be single-bodied, and while losing the leading
// `*rt.TypeID` that `T.method(x)` dispatch rides on.
//
// The discriminator is the SIGNATURE: `sig.dict != nil` for a dictionary
// lowering, `sig.mono != nil` for a template, and the two are mutually
// exclusive by `signature()`'s structure. Asked of the signature directly rather
// than of the lowered body, because a one-instance monomorphization and a
// dictionary body differ only in a parameter nobody would notice was missing.
func TestMonoBound_TheDictionaryStillWinsWhenItCan(t *testing.T) {
	// `Showable` is a plain user interface with one bound on one type
	// parameter: exactly `dictSeamFor`'s admitted shape.
	src := "interface Showable {\n  fn show(v: self): String\n}\n\n" +
		"struct User {\n  name: String\n}\n\n" +
		"impl Showable for User {\n  fn show(v: User): String {\n    v.name\n  }\n}\n\n" +
		"fn announce<T>(x: T): String where T: Showable {\n  Showable.show(x)\n}\n\n" +
		"fn pair<T, U>(a: T, b: U): String where T: Showable, U: Showable {\n" +
		"  Showable.show(a) + Showable.show(b)\n}\n\n" +
		"fn main(): Unit {\n  if True { }\n}\n"
	p, err := AnalyzeSource("monobound_order", src)
	if err != nil {
		t.Fatalf("analyzing: %v", err)
	}
	g := newGen(&p.Modules[0], "nomimod0", nil, nil, -1, nil)
	g.declareTypes()
	g.declareFuncs()

	announce := g.funcs["announce"]
	if announce == nil {
		t.Fatal("no signature for `announce`; the rest of this test is vacuous")
	}
	if announce.dict == nil {
		t.Error("`announce<T> where T: Showable` did not take the DICTIONARY. One bound on one " +
			"type parameter resolving to a lowerable interface is dictSeamFor's admitted shape, " +
			"so monomorphization has taken over a population that should stay single-bodied")
	}
	if announce.mono != nil {
		t.Error("`announce` is BOTH a dictionary signature and a monomorphization template. " +
			"signature() returns at dictSignature's claim, so these are mutually exclusive by " +
			"construction; both being set means the routing changed")
	}

	pair := g.funcs["pair"]
	if pair == nil {
		t.Fatal("no signature for `pair`")
	}
	if pair.dict != nil {
		t.Error("`pair<T, U> where T: Showable, U: Showable` took the dictionary. dictSeamFor " +
			"declines two bounded type parameters, so the seam's own gate has changed and the " +
			"turbofish order it declines to re-encode is being re-encoded somewhere")
	}
	if pair.mono == nil {
		t.Error("`pair` is not a monomorphization template. The dictionary declined it, so " +
			"nothing else can lower it and the declaration is refused `generic function`")
	}
}
