package rt

// Nomi's `Range<T>` — and like set.go, the point is how little of it there is.
//
// # A Range is three fields and no algorithm
//
// std/ranges.nomi:24 declares
//
//	pub opaque struct Range<T> where T: Comparable {
//	  start: T
//	  end: Maybe<T>
//	  inclusive: Bool
//	}
//
// and a range LITERAL desugars to exactly that:
// `ranges.Range{start, end: Some(e) | None, inclusive}` and nothing else. Every
// operation over it — `contains?`, `bounded?`, `known_count`, `each_while` — is
// an ordinary Nomi body over those three fields plus ONE interface method on the
// element type. So there is no interval tree here, no normalization, and no
// representation decision beyond field-for-field transcription.
//
// The Go type has to exist here for set.go's reason, unchanged: a `*typeDef`
// reached from two generated packages must render to the same Go type text in
// every one of them (internal/irbuild/stdprelude.go's packageNeutral), so
// `Range<Int>` cannot be a per-package generated struct.
//
// # THE DICTIONARY IS AN ARGUMENT, WHICH IS WHY THE `where` CLAUSE IS NOT A WALL
//
// `Range<T>`'s declaration carries `where T: Comparable`, and
// `impl Iter for Range<T>` carries `where T: Discrete`. Neither is resolved
// here. Every function below takes the element's interface methods as PLAIN GO
// FUNCS, assembled by the caller at each call site from the element's static
// kind — exactly as map.go and set.go take `hash, eq`, and for the same reason:
// the caller knows the element type, so it can build the function rather than
// look it up at run time.
//
// That is what makes a bound on a std TYPE the same non-problem it already was
// on a std FUNCTION. internal/irbuild/iterext.go's iterMaxArity header records the
// measurement for the function case — "a `where T: Comparable` on a std function
// is discharged at the call site by the CONCRETE type argument" — and nothing
// about a type changes it. The corpus instantiates Int, Codepoint, Decimal,
// String and Float, all concrete.
//
// The methods each function needs, and no function takes one it does not use:
//
//	cmp   Comparable.compare        contains?, known_count, each_while, step_by
//	next  Discrete.next             each_while
//	steps Discrete.steps_between    known_count
//	step  Steppable.step_by         step_by
//
// `bounded?` takes NONE, which is the observable half of "a Range VALUE needs no
// dictionary at all": it is `case r.end` and nothing more.
//
// # Every function here is std's own body with the recursion flattened
//
// The rule set.go states applies unchanged, and for a Range the thing that must
// be preserved exactly is the BOUNDARY behaviour, since all of it is observable:
//
//   - `each_while` stops when `start > end` (inclusive) or `start >= end`
//     (exclusive), so `5..1` and `5..=1` are both EMPTY rather than descending.
//   - `Discrete.next` answering None means the element type SATURATED, and std
//     yields `r.start` one last time rather than dropping it. So a range ending
//     at the top of the domain emits its last element.
//   - `step_by` with a step that does not MOVE stops WITHOUT emitting — so
//     `Range.step_by(1..=9, 0)` is `[]` and not an infinite run of `1`. This is
//     the one place where the stop is not the bound.
//   - `known_count` is `steps_between` plus one for an inclusive end, and it is
//     `None` for an unbounded range — which is what `Iter.count` consults, so an
//     unbounded range must not answer a number here.
//
// std recurses per element in TAIL position and says so ("Range walks itself by
// recursing per element, so a long drive is only stack-safe because the
// recursive `each_while` call is in tail position"). These are loops. That is
// strictly stronger than the source and observably identical: the corpus drives
// `1..=200_000` through one.
//
// # What is NOT here
//
// No `RangeEqual` and no `RangeHash`. std declares `impl Display` and
// `impl Debug` for `Range<T>` and declares NEITHER `Equatable` nor `Hashable`,
// so `1..5 == 1..5` and a Range map key are refusals rather than gaps, and
// adding either would be this file inventing a rule std does not have.

