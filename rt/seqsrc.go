package rt

import (
	"iter"

	"github.com/rivo/uniseg"
)

// The `Iter` SOURCES beyond List, the lazy adapters that carry per-run state,
// and the materializing terminals.
//
// # What this file is answering
//
// Before it, a lowered pipeline could start at a List, at `Iter.from`, or at
// another pipeline, and nothing else — a String or a Map refused as
// `Iter over an unlowered source` with the type in the detail. And of std's
// twenty-four `Iter` functions, nine were lowered; the other fifteen refused
// with their own name in the detail.
//
// Both refusals were honest and neither was a representation problem. A source
// needs a Go `each_while` and every one of these needs the same three lines;
// an adapter needs a wrapper whose per-run state is a local. See
// internal/irbuild/iter.go for the builder half.
//
// # Per-run state is what makes these different from seq.go's six
//
// `map` and `filter` are stateless: their whole wrapper is one call. `drop`,
// `with_index`, `cycle` and `chunks` each carry a counter or a buffer, and
// std/iter.nomi promises a bound pipeline consumed twice yields the same
// sequence. So the state lives INSIDE `Run` and never in the closure over the
// `Seq` — the rule seq.go's `TakeEach` states and the one thing about this file
// a reader must not undo. A counter hoisted one scope out passes every
// single-consumption test and prints an empty list for the second consumption.
//
// # Everything here is specified, not derived
//
// Each function below implements a std/iter.nomi builtin, and the golden files
// pin what programs over them print. The `completed || ended` bookkeeping, `chunks`' trailing short chunk, `cycle`'s empty-source
// guard, `drop`'s "skip counts elements the source produced, not elements the
// consumer accepted" — none of those is a choice made here.

// --- String -----------------------------------------------------------------

// EachGraphemeCluster walks s's grapheme clusters, handing each to yield, and
// reports whether it ran to exhaustion.
//
// Frame-free and error-free on purpose: this is the boundary RULE, and each
// caller wraps it in its own protocol (`StringEachWhile` below is one). One
// encoding, which is the whole reason `String.length` / `slice` / `reverse`
// live in grapheme.go too — see that file's header. A second copy of a UAX #29
// walk is a divergence waiting for the input the fixtures do not name.
//
// The segmentation state is THREADED between calls, which is what uniseg's API
// asks for. Measured over regional-indicator pairs, ZWJ sequences, CRLF, a Thai
// vowel sign and a tag-sequence flag, a restarted state agrees on all ten — so
// this is not a bug fix, it is the removal of a question.
func EachGraphemeCluster(s string, yield func(cluster string) bool) bool {
	state := -1
	for len(s) > 0 {
		var c string
		c, s, _, state = uniseg.FirstGraphemeClusterInString(s, state)
		if !yield(c) {
			return false
		}
	}
	return true
}

// StringEachWhile is `impl Iter for String`'s
// `host fn each_while(s: String, yield: (String) -> Bool): Bool`.
func StringEachWhile(fr *Frame, s string, yield func(fr *Frame, item string) bool) bool {
	return EachGraphemeCluster(s, func(cluster string) bool { return yield(fr, cluster) })
}

// StringSeq views a String as a push sequence of its grapheme clusters.
func StringSeq(s string) Seq[string] {
	return Seq[string]{Run: func(fr *Frame, yield func(fr *Frame, item string) bool) bool {
		return StringEachWhile(fr, s, yield)
	}}
}

// --- Bytes ------------------------------------------------------------------

