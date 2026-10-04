package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// `Range<T>` as `rt.Range[T]` — the `a..b` / `a..=b` literal, the `impl Range<T>`
// method surface, the `Iter` source, and the rendering.
//
// # Why this is an owner-keyed arm and not the stdlib path
//
// sets.go's header states the rule and this file is its second case, for exactly
// the same reason: stdgenstruct.go supplies the KIND, and what it cannot supply
// is a LOWERING, because every function of `impl Range<T>` mentions T inside
// `Range<T>` rather than bare — the shape generic.go refuses ("the box would be
// at the wrong DEPTH"). So the index answers with a refusal and this arm answers
// with an implementation, reading the element type off the LOWERED RECEIVER.
//
// # THE DICTIONARY IS RESOLVED HERE, STATICALLY, AND THAT IS THE WHOLE TRICK
//
// `Range<T>` carries `where T: Comparable` and `impl Iter for Range<T>` carries
// `where T: Discrete`. Neither is a dictionary at run time. The builder knows the
// element kind at every site, so it looks the interface method up in the STD
// INDEX at that concrete kind and passes the resulting Go func as an argument —
// which is `sortComparator`'s arrangement for `Iter.sort`, one interface wider.
//
// The implementations are std's own. `Discrete.next` at Codepoint is
//
//	fn next(cp: Codepoint): Maybe<Codepoint> {
//	  n = Codepoint.to_int(cp)
//	  if n == 1_114_111 { None }
//	  else if n == 55_295 { Codepoint.from_int(57_344) }
//	  else { Codepoint.from_int(n + 1) }
//	}
//
// — ordinary Nomi in std/codepoints.nomi, with the UTF-16 surrogate-block skip in
// it, and the Nomi-bodied half of a std module lowers like any other module.
// So a program runs std's OWN successor predicate, not a Go copy of it, and
// rt/range.go contains no arithmetic over any element type. The one thing this
// file must not do is what rt/codepoint.go's header warns about by name: write a
// second validator.
//
// Per concrete element type the corpus reaches:
//
//	elem       Comparable.compare   Discrete.next / steps_between   Steppable.step_by
//	Int        std/int (Nomi)       std/int (Nomi)                  std/int (Nomi)
//	Codepoint  derive (synth)       std/codepoints (Nomi)           std/codepoints (Nomi)
//	Decimal    host fn              --                              std/decimal (Nomi)
//	String     std/strings          --                              --
//	Float      std/float            --                              --
//	user type  the program's impl   --                              the program's impl
//
// A user element's compare and step_by are the program's own impl bodies, in
// the file that declares the element or another (elemComparator, elemStepper);
// the VM's range hosts take them as function values as they take std's.
//
// The two `--` rows in the Discrete column are why `Range<String>` and
// `Range<Float>` are containment values and not sequences, and the front end
// enforces that before this file sees it: `impl Iter for Range<T> where T:
// Discrete` does not apply, so `Iter.to_list("a".."m")` is a type error rather
// than a refusal here. The refusals below exist for the case the front end DOES
// admit and this builder cannot serve — a user type implementing Discrete, whose
// impl is a Nomi body in the user's own module rather than in the std index.
//
// The Codepoint row is why stdenum.go has stdEnumSynthAnchors. `derive
// Comparable for Codepoint` synthesises `compare(a: Codepoint, b: Codepoint):
// Ordering` into a file that never imports `Ordering`, so resolving the anchor
// through that file's imports would refuse this whole element type under a key
// claiming a missing representation.
//
// # What is NOT here, and each absence is std's rather than the builder's
//
//   - No equality and no hashing. std declares `impl Display` and `impl Debug`
//     for `Range<T>` and declares NEITHER `Equatable` nor `Hashable`, so
//     `1..5 == 1..5` and a Range map key refuse under the ordinary named-type
//     keys. Adding either would be inventing a rule std does not have.
//   - No `StepByRange` kind. std declares it `opaque struct` with no `pub`, so
//     no Nomi program can name one; `step_by` answers `Iter<T>` and the `Seq` IS
//     the observable value. See rt.StepByRangeSeq.
//   - No UNBOUNDED literal. The parser rejects the open-ended forms, so a
//     RangeLit always has both operands and the unbounded shape is reachable
//     only through `Range.from` / `Range.naturals`. rangeLit still handles a nil
//     operand, because ast.RangeLit documents it and checkRangeLit tolerates it,
//     but no source in this language produces one.
//
// # The refusals this arm can give
//
// unsupported.go's node table has no `range literal` key, because RangeLit has
// an arm here. The arm refuses under three named conditions instead:
// `range over an unorderable element`, `range over an element without a
// successor` and `Range.step_by over an unsteppable element`, each reachable
// from a user type that implements the interface in its own module.

// rangeSpec is the `Range` row, resolved by (origin, name) so a reorder of the
// spec table cannot repoint it silently — setSpec's form and its reason.
var rangeSpec = func() *stdGenStructSpec {
	for i := range stdGenStructSpecs {
		s := &stdGenStructSpecs[i]
		if s.origin == "std/ranges" && s.nomi == "Range" {
			return s
		}
	}
	return nil
}()

