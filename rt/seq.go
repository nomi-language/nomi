package rt

// The `Iter` push protocol, and std/iter's one private implementor.
//
// # `Seq[T]` is not a design decision made here — it is a lowering
//
// std/iter.nomi declares exactly one `Iter` implementor:
//
//	opaque struct Seq<T> {
//	  run: ((T) -> Bool) -> Bool
//	}
//
//	impl Iter for Seq<T> {
//	  fn each_while(s: Seq<T>, yield: (T) -> Bool): Bool { s.run(yield) }
//	}
//
// A Nomi function value lowers to a Go func with a leading `*Frame`, so
// `(T) -> Bool` is `func(*Frame, T) bool` and `((T) -> Bool) -> Bool` is
// `func(*Frame, func(*Frame, T) bool) bool`. The struct below is that
// declaration lowered field for field. Nothing about it was invented: the shape
// is the language's pure-push iteration protocol, which is also Go 1.23's own
// `iter.Seq` shape, and the type argument is the element type.
//
// Why the Go type lives HERE is the reason prelude.go gives for `Maybe` and
// `Result`: a named Nomi type's identity in internal/irbuild is the declaration
// pointer its `*typeDef` came from, and a std type needs one Go type every
// module's code can name.
//
// # The existential is a closure, and under push that is not a box
//
// The existential representation was chosen after measuring a
// "closure" encoding as no better than boxing — 2 allocations per ELEMENT at
// depth 1. That measurement was of the PULL protocol, whose `next` returns
// `Self`, so every step had to rebuild a wrapper. It does not transfer, and the
// iter-protocol decision says why in as many words: with no `Self` anywhere in
// the signature, nothing is erased.
//
// Concretely: erasing a `*List[T]` to a `Seq[T]` allocates one closure, ONCE,
// when the pipeline is built. Composing an adapter allocates one more. Nothing
// allocates per element. That is why `Seq[T]` can be the uniform carrier for
// `Iter<T>` without the specialize-on-upstream machinery the pull protocol
// needed — the machinery push deleted.
//
// # What a `Seq` carries beyond `Run`
//
// `known_count` is the `Iter` interface's second, `open` member, and
// count-storing sources override it (`std/lists.nomi`) so `Iter.count` on a
// List is O(1). A lazy pipeline does not override it, so std's answer for a
// mapped or filtered sequence is `None`, and such a `Seq` has no Src and no
// Count.
//
// A VIEW is different. A List, Map, Set, Vector, String, Bytes, Range or a
// value whose own type implements `Iter` becomes a `Seq` where it enters a
// declared `Iter<T>` position (a parameter, a field, a payload): an `Iter<T>`
// holding that source is still that source, so its `known_count` is the
// source's own override and its Debug is the source's Debug. Src is the viewed
// value and Count its override; both are nil on a pipeline's sequence.
// internal/irbuild still answers `Iter.count` over a source of a known kind
// from the kind itself, without a view.

// Seq is `opaque struct Seq<T> { run: ((T) -> Bool) -> Bool }`.
//
// Run drives the source, handing each element to yield, and reports whether the
// source ran to EXHAUSTION (true) or a consumer stopped it early (false). A
// yield returning false is the protocol's "stop"; it is not an error.
type Seq[T any] struct {
	Run func(fr *Frame, yield func(fr *Frame, item T) bool) bool
	// Src is the source this sequence is a view of, nil for an adapter's or
	// a constructor's sequence. Debug renders it.
	Src any
	// Count is the viewed source's `known_count` override, nil when the
	// source inherits the declining default.
	Count func(fr *Frame) Maybe[int64]
}

// --- sources ----------------------------------------------------------------

// ListEachWhile is `impl Iter for List<T>`'s `each_while`.
//
// It walks the List in Go rather than through dispatch: the push protocol's
// payoff is that a pipeline re-enters the callback and nothing else.
func ListEachWhile[T any](fr *Frame, xs *List[T], yield func(fr *Frame, item T) bool) bool {
	return ListCellEachWhile(fr, xs, yield)
}

// UserSeq views a value whose own type implements the `Iter` protocol as a push
// sequence — `impl Iter for Tree`, where the implementor drives its own
// recursion and threads the consumer's stop decision back out through it.
//
// The impl's `each_while` is passed in rather than reached through a
// dispatch table, because the caller selected it STATICALLY: the source's kind
// named exactly one impl block. So this costs the same one closure
// ListCellSeq costs and nothing per element, and the walk is a direct Go call.
//
// Two type parameters rather than one, with the element FIRST so the call site
// can write `rt.UserSeq[int64](tree, …)` and let Go infer the receiver. R is
// not constrained to anything: a Nomi `impl` receiver may be a struct, an enum
// pointer or a newtype, and every one of them is just the first argument of the
// function being handed over.
func UserSeq[T, R any](recv R, each func(fr *Frame, r R, yield func(fr *Frame, item T) bool) bool) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		return each(fr, recv, yield)
	}}
}