// BytesEachWhile is `impl Iter for Bytes`'s
// `host fn each_while(data: Bytes, yield: (Byte) -> Bool): Bool`.
//
// OCTETS, not grapheme clusters and not runes, and the distinction from
// StringSeq above is the whole
// reason the two are separate walks over what is, in this package, the same Go
// `string`: `rt.Bytes` is a `string` for the reasons rt/bytes.go gives.
//
// `for i := range len(data)` and NOT `for i := range data`, which is the one
// mistake this function exists in order not to make: `range` over a Go string
// yields `(byteIndex, rune)` at rune BOUNDARIES, so it would silently skip the
// continuation bytes of every multi-byte sequence. `String.to_bytes("café")` is
// five octets and four runes, and that is exactly the case the corpus checks —
// 04-scalars-and-text/strings_test.nomi expects `[99, 97, 102, 195, 169]`,
// which a rune walk renders as `[99, 97, 102, 233]`.
func BytesEachWhile(fr *Frame, data Bytes, yield func(fr *Frame, item Byte) bool) bool {
	for i := range len(data) {
		if !yield(fr, Byte(data[i])) {
			return false
		}
	}
	return true
}

// BytesSeq views a Bytes as a push sequence of its octets. One closure,
// allocated once.
func BytesSeq(data Bytes) Seq[Byte] {
	return Seq[Byte]{Run: func(fr *Frame, yield func(fr *Frame, item Byte) bool) bool {
		return BytesEachWhile(fr, data, yield)
	}}
}

// --- Map --------------------------------------------------------------------

// MapEachWhile is `impl Iter for Map<K, V>`'s
// `host fn each_while(m: Map<K, V>, yield: ((K, V)) -> Bool): Bool`.
//
// `pair` builds the caller's `(K, V)` tuple, because a Nomi tuple lowers to an
// ANONYMOUS Go struct generated per package and rt cannot name it. The closure
// is allocated once when the pipeline is built, not per element.
//
// The order is `MapEntries`', which is INSERTION order and is the single
// ordered accessor in this package: `FormatMap` and `MapEqual` read it too.
// Walking
// the trie directly here would be a third order and it would be wrong.
func MapEachWhile[K, V, P any](fr *Frame, m Map[K, V], pair func(K, V) P, yield func(fr *Frame, item P) bool) bool {
	for _, e := range MapEntries(m) {
		if !yield(fr, pair(e.Key, e.Val)) {
			return false
		}
	}
	return true
}

// MapSeq views a Map as a push sequence of its `(K, V)` pairs.
//
// `MapEntries` is called inside `Run` rather than once here, so the sequence is
// replayable and holds no materialized slice between consumptions.
func MapSeq[K, V, P any](m Map[K, V], pair func(K, V) P) Seq[P] {
	return Seq[P]{Run: func(fr *Frame, yield func(fr *Frame, item P) bool) bool {
		return MapEachWhile(fr, m, pair, yield)
	}}
}

// --- known_count: the O(1)-count protocol -----------------------------------
//
// `Iter.known_count` is the interface's second, `open` member. Its default is
// `None` and count-storing sources override it, which is what makes
// `Iter.count` O(1) on a List or a Map and O(n) on everything else
// (std/iter.nomi's `count` is a `case` over exactly this).
//
// internal/irbuild answers from the source's STATIC kind, so these are three
// separate functions rather than one dispatch. That is strictly better than a
// runtime protocol call and it is the same choice `Iter.count` already makes.

// ListCellKnownCount is `impl Iter for List<T>`'s `known_count`: the cached
// length, over the consumer's cell type.
func ListCellKnownCount[T any, N ListCellShape[T, N]](xs *N) Maybe[int64] {
	return Some(ListCellCount[T, N](xs))
}

// MapKnownCount is `impl Iter for Map<K, V>`'s override, `Some(size(m))`.
func MapKnownCount[K, V any](m Map[K, V]) Maybe[int64] { return Some(MapSize(m)) }

