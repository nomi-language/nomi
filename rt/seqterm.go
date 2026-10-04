package rt

// The `Iter` terminals that STOP EARLY, and the two adapters that stop early on
// a predicate.
//
// # These are not folds, and that is the measurement this file exists to record
//
// std/iter.nomi writes every streaming terminal over `reduce` plus a callback
// carrying a lambda-parameter DEFAULT:
//
//	pub fn any?<T>(source: Iter<T>, f: (T) -> Bool): Bool {
//	  reduce(source, |_acc = False, item| if f(item) { break True } else { False })
//	}
//
// Read as a fold, that needs two mechanisms a Go func value has nowhere to
// carry: the seed (the lambda parameter's default) and `break` (a control
// signal, not a value a Nomi function can observe).
//
// Reading them as seeded folds is wrong, and the reason is worth stating precisely because it
// is what makes this file three lines per function instead of fifty. In each of
// these terminals the seed is EXACTLY the answer for a source that ran to
// exhaustion, and `break v` is EXACTLY "stop, with this answer" — and the push
// protocol already carries both. `each_while` returns True when the source
// exhausted and False when a consumer stopped it, so:
//
//	any?        seed False  == "nothing stopped me"     -> !Run(|x| !f(x))
//	all?        seed True   == vacuous truth            ->  Run(f)
//	empty?      seed True   == "no element arrived"     ->  Run(|_| false)
//	not_empty?  seed False                             -> !Run(|_| false)
//	first/find  seed None   == "no element arrived"     -> a captured local
//	each        seed 0, discarded by `case _ -> Unit`   ->  Run(|x| { f(x); true })
//	count       seed 0, and the fold is std's own       ->  a captured counter
//
// So none of these needs a seed mechanism, and none needs a `break` channel.
// `Iter.reduce` — a USER-written accumulator with a USER-written seed — still
// does, and it is still refused; the difference is that the seed is
// unobservable in every function above and load-bearing in that one.
//
// # Polarity is the failure mode here, not cost
//
// `!Run(|x| !f(x))` type-checks whichever way the negations land, and a version
// that drains the source still answers correctly. Two things therefore have to
// be pinned separately and both are: the ANSWER for a predicate that matches in
// the middle / nowhere / everywhere, and the fact that the source is asked for
// no element after the decision is made. seqterm_test.go counts what the source
// was asked for; internal/irbuild/testdata/iter_terminals.nomi observes the same
// thing through an unbounded source, where draining does not answer slowly, it
// does not answer at all.
//
// The one property here with no observable of its own is take_while's
// `completed || ended` answer, which no list a pipeline produces can show. It is
// pinned twice for that reason: directly on the protocol Bool, and again through
// a consumer that reproduces `Iter.concat`'s rule, where collapsing it yields a
// SHORT LIST instead of a wrong Bool. `concat` is not lowered, so that second
// pin transcribes std's body rather than exercising one.
//
// # Why this is not in seq.go
//
// seq.go is the protocol plus the six functions the first slice lowered, and it
// is a settled file. Everything here shares one mechanism — a consumer that
// refuses the next element — so the split is on a real seam rather than on file
// size.

// --- terminals: the predicate family ----------------------------------------

// SeqAny is `pub fn any?<T>(source: Iter<T>, f: (T) -> Bool): Bool`.
//
// The yield is the NEGATED predicate: keep asking while nothing has matched. So
// a source that ran to exhaustion is a source in which nothing matched, and the
// answer is the negation of the protocol's Bool. A match stops the source on
// the spot, which is std's `break True`.
func SeqAny[T any](fr *Frame, src Seq[T], pred func(fr *Frame, item T) bool) bool {
	return !src.Run(fr, func(fr *Frame, item T) bool {
		return !pred(fr, item)
	})
}

// SeqAll is `pub fn all?<T>(source: Iter<T>, f: (T) -> Bool): Bool`.
//
// The predicate IS the yield, with no adaptation at all: "keep going while the
// element passes" and "every element passed" are the same statement read from
// the two ends. Vacuously true for an empty source, because an empty source
// exhausts.
func SeqAll[T any](fr *Frame, src Seq[T], pred func(fr *Frame, item T) bool) bool {
	return src.Run(fr, pred)
}