// ListCount is `Iter.count` over a List: the `known_count` protocol's O(1)
// answer, which `std/lists.nomi:78` declares and `Cons` maintains.
func ListCount[T any](xs *List[T]) int64 {
	return ListCellCount[T, List[T]](xs)
}

// FromEach is std/iter's `host fn from_each(start: Int, yield): Bool` — the
// unbounded ascending source.
//
// `n++` and not CheckedAdd, which is a divergence from every other Int addition
// in this runtime and is deliberate: an `Iter.from` driven past Int max wraps
// rather than trapping, and the golden files record that behaviour.
func FromEach(fr *Frame, start int64, yield func(fr *Frame, item int64) bool) bool {
	for n := start; ; n++ {
		if !yield(fr, n) {
			return false
		}
	}
}

// SeqFrom is `pub fn from(start: Int): Iter<Int>`, whose whole body is
// `Seq{run: |yield: (Int) -> Bool| from_each(start, yield)}`.
func SeqFrom(start int64) Seq[int64] {
	return Seq[int64]{Run: func(fr *Frame, yield func(fr *Frame, item int64) bool) bool {
		return FromEach(fr, start, yield)
	}}
}

// --- adapters ---------------------------------------------------------------
//
// Each pair below is one Nomi declaration lowered, and the split is the split
// std itself makes: a `host fn <name>_each` that mediates the protocol, plus a
// one-line `pub fn <name>` whose entire body is
// `Seq{run: |yield| <name>_each(...)}`.
//
// So `SeqMap` is not a Go reimplementation of `Iter.map` — it is `Iter.map`'s
// body, which the stdlib lowering path cannot reach because the function is
// generic and its signature is outside the scalar boundary (stdlib.go). That
// the bodies really are that one call is asserted, not assumed:
// TestStdIterAdapterBodiesAreTheOneCallRTAssumes in internal/irbuild reads the
// declarations out of std/iter.nomi and fails if one grows a second statement.
//
// The `ended`/`completed` bookkeeping keeps a load-bearing distinction: True means "my stream
// ended" (the upstream exhausted, or the callback asked to stop producing) and
// False means "the DOWNSTREAM consumer refused another element". That is what
// lets a `take(3)` shut a whole chain down while `concat` still moves on to its
// second source.

// MapEach is `host fn map_each<T, U>(source: Iter<T>, f: (T) -> U, yield: (U) -> Bool): Bool`.
//
// A Nomi callback may answer with five different control signals — a value,
// Unit, `continue`, `break v`, or bare `break` — and each maps to a different
// protocol action. Two of those five are not handled here: `break` and
// `continue` are refused by name at the call site. The remaining three
// collapse into one line here, and that is not a simplification, it is what
// the signals mean once the callback is a Go func: a returned value is yielded,
// and a callback whose result kind is `Unit` returns `Unit{}` — so
// `yield(fr, f(fr, item))` is all three.
func MapEach[T, U any](fr *Frame, src Seq[T], f func(fr *Frame, item T) U, yield func(fr *Frame, item U) bool) bool {
	return src.Run(fr, func(fr *Frame, item T) bool {
		return yield(fr, f(fr, item))
	})
}

// SeqMap is `pub fn map<T, U>(source: Iter<T>, f: (T) -> U): Iter<U>`.
func SeqMap[T, U any](src Seq[T], f func(fr *Frame, item T) U) Seq[U] {
	return Seq[U]{Run: func(fr *Frame, yield func(fr *Frame, item U) bool) bool {
		return MapEach(fr, src, f, yield)
	}}
}

// FilterEach is `host fn filter_each<T>(source: Iter<T>, pred: (T) -> Bool, yield: (T) -> Bool): Bool`.
//
// Note which value is forwarded: the ELEMENT, not the predicate's answer. In
// `filter` the callback's value is the keep/drop decision, which is the one
// place `filter_each` and `map_each` differ beyond the type.
func FilterEach[T any](fr *Frame, src Seq[T], pred func(fr *Frame, item T) bool, yield func(fr *Frame, item T) bool) bool {
	return src.Run(fr, func(fr *Frame, item T) bool {
		if !pred(fr, item) {
			return true
		}
		return yield(fr, item)
	})
}