// SeqKnownCount and StringKnownCount are the interface DEFAULT, and the two
// sources that inherit it.
//
// A `Seq` inherits it, unless it views a source with an override (Seq.Count),
// because a lazy pipeline cannot say how many elements it will yield without
// running — std's `Seq` overrides nothing, and an unbounded
// source would never answer at all.
//
// A String inherits it for a different reason, and the difference matters:
// std/strings declares `each_while` and NO `known_count`, because a grapheme
// cluster count is inherently O(n) — the boundary rule has to be run. Answering
// `Some(int64(len(s)))` here would be O(1) and WRONG (bytes are not clusters),
// and answering `Some(StringLength(s))` would be a correct number obtained by
// doing exactly the work `known_count` exists to avoid, which would make
// `Iter.known_count` on text a silent O(n) where a caller chose it to be sure
// of O(1). `String.length` is the spelling for the count; this is the spelling
// for "ask me and I will decline".
//
// Each TAKES its source and ignores it, and the parameter is not decorative: a
// no-argument `NoKnownCount()` would let a caller drop the source expression
// entirely, and building a pipeline is not free of effects — the list literal
// is evaluated, the lambdas are closed over. Nomi evaluates the argument and
// then calls the protocol default, so these evaluate it exactly once and then
// decline. It is also the difference between
// declining and CONSUMING: neither calls `Run`, which is what makes
// `known_count` the safe probe on an infinite source.
func SeqKnownCount[T any](fr *Frame, src Seq[T]) Maybe[int64] {
	if src.Count != nil {
		// A view answers its source's override (see Seq).
		return src.Count(fr)
	}
	return None[int64]()
}

// StringKnownCount is the same decline for a String. See SeqKnownCount.
func StringKnownCount(s string) Maybe[int64] { return None[int64]() }

// --- the protocol primitive --------------------------------------------------

// SeqEachWhile is `Iter.each_while(source, yield)` over a lowered source: the
// interface function itself, called by name rather than through an adapter.
func SeqEachWhile[T any](fr *Frame, src Seq[T], yield func(fr *Frame, item T) bool) bool {
	return src.Run(fr, yield)
}

// --- positional terminals -----------------------------------------------------

// SeqLast is `pub fn last<T>(source: Iter<T>): Maybe<T>`, whose std body is
// `reduce(source, |_acc = None, item| Some(item))`.
//
// It consumes the WHOLE source — there is no way to know an element is the last
// until the next one fails to arrive — so it does not terminate on an infinite
// source. std says so and this reproduces it rather than guarding it:
// infiniteness is undecidable, so nothing detects it.
func SeqLast[T any](fr *Frame, src Seq[T]) Maybe[T] {
	out := None[T]()
	src.Run(fr, func(fr *Frame, item T) bool {
		out = Some(item)
		return true
	})
	return out
}

// SeqAt is `pub fn at<T>(source: Iter<T>, index: Int): Maybe<T>`.
//
// A NEGATIVE index is None without touching the source, which is std's own
// `if index < 0 { None }` guard and is observable: the source may have effects.
// Otherwise the walk stops as soon as the element is found, which is std's
// `break (0, Some(item))`.
func SeqAt[T any](fr *Frame, src Seq[T], index int64) Maybe[T] {
	if index < 0 {
		return None[T]()
	}
	out := None[T]()
	remaining := index
	src.Run(fr, func(fr *Frame, item T) bool {
		if remaining == 0 {
			out = Some(item)
			return false
		}
		remaining--
		return true
	})
	return out
}

// --- lazy adapters with per-run state ----------------------------------------

// DropEach is `host fn drop_each<T>(source: Iter<T>, n: Int, yield): Bool`.
//
// `skip` counts elements the SOURCE produced, not elements the consumer
// accepted, and it is decremented before the consumer is ever consulted — so a
// dropped element is not offered downstream and does not count against a
// downstream `take`.
func DropEach[T any](fr *Frame, src Seq[T], n int64, yield func(fr *Frame, item T) bool) bool {
	skip := n
	return src.Run(fr, func(fr *Frame, item T) bool {
		if skip > 0 {
			skip--
			return true
		}
		return yield(fr, item)
	})
}

// SeqDrop is `pub fn drop<T>(source: Iter<T>, n: Int): Iter<T>`.
func SeqDrop[T any](src Seq[T], n int64) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		return DropEach(fr, src, n, yield)
	}}
}