// SeqEmpty is `pub fn empty?<T>(source: Iter<T>): Bool`.
//
// Refuses the first element unconditionally, so the answer is the protocol's
// Bool unchanged: True means nothing was ever yielded. One element is asked for
// and no more, which is what makes this terminate on an unbounded source.
func SeqEmpty[T any](fr *Frame, src Seq[T]) bool {
	return src.Run(fr, func(fr *Frame, item T) bool { return false })
}

// SeqNotEmpty is `pub fn not_empty?<T>(source: Iter<T>): Bool`.
func SeqNotEmpty[T any](fr *Frame, src Seq[T]) bool { return !SeqEmpty(fr, src) }

// SeqFirst is `pub fn first<T>(source: Iter<T>): Maybe<T>`.
//
// The captured local is what std's `break Some(item)` becomes: the value leaves
// through a variable rather than through a control signal, and the source is
// stopped by the same `false` that carried the signal.
//
// Seeded with None rather than left as the Go zero value: `Maybe[T]{}` has tag
// 0, which is neither Some nor None (rt/prelude.go reserves it so a zero value
// is detectably invalid), so a source that yields nothing must be given the
// real None.
func SeqFirst[T any](fr *Frame, src Seq[T]) Maybe[T] {
	out := None[T]()
	src.Run(fr, func(fr *Frame, item T) bool {
		out = Some(item)
		return false
	})
	return out
}

// SeqFind is `pub fn find<T>(source: Iter<T>, f: (T) -> Bool): Maybe<T>`.
//
// SeqFirst with a predicate in front of it. Note which value is captured — the
// ELEMENT, not the predicate's answer — the same distinction FilterEach keeps
// and the one that produces a program that type-checks when it is got wrong.
func SeqFind[T any](fr *Frame, src Seq[T], pred func(fr *Frame, item T) bool) Maybe[T] {
	out := None[T]()
	src.Run(fr, func(fr *Frame, item T) bool {
		if !pred(fr, item) {
			return true
		}
		out = Some(item)
		return false
	})
	return out
}

// --- terminals: the exhausting family ---------------------------------------

// SeqEach is `pub fn each<T>(source: Iter<T>, f: (T) -> Unit)`.
//
// Generic in the callback's RESULT and not fixed to Unit, which is std's own
// shape: `each`'s body is `case reduce(source, |_acc = 0, item| f(item)) { _ ->
// Unit }`, so whatever the callback answers is accumulated and then discarded.
// Fixing U to Unit here would refuse a callback std accepts.
func SeqEach[T, U any](fr *Frame, src Seq[T], f func(fr *Frame, item T) U) Unit {
	src.Run(fr, func(fr *Frame, item T) bool {
		f(fr, item)
		return true
	})
	return Unit{}
}

// SeqCount is `Iter.count`'s fold arm: `reduce(source, |acc = 0, _item| acc + 1)`.
//
// Reached only when `known_count` answers None, which for a `Seq` it always
// does — a List is answered in O(1) from its cached length by ListCount, and
// internal/irbuild chooses between the two from the source's static kind, exactly
// as std chooses between them at run time (std/iter.nomi:659).
//
// The counter is `n++` and not CheckedAdd, and unlike FromEach's `n++` this is
// not even a reachable divergence: every increment costs one yield, so tripping
// Nomi's Int overflow trap would take 2^63 elements. std's `acc + 1` would trap
// there and this would wrap; no program can reach the disagreement.
func SeqCount[T any](fr *Frame, src Seq[T]) int64 {
	var n int64
	src.Run(fr, func(fr *Frame, item T) bool {
		n++
		return true
	})
	return n
}

// --- adapters bounded by a predicate ----------------------------------------

// TakeWhileEach is `host fn take_while_each<T>(source, pred, yield): Bool`.
//
// A callback answering with a control signal is not handled here:
// `break`/`continue` in a callback are refused by name at the call site. The
// `ended` bookkeeping is load-bearing:
// a predicate that says stop ENDS THIS STREAM (True — a downstream stage sees a
// normally-finished source), while a downstream consumer refusing an element is
// False. Collapsing the two makes `Iter.concat(Iter.take_while(a, p), b)` skip
// b.
func TakeWhileEach[T any](fr *Frame, src Seq[T], pred func(fr *Frame, item T) bool, yield func(fr *Frame, item T) bool) bool {
	ended := false
	completed := src.Run(fr, func(fr *Frame, item T) bool {
		if !pred(fr, item) {
			ended = true
			return false
		}
		return yield(fr, item)
	})
	return completed || ended
}