// SeqFilter is `pub fn filter<T>(source: Iter<T>, pred: (T) -> Bool): Iter<T>`.
func SeqFilter[T any](src Seq[T], pred func(fr *Frame, item T) bool) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		return FilterEach(fr, src, pred, yield)
	}}
}

// TakeEach is `host fn take_each<T>(source: Iter<T>, n: Int, yield: (T) -> Bool): Bool`.
//
// Two details are observable:
//
//   - `n <= 0` yields NOTHING and answers True. It does not touch the source,
//     which is what makes `Iter.from(1) |> Iter.take(0)` terminate.
//   - the element is yielded BEFORE the counter is decremented, so `take(3)`
//     over a three-element source stops on its own rather than asking the
//     source for a fourth element. The answer is `completed || bounded`: the
//     stream ended either because the source ran out or because the bound was
//     reached, and both are "exhausted" as far as a downstream stage is
//     concerned.
//
// `remaining` lives inside Run and not in the closure over the Seq, which is
// what keeps a `Seq` REPLAYABLE — std/iter.nomi's header promises that binding
// a pipeline and consuming it twice yields the same sequence. A counter hoisted
// into SeqTake's closure would make the second run yield nothing.
func TakeEach[T any](fr *Frame, src Seq[T], n int64, yield func(fr *Frame, item T) bool) bool {
	if n <= 0 {
		return true
	}
	remaining := n
	bounded := false
	completed := src.Run(fr, func(fr *Frame, item T) bool {
		if !yield(fr, item) {
			return false
		}
		remaining--
		if remaining == 0 {
			bounded = true
			return false
		}
		return true
	})
	return completed || bounded
}

// SeqTake is `pub fn take<T>(source: Iter<T>, n: Int): Iter<T>`.
func SeqTake[T any](src Seq[T], n int64) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		return TakeEach(fr, src, n, yield)
	}}
}

// --- terminals --------------------------------------------------------------

// SeqReduce is `pub host fn reduce<T, U>(source: Iter<T>, f: (U, T) -> U): U`
// where the callback declares a first-parameter DEFAULT — the SEED.
//
// # Why the seed is a parameter here and not a field of anything
//
// In Nomi the seed is written inside the callback: `Iter.reduce(xs, |acc = 0,
// x| acc + x)`. It is not an ordinary default, because nothing is applied on an
// arity mismatch — the CALLEE reads it out of the function value and
// evaluates it off the lambda's own closure. A Go func value has nowhere to
// carry that, so the caller lifts the default out at the CALL SITE and hands
// it here.
//
// It is an argument to this function rather than a field on `Seq` for a reason
// that is a correctness one and not a tidiness one: the seed belongs to the
// CALLBACK, not to the source. The same `Seq` may be reduced twice with two
// different seeds, and std/iter declares `Seq` with exactly one field, so a
// second one would be a type std does not have.
//
// The Bool `Run` answers is deliberately dropped. Under the push protocol False
// means a consumer refused the next element, and this consumer never does: a
// callback that says `break` is refused at the call site (internal/irbuild/iter.go),
// so the only way the loop ends early is the SOURCE ending it, and the
// accumulator is the answer either way.
func SeqReduce[T, U any](fr *Frame, src Seq[T], seed U, f func(fr *Frame, acc U, item T) U) U {
	acc := seed
	src.Run(fr, func(fr *Frame, item T) bool {
		acc = f(fr, acc, item)
		return true
	})
	return acc
}

// SeqReduceFirst is the same terminal over a callback with NO default: the
// FIRST element seeds the accumulator and iteration starts at the second, which
// is what std/iter.nomi:146 documents.
//
// Measured rather than inferred from the doc comment, because the two halves
// disagree in an interesting way. `Iter.reduce([4, 5, 6], |acc, x| acc)` is 4
// and `|acc, x| x` is 6, so the first element really does seed
// rather than being folded in. An EMPTY source is a fault, not a zero:
//
//	Iter.reduce: cannot reduce empty collection without initial value
//
// captured verbatim off `nomi run` (stderr, exit 1, no `line N:` prefix — this
// fault names no source position).
//
// The accumulator's type is the ELEMENT's here, which it is not in SeqReduce:
// with the first element seeding, U and T are the same type by construction.
func SeqReduceFirst[T any](fr *Frame, src Seq[T], f func(fr *Frame, acc T, item T) T) T {
	var acc T
	seeded := false
	src.Run(fr, func(fr *Frame, item T) bool {
		if !seeded {
			acc, seeded = item, true
			return true
		}
		acc = f(fr, acc, item)
		return true
	})
	if !seeded {
		Trap(emptyReduceText())
	}
	return acc
}

func emptyReduceText() string {
	return "Iter.reduce: cannot reduce empty collection without initial value"
}