// WithIndexEach is `host fn with_index_each<T>(source: Iter<T>, yield): Bool`.
//
// `pair` builds the `(Int, T)` tuple for the same reason MapEachWhile takes
// one: the tuple is an anonymous Go struct rt cannot name.
//
// The index is incremented for every element the SOURCE produced, whether or
// not the consumer accepted it — which is the only reading that makes
// `drop(1) |> with_index()` start at 0 and `with_index() |> filter(...)` keep
// the original positions. So the index increments before yielding.
func WithIndexEach[T, P any](fr *Frame, src Seq[T], pair func(int64, T) P, yield func(fr *Frame, item P) bool) bool {
	index := int64(0)
	return src.Run(fr, func(fr *Frame, item T) bool {
		p := pair(index, item)
		index++
		return yield(fr, p)
	})
}

// SeqWithIndex is `pub fn with_index<T>(source: Iter<T>): Iter<(Int, T)>`.
func SeqWithIndex[T, P any](src Seq[T], pair func(int64, T) P) Seq[P] {
	return Seq[P]{Run: func(fr *Frame, yield func(fr *Frame, item P) bool) bool {
		return WithIndexEach(fr, src, pair, yield)
	}}
}

// SeqConcat is `pub fn concat<T>(a: Iter<T>, b: Iter<T>): Iter<T>`.
//
// The one adapter std writes with no host help, because "drive a, then drive b,
// unless the consumer already said stop" is exactly what the protocol's Bool
// means. This is std's body transcribed, not an extern.
func SeqConcat[T any](a, b Seq[T]) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		if a.Run(fr, yield) {
			return b.Run(fr, yield)
		}
		return false
	}}
}

// CycleEach is `host fn cycle_each<T>(source: Iter<T>, yield): Bool`.
//
// The empty-source guard is the load-bearing line: a source that yields nothing
// must answer True immediately rather than spinning forever asking it again.
// `any` is per PASS, not per cycle, so a source that empties later still stops.
func CycleEach[T any](fr *Frame, src Seq[T], yield func(fr *Frame, item T) bool) bool {
	for {
		any := false
		completed := src.Run(fr, func(fr *Frame, item T) bool {
			any = true
			return yield(fr, item)
		})
		if !completed {
			return false
		}
		if !any {
			return true
		}
	}
}

// SeqCycle is `pub fn cycle<T>(source: Iter<T>): Iter<T>`.
func SeqCycle[T any](src Seq[T]) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		return CycleEach(fr, src, yield)
	}}
}

// RepeatEach is `host fn repeat_each<T>(x: T, yield): Bool`.
//
// Unbounded, and it can only end by the consumer refusing an element — so it
// returns False or it does not return. There is no True branch and that is
// correct rather than an omission.
func RepeatEach[T any](fr *Frame, x T, yield func(fr *Frame, item T) bool) bool {
	for {
		if !yield(fr, x) {
			return false
		}
	}
}

// SeqRepeat is `pub fn repeat<T>(x: T): Iter<T>`.
func SeqRepeat[T any](x T) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		return RepeatEach(fr, x, yield)
	}}
}

// IterateEach is `host fn iterate_each<T>(seed: T, step: (T) -> T, yield): Bool`.
//
// The element yielded is the CURRENT state and the callback's answer is the
// NEXT one, so `iterate(1, |x| x * 2)` yields 1 before it yields 2. Getting
// that backwards produces a sequence that is right in every element but the
// first, which is the kind of off-by-one a `take(4)` fixture catches and a
// `first()` one does not.
//
// A callback's control signals collapse to one here for the reason seq.go's
// MapEach gives: `break` and `continue` in a lowered callback are
// refused by name at the call site, so a Go func's return value is the only
// answer that can arrive.
func IterateEach[T any](fr *Frame, seed T, step func(fr *Frame, state T) T, yield func(fr *Frame, item T) bool) bool {
	current := seed
	for {
		next := step(fr, current)
		if !yield(fr, current) {
			return false
		}
		current = next
	}
}