// rangeKindOf is the Range kind for element kind elem, or kindInvalid.
//
// One constructor, so a literal, an annotation, `Range.from` and every method
// result reach the SAME interned def for one element type — stdgenstruct.go's
// identity rule applied rather than restated.
func (g *gen) rangeKindOf(elem kind) kind {
	if rangeSpec == nil {
		// Unreachable while the spec table has its `Range` row, and honoured
		// rather than asserted: a missing row must refuse everything, not panic
		// the compiler. setKindOf's clause.
		return kindInvalid
	}
	k, ok := g.genStructInstance(rangeSpec, elem)
	if !ok {
		return kindInvalid
	}
	return k
}

// rangeElem reads a Range kind's element kind, and reports whether k is a Range.
//
// By SPEC POINTER through genStructOf, never by rendered name: a user type
// spelled `Range` has its own def and answers false, which is the half a name
// check gets wrong.
func rangeElem(k kind) (kind, bool) {
	spec, args, ok := genStructOf(k)
	if !ok || spec != rangeSpec || len(args) != 1 {
		return kindInvalid, false
	}
	return args[0], true
}

// --- the element's interface methods, resolved statically --------------------

// rangeOrders reports whether elem has `Comparable.compare`, refusing by name
// when it has not.
//
// sortOrders verbatim, and delegating to it rather than repeating the
// resolution so a Range's bound test orders elements EXACTLY the way `<` and
// `Iter.sort` do. Three resolutions of one ordering is how a range comes to
// contain an element a sort would place outside it.
func (g *gen) rangeOrders(elem kind, at ast.Node) bool {
	if !g.sortOrders(elem, at) {
		g.reject("range over an unorderable element", elem.nomi(), at)
		return false
	}
	return true
}

// rangeDiscrete reports whether elem has `Discrete.next` and
// `Discrete.steps_between`.
//
// Both or neither: a Range that can WALK must also be able to answer
// `known_count`, because `Iter.count` consults the count protocol first and a
// source that walked but could not count would be an O(n) answer behind an O(1)
// name. std declares `steps_between` as an `open` member with a `None` default,
// so an element type may legitimately decline to COUNT — that is the RESULT being
// None, not the method being absent, and the two are distinguished here: an
// absent method refuses, a `None` answer is passed through.
func (g *gen) rangeDiscrete(elem kind, at ast.Node) bool {
	maybeElem, shared := g.sharedMaybe(elem)
	maybeInt, sharedInt := g.sharedMaybe(kindInt)
	if !shared || !sharedInt ||
		g.stdIfaceFnAt("Discrete.next", elem, []kind{elem}, maybeElem) == nil ||
		g.stdIfaceFnAt("Discrete.steps_between", elem, []kind{elem, elem}, maybeInt) == nil {
		g.reject("range over an element without a successor", elem.nomi(), at)
		return false
	}
	return true
}

// sharedMaybe is `Maybe<of>` in the PROCESS-WIDE table, which is the instance
// every std signature's own `Maybe` result is interned in.
//
// The shared table rather than preludeInstanceOf, and that is the correct
// direction here rather than a shortcut: these kinds are compared against a std
// function's declared `result`, which stdTypeKind resolved through
// preludeSigKind — the shared table. A per-gen instance would compare unequal to
// every std signature and every lookup below would report a mismatch that is not
// one.
func (g *gen) sharedMaybe(of kind) (kind, bool) {
	return sharedPreludeInstance(preludeSpecFor("std/maybe", "Maybe"), []kind{of})
}

// stdIfaceFnAt resolves one std interface method at receiver kind recv, checking
// the signature the caller assumes.
//
// Deliberately NOT stdlibImplOf, and the difference is one line: stdlibImplOf
// returns nil for a receiver whose `def` is nil, i.e. every SCALAR — and `Int` is
// the element type the corpus reaches most. stdCompareAt makes the identical
// choice for the identical reason and this is its generalisation; the two could
// be one function and are not only because stdCompareAt's caller already holds
// the Ordering def it wants to compare against.
func (g *gen) stdIfaceFnAt(key string, recv kind, params []kind, result kind) *stdFunc {
	if g.std == nil {
		return nil
	}
	f := stdPick(g.std.byIface[key][recv], params)
	if f == nil || f.why != "" || len(f.params) != len(params) {
		return nil
	}
	for i, p := range params {
		if f.params[i] != p {
			return nil
		}
	}
	if f.result != result {
		return nil
	}
	return f
}

// --- the literal -------------------------------------------------------------

// --- the call arm ------------------------------------------------------------

// --- Iter, known_count, rendering -------------------------------------------

// rangeWalks reports whether elem has the ordering plus the successor pair —
// what any WALK over a Range needs, and what `contains?` deliberately does not.
func (g *gen) rangeWalks(elem kind, at ast.Node) bool {
	return g.rangeOrders(elem, at) && g.rangeDiscrete(elem, at)
}