// Range is `opaque struct Range<T> where T: Comparable { start: T; end:
// Maybe<T>; inclusive: Bool }`, field for field.
//
// `End` being `Maybe[T]` rather than a `T` plus a bool is the declaration's own
// shape and it carries a fact the pair would not: std documents that when `end`
// is None, "`inclusive` carries no meaning and is canonically `False`". The
// builder produces that canonical form (a literal with no end is never inclusive —
// ast.RangeLit's own rule), so nothing below has to re-establish it, and
// `Inclusive` is simply never read on the unbounded path.
type Range[T any] struct {
	Start     T
	End       Maybe[T]
	Inclusive bool
}

// --- queries, which need no Discrete ----------------------------------------

// RangeContains is `Range.contains?` — `n >= start and n <= end` for an
// inclusive range, `n >= start and n < end` for an exclusive one, and `n >=
// start` alone when there is no upper bound.
//
// Written against OrderingRank rather than three separate comparator calls per
// branch so the comparator runs at most twice, which is what std's `and` does by
// short-circuiting.
func RangeContains[T any](fr *Frame, r Range[T], n T, cmp func(fr *Frame, a, b T) Ordering) bool {
	if OrderingRank(cmp(fr, n, r.Start)) < 0 {
		return false
	}
	if r.End.Tag != TagSome {
		return true
	}
	rank := OrderingRank(cmp(fr, n, r.End.Some))
	if r.Inclusive {
		return rank <= 0
	}
	return rank < 0
}

// RangeBounded is `Range.bounded?` — `case r.end { Some(_) -> True; None ->
// False }`.
//
// The one function here that takes no element method at all. See the header.
func RangeBounded[T any](r Range[T]) bool { return r.End.Tag == TagSome }

// RangeContainsFloat uses Float's interval comparisons. NaN is unordered under
// these operators even though Comparable.compare provides a total sort order.
func RangeContainsFloat(r Range[float64], n float64) bool {
	if !(n >= r.Start) {
		return false
	}
	if r.End.Tag != TagSome {
		return true
	}
	if r.Inclusive {
		return n <= r.End.Some
	}
	return n < r.End.Some
}

// --- iteration --------------------------------------------------------------

// RangeKnownCount is `impl Iter for Range<T>`'s `known_count` override: the
// O(1) element count when the element type can report one.
//
// Three answers and each is std's, in order:
//
//   - `None` for an UNBOUNDED range. Not "unknown for now" — an unbounded range
//     has no count, and `Iter.count` consults this before folding, so answering
//     a number here would make `Iter.count(Range.from(1))` return it instead of
//     hanging. Both are wrong; only one is silent.
//   - `Some(0)` when `start > end`, checked BEFORE steps_between, because
//     `Discrete.steps_between` is free to answer `Some(0)` for a reversed pair
//     and std does not rely on it.
//   - `steps_between(start, end)` plus one for an inclusive end. So `1..5` is 4
//     and `1..=5` is 5, and `steps_between` may answer `None` — which is a
//     legitimate "I cannot say without walking" from the element type and is
//     passed through rather than converted to a walk.
func RangeKnownCount[T any](fr *Frame, r Range[T], cmp func(fr *Frame, a, b T) Ordering, steps func(fr *Frame, a, b T) Maybe[int64]) Maybe[int64] {
	if r.End.Tag != TagSome {
		return None[int64]()
	}
	if OrderingRank(cmp(fr, r.Start, r.End.Some)) > 0 {
		return Some[int64](0)
	}
	n := steps(fr, r.Start, r.End.Some)
	if n.Tag != TagSome {
		return None[int64]()
	}
	if r.Inclusive {
		return Some(n.Some + 1)
	}
	return n
}