// IterateEachCtl is IterateEach for a step callback that can `break`.
//
// The step runs before the current state is emitted, as IterateEach's does.
// CtlEmit emits the current state and advances to the answer; CtlEmitStop
// (`break v`) emits the ANSWER, not the current state, and stops; CtlStop
// (bare `break`) stops without emitting. CtlSkip has no next state to fall forward to, so the caller refuses it
// before answering; here it stops.
func IterateEachCtl[T any](fr *Frame, seed T, step func(fr *Frame, state T) (T, Ctl), yield func(fr *Frame, item T) bool) bool {
	current := seed
	for {
		next, c := step(fr, current)
		switch c {
		case CtlEmit:
			if !yield(fr, current) {
				return false
			}
			current = next
		case CtlEmitStop:
			return yield(fr, next)
		default:
			return true
		}
	}
}

// SeqIterateCtl is SeqIterate over IterateEachCtl.
func SeqIterateCtl[T any](seed T, step func(fr *Frame, state T) (T, Ctl)) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		return IterateEachCtl(fr, seed, step, yield)
	}}
}

// SeqIterate is `pub fn iterate<T>(seed: T, step: (T) -> T): Iter<T>`.
func SeqIterate[T any](seed T, step func(fr *Frame, state T) T) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		return IterateEach(fr, seed, step, yield)
	}}
}

// FlatMapEach is `host fn flat_map_each<T, U>(source, f: (T) -> Iter<U>, yield): Bool`.
//
// The inner source's "consumer stopped" answer is the OUTER one: a downstream
// `take` that fills up inside an inner sequence must shut the outer source down
// too, so the inner Run's answer is returned straight through.
func FlatMapEach[T, U any](fr *Frame, src Seq[T], f func(fr *Frame, item T) Seq[U], yield func(fr *Frame, item U) bool) bool {
	return src.Run(fr, func(fr *Frame, item T) bool {
		return f(fr, item).Run(fr, yield)
	})
}

// SeqFlatMap is `pub fn flat_map<T, U>(source: Iter<T>, f: (T) -> Iter<U>): Iter<U>`.
func SeqFlatMap[T, U any](src Seq[T], f func(fr *Frame, item T) Seq[U]) Seq[U] {
	return Seq[U]{Run: func(fr *Frame, yield func(fr *Frame, item U) bool) bool {
		return FlatMapEach(fr, src, f, yield)
	}}
}

// FlatMapListEach is FlatMapEach for a callback that answers a LIST.
//
// `Iter.flat_map(xs, |x| [x, x * 10])` is the common spelling and its callback's
// declared result is `Iter<U>`, which a `List<U>` satisfies. Wrapping each
// returned list in a `Seq` would allocate a closure PER ELEMENT of the outer
// source; walking the cons chain here allocates nothing. Two entry points
// rather than one because the caller knows statically which it has.
func FlatMapListEach[T, U any](fr *Frame, src Seq[T], f func(fr *Frame, item T) *List[U], yield func(fr *Frame, item U) bool) bool {
	return src.Run(fr, func(fr *Frame, item T) bool {
		return ListEachWhile(fr, f(fr, item), yield)
	})
}

// SeqFlatMapList is `flat_map` over a callback answering a List.
func SeqFlatMapList[T, U any](src Seq[T], f func(fr *Frame, item T) *List[U]) Seq[U] {
	return Seq[U]{Run: func(fr *Frame, yield func(fr *Frame, item U) bool) bool {
		return FlatMapListEach(fr, src, f, yield)
	}}
}

// ChunksEach is `host fn chunks_each<T>(source: Iter<T>, size: Int, yield): Bool`.
//
// Three details are observable:
//
//   - a non-positive size yields NOTHING and answers True without touching the
//     source, so `chunks(0)` terminates over an unbounded one.
//   - the TRAILING short chunk is emitted after the source exhausts, and only
//     when the source exhausted — a consumer that stopped mid-buffer must not
//     then receive a partial chunk it did not ask for.
//   - the buffer is copied into each chunk. `buf = buf[:0]` reuses the backing
//     array, so handing the slice to the List builder without copying would let
//     the next chunk overwrite the previous one's elements.
func ChunksEach[T any](fr *Frame, src Seq[T], size int64, yield func(fr *Frame, item *List[T]) bool) bool {
	if size <= 0 {
		return true
	}
	buf := make([]T, 0, size)
	completed := src.Run(fr, func(fr *Frame, item T) bool {
		buf = append(buf, item)
		if int64(len(buf)) < size {
			return true
		}
		chunk := listOf(buf)
		buf = buf[:0]
		return yield(fr, chunk)
	})
	if !completed {
		return false
	}
	if len(buf) > 0 {
		return yield(fr, listOf(buf))
	}
	return true
}