// SeqTakeWhile is `pub fn take_while<T>(source: Iter<T>, pred: (T) -> Bool): Iter<T>`.
func SeqTakeWhile[T any](src Seq[T], pred func(fr *Frame, item T) bool) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		return TakeWhileEach(fr, src, pred, yield)
	}}
}

// DropWhileEach is `host fn drop_while_each<T>(source, pred, yield): Bool`.
//
// The predicate is consulted only while
// the leading run lasts: the element that FAILS it is yielded (it is the first
// kept element, not a discarded boundary), and after that the predicate is
// never called again — `[1, 2, 3, 1] |> Iter.drop_while(|x| x < 3)` is
// `[3, 1]`, so a version that kept testing would drop the trailing 1.
//
// `started` lives inside the call and not in SeqDropWhile's closure, which is
// what keeps the sequence REPLAYABLE — the same rule TakeEach's `remaining`
// follows and for the same reason.
func DropWhileEach[T any](fr *Frame, src Seq[T], pred func(fr *Frame, item T) bool, yield func(fr *Frame, item T) bool) bool {
	started := false
	return src.Run(fr, func(fr *Frame, item T) bool {
		if !started {
			if pred(fr, item) {
				return true
			}
			started = true
		}
		return yield(fr, item)
	})
}

// SeqDropWhile is `pub fn drop_while<T>(source: Iter<T>, pred: (T) -> Bool): Iter<T>`.
func SeqDropWhile[T any](src Seq[T], pred func(fr *Frame, item T) bool) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		return DropWhileEach(fr, src, pred, yield)
	}}
}

// --- the two terminals that BUILD a Map -------------------------------------
//
// `to_map` and `group_by` are here rather than beside the folds above because
// what they need is a Map CONSTRUCTED, not a source walked differently.
//
// Both take the key's hash and equality as arguments, for the reason every Map
// entry point in this package does: a Nomi map key is compared STRUCTURALLY and
// there is no `K: Hashable` bound, so a list, a struct or a tuple is a legal key
// where Go requires `comparable`. The
// caller resolves both from the key's type.

// SeqToMap is `pub host fn to_map<K, V>(source: Iter<(K, V)>): Map<K, V>`.
//
// The element is a Nomi TUPLE, which lowers to a per-package anonymous Go struct
// this package cannot name — so the two projections arrive as closures the call
// site emits, which is iterext.go's stated rule for every tuple-shaped rt entry
// point. They are pure field reads and take no Frame.
//
// Later keys win and an existing key KEEPS its insertion position, because that
// is what MapPut does and std's `to_map` is a left-to-right walk of the source.
func SeqToMap[T, K, V any](
	fr *Frame,
	src Seq[T],
	hash func(K) uint64,
	eq func(K, K) bool,
	key func(item T) K,
	val func(item T) V,
) Map[K, V] {
	var acc Map[K, V]
	src.Run(fr, func(fr *Frame, item T) bool {
		acc = MapPut(acc, hash, eq, key(item), val(item))
		return true
	})
	return acc
}

// SeqGroupByCells is `Iter.group_by` over the consumer's cell type. wrap turns a
// finished group into the map's value type, which is the group itself for a
// compiled program and the boxed value for a consumer whose map holds boxes.
func SeqGroupByCells[T, K any, N ListCellShape[T, N], V any](
	fr *Frame,
	src Seq[T],
	hash func(K) uint64,
	eq func(K, K) bool,
	key func(fr *Frame, item T) K,
	wrap func(*N) V,
) Map[K, V] {
	var acc Map[K, *N]
	src.Run(fr, func(fr *Frame, item T) bool {
		k := key(fr, item)
		// A missing key yields the nil List, which IS the empty list, so no
		// Maybe.with_default is needed to reproduce std's `[]`.
		cur, _ := MapLookup(acc, hash, eq, k)
		acc = MapPut(acc, hash, eq, k, ConsCell[T, N](item, cur))
		return true
	})
	return MapMapValues(fr, acc, hash, eq, func(fr *Frame, v *N) V {
		return wrap(SeqReverseCells[T, N](fr, ListCellSeq[T, N](v)))
	})
}