// RangeEachWhile is `impl Iter for Range<T> where T: Discrete`'s `each_while`.
//
// std's body recursing in tail position, flattened to a loop. The two exits are
// the ones the header names: the BOUND (inclusive `start > end`, exclusive
// `start >= end`) answers True having yielded nothing further, and a saturated
// element type — `Discrete.next` answering None — yields `start` ONE MORE TIME
// and answers whatever the consumer said. Dropping that last element is the
// tempting off-by-one and it is observable at the top of any bounded domain.
//
// Note the bound is checked BEFORE `next` is called, so a range whose start is
// already past its end never asks the element type for a successor.
func RangeEachWhile[T any](fr *Frame, r Range[T], cmp func(fr *Frame, a, b T) Ordering, next func(fr *Frame, v T) Maybe[T], yield func(fr *Frame, item T) bool) bool {
	cur := r.Start
	for {
		if r.End.Tag == TagSome {
			rank := OrderingRank(cmp(fr, cur, r.End.Some))
			if r.Inclusive {
				if rank > 0 {
					return true
				}
			} else if rank >= 0 {
				return true
			}
		}
		nxt := next(fr, cur)
		if nxt.Tag != TagSome {
			// The element type saturated: `cur` is the last element.
			return yield(fr, cur)
		}
		if !yield(fr, cur) {
			return false
		}
		cur = nxt.Some
	}
}

// RangeSeq views a Range as a push sequence of its elements.
//
// One closure, allocated once, and the walk lives inside `Run` so the sequence
// is REPLAYABLE, because std/iter.nomi promises that
// binding a pipeline and consuming it twice yields the same sequence, and a
// cursor hoisted into this closure would make the second run empty.
func RangeSeq[T any](r Range[T], cmp func(fr *Frame, a, b T) Ordering, next func(fr *Frame, v T) Maybe[T]) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		return RangeEachWhile(fr, r, cmp, next, yield)
	}}
}

// StepByRangeSeq is `Range.step_by(r, by)`: `impl Iter for StepByRange<T, S>`'s
// `each_while` behind the one-line `Seq` wrapper std's own `step_by` returns.
//
// `StepByRange<T, S>` itself has no Go type here, and that is not an omission.
// std declares it `opaque struct` — file-private, with no `pub` — so no Nomi
// program can name it, hold one, or reach it except as the `Iter<T>` that
// `step_by` answers. A Go type for it would be a representation nothing can
// observe; the `Seq` IS the observable value.
//
// THE ZERO-STEP CASE IS THE ONE THAT IS NOT ABOUT THE BOUND, and it is
// transcribed rather than derived. std tests `next < start or next > start` and
// stops WITHOUT emitting when neither holds, with its own comment: "A zero step
// would spin forever; stop without emitting, which is what the pull form's
// `None` meant here." So `Range.step_by(1..=9, 0)` is `[]` — not `[1]`, and not
// a hang. A `moved` test written as `!=` would be the same answer here and a
// different one for an element type whose ordering and equality disagree, so the
// comparator is asked exactly as std asks it.
//
// The bound test uses `<=` / `<` where RangeEachWhile uses `>` / `>=`, because
// std spells this one as `in_bounds` and that one as `done`. Same predicate,
// opposite polarity; kept in each function's own spelling so either can be read
// against its source.
func StepByRangeSeq[T, S any](r Range[T], by S, cmp func(fr *Frame, a, b T) Ordering, step func(fr *Frame, v T, by S) Maybe[T]) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		cur := r.Start
		for {
			if r.End.Tag == TagSome {
				rank := OrderingRank(cmp(fr, cur, r.End.Some))
				if r.Inclusive {
					if rank > 0 {
						return true
					}
				} else if rank >= 0 {
					return true
				}
			}
			nxt := step(fr, cur, by)
			if nxt.Tag != TagSome {
				// The element type saturated: `cur` is the last element.
				return yield(fr, cur)
			}
			if OrderingRank(cmp(fr, nxt.Some, cur)) == 0 {
				// A step that does not move. Stop WITHOUT emitting.
				return true
			}
			if !yield(fr, cur) {
				return false
			}
			cur = nxt.Some
		}
	}}
}

// --- rendering --------------------------------------------------------------