// SeqChunks is `pub fn chunks<T>(source: Iter<T>, size: Int): Iter<List<T>>`.
func SeqChunks[T any](src Seq[T], size int64) Seq[*List[T]] {
	return Seq[*List[T]]{Run: func(fr *Frame, yield func(fr *Frame, item *List[T]) bool) bool {
		return ChunksEach(fr, src, size, yield)
	}}
}

// --- zip, the one adapter push cannot express directly -----------------------

// seqPull turns a push sequence into a PULL cursor: `next` yields the following
// element, `stop` abandons the rest.
//
// `iter.Pull` from Go's standard library, which is a coroutine plus a handoff
// per element. std/iter.nomi's `zip` comment calls this out by name — "`zip_each`
// therefore runs `b` as a pull cursor — Nomi's `iter.Pull`, and like Go's it
// costs a coroutine plus a handoff per element" — so this is std's own stated
// cost rather than an implementation choice made here.
//
// STOP IS NOT OPTIONAL. A cursor abandoned without it leaks its coroutine, and
// for an unbounded source that coroutine is parked forever. `SeqZip` defers it
// on every exit, which is what makes
// `Iter.zip(Iter.from(0), Iter.from(100)) |> Iter.first()` — one pair taken from
// two infinite sources — terminate and clean up rather than strand the pulled
// side.
//
// The Frame handed to the inner yield is DROPPED and the enclosing `fr` is used
// instead, deliberately: `iter.Seq`'s yield carries no frame, and a pulled
// element is a VALUE — the position a fault should report is the zip consumer's,
// which is where `fr` comes from.
func seqPull[U any](fr *Frame, src Seq[U]) (next func() (U, bool), stop func()) {
	return iter.Pull(func(yield func(U) bool) {
		src.Run(fr, func(_ *Frame, item U) bool { return yield(item) })
	})
}

// SeqZip is `pub fn zip<T, U>(a: Iter<T>, b: Iter<U>): Iter<(T, U)>`.
//
// The LEFT source drives and the right becomes a pull cursor, because two push
// sources cannot both be in charge of the loop. That asymmetry is the reason zip
// is "the lone adapter a push protocol cannot express directly" in std's own
// words, and it is why zip keeps working when EITHER side is infinite: a
// materializing right side would never return, and an abandoned cursor would
// hang the left.
//
// THE ANSWER IS `completed || exhausted`, which is worth spelling out because two of its three cases answer TRUE:
//
//	left ran out          completed  -> true   the zip exhausted
//	right ran out first   exhausted  -> true   the zip exhausted
//	a consumer stopped    neither    -> false  somebody refused another element
//
// The middle row is the one that is easy to get wrong. When the right cursor is
// spent, this stops the LEFT source by returning false into it — the same signal
// a consumer stop uses — so `completed` is false while the zip genuinely
// exhausted. Reporting `completed` alone would tell every enclosing adapter that
// a consumer had refused an element, which is the protocol's other meaning.
//
// The pair constructor is emitted by the call site, for the reason every
// tuple-shaped entry point in this package takes one: a Nomi tuple lowers to a
// per-package anonymous Go struct rt cannot name.
func SeqZip[T, U, P any](left Seq[T], right Seq[U], pair func(T, U) P) Seq[P] {
	return Seq[P]{Run: func(fr *Frame, yield func(fr *Frame, item P) bool) bool {
		next, stop := seqPull(fr, right)
		defer stop()
		exhausted := false
		completed := left.Run(fr, func(fr *Frame, l T) bool {
			r, more := next()
			if !more {
				exhausted = true
				return false
			}
			return yield(fr, pair(l, r))
		})
		return completed || exhausted
	}}
}

// listOf builds a List from a slice, copying nothing that the caller may reuse:
// each element is consed into a fresh cell, so the backing array is not
// retained.
func listOf[T any](items []T) *List[T] {
	var out *List[T]
	for i := len(items) - 1; i >= 0; i-- {
		out = Cons(items[i], out)
	}
	return out
}

// --- materializing terminals --------------------------------------------------

// SeqReverseCells is `Iter.reverse`: the source's elements consed into a List
// of the consumer's cell type, last first.
func SeqReverseCells[T any, N ListCellShape[T, N]](fr *Frame, src Seq[T]) *N {
	var out *N
	src.Run(fr, func(fr *Frame, item T) bool {
		out = ConsCell[T, N](item, out)
		return true
	})
	return out
}

// SeqPartitionCells is `Iter.partition` over the consumer's cell type: the
// elements pred accepts and the ones it rejects, each in source order.
func SeqPartitionCells[T any, N ListCellShape[T, N], P any](fr *Frame, src Seq[T], pred func(fr *Frame, item T) bool, pair func(*N, *N) P) P {
	var yes, no []T
	src.Run(fr, func(fr *Frame, item T) bool {
		if pred(fr, item) {
			yes = append(yes, item)
		} else {
			no = append(no, item)
		}
		return true
	})
	return pair(listCellsOf[T, N](yes), listCellsOf[T, N](no))
}

// listCellsOf is listOf over the consumer's cell type.
func listCellsOf[T any, N ListCellShape[T, N]](items []T) *N {
	var out *N
	for i := len(items) - 1; i >= 0; i-- {
		out = ConsCell[T, N](items[i], out)
	}
	return out
}

// SeqSortWithCells shares sorting while preserving the consumer's cell type.
func SeqSortWithCells[T any, N ListCellShape[T, N]](fr *Frame, src Seq[T], compare func(*Frame, T, T) Ordering) *N {
	var items []T
	src.Run(fr, func(_ *Frame, item T) bool { items = append(items, item); return true })
	return SortSliceToListCells[T, N](items, func(a, b T) bool { return compare(fr, a, b).Tag == TagLess })
}

// ChunkByEach is `host fn chunk_by_each<T, K>(source, key_fn, yield): Bool`.
//
// ADJACENT runs only: unlike `group_by` it never reorders or merges
// non-adjacent elements, so one key and one buffer is the whole state. The
// trailing run is emitted on exhaustion and only on exhaustion, which is
// ChunksEach's rule.
//
// The key equality is the CALLER's, built from the projected key's kind by
// maps.go's valueEqual — the same function `==` and a Map key use, so a Nomi
// key that compares equal here compares equal everywhere.
func ChunkByEach[T, K any](fr *Frame, src Seq[T], key func(fr *Frame, item T) K, eq func(K, K) bool, yield func(fr *Frame, item *List[T]) bool) bool {
	var run []T
	var runKey K
	completed := src.Run(fr, func(fr *Frame, item T) bool {
		k := key(fr, item)
		if len(run) == 0 {
			run, runKey = append(run, item), k
			return true
		}
		if eq(k, runKey) {
			run = append(run, item)
			return true
		}
		chunk := listOf(run)
		run, runKey = []T{item}, k
		return yield(fr, chunk)
	})
	if !completed {
		return false
	}
	if len(run) > 0 {
		return yield(fr, listOf(run))
	}
	return true
}

// SeqChunkBy is `pub fn chunk_by<T, K>(source, key_fn): Iter<List<T>>`.
func SeqChunkBy[T, K any](src Seq[T], key func(fr *Frame, item T) K, eq func(K, K) bool) Seq[*List[T]] {
	return Seq[*List[T]]{Run: func(fr *Frame, yield func(fr *Frame, item *List[T]) bool) bool {
		return ChunkByEach(fr, src, key, eq, yield)
	}}
}
