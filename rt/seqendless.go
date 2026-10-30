package rt

// The `is_infinite` protocol: a sequence that knows it never ends.
//
// `Iter.repeat(x)`, `Iter.from(n)`, `Iter.iterate(seed, step)` and an
// unbounded `Range.from(n)` never end on their own, and nothing in
// `known_count` can say so: it answers a count or declines. So `Seq` carries
// a second answer, Endless: the name of the source that makes the sequence
// endless, or "" when it may end. A terminal that has to reach the end
// (`Iter.to_list`, `Iter.count`, `Iter.sum`, ...) asks it before it drives the
// source and traps at once (SeqMustEnd) instead of running until memory runs
// out.
//
// THE ANSWER IS SET WHEN THE PIPELINE IS BUILT, NEVER PER ELEMENT. A source
// sets a constant function; an adapter copies its upstream's field (map,
// filter, drop, with_index, drop_while, chunk_by, scan, flat_map), clears it
// (take, take_while, and every adapter whose callback can `break`), or
// combines two (zip: both endless; concat: either). Asking costs one call per
// terminal. Only cycle, zip and concat allocate for it, one closure when the
// pipeline is built and only when an upstream has an answer to combine.
//
// A WRONG "ENDLESS" IS WORSE THAN NONE. It turns a program that finishes into
// one that traps, where a missing answer only leaves a hang that was already
// there. So every rule here answers "" whenever it cannot be sure: a callback
// that can `break` may end the stream, a cycle whose source may be empty ends
// at once, and a flat_map whose inner sequences are endless is not known to
// be endless until one of them runs.
//
// A source that never ends also never completes a filter: `Iter.filter(src,
// p)` of an endless source stays endless even though it may yield nothing,
// because driving it to its end never finishes either way.

var (
	endlessFrom    = func(*Frame) string { return "Iter.from" }
	endlessRepeat  = func(*Frame) string { return "Iter.repeat" }
	endlessIterate = func(*Frame) string { return "Iter.iterate" }
	endlessRange   = func(*Frame) string { return "Range.from" }
)

// SeqEndless is the name of the source that makes src endless, or "" when
// src may end.
func SeqEndless[T any](fr *Frame, src Seq[T]) string {
	if src.Endless == nil {
		return ""
	}
	return src.Endless(fr)
}

// SeqInfinite is `Iter.is_infinite(source)`.
func SeqInfinite[T any](fr *Frame, src Seq[T]) bool { return SeqEndless(fr, src) != "" }

// InfiniteIterationText is the trap a terminal that must reach the end of its
// source raises when the source never ends: `Iter.to_list of an infinite
// iterator (Iter.repeat) never finishes; ...`. terminal is the operation the
// program called and source the one that makes the sequence endless.
func InfiniteIterationText(terminal, source string) string {
	return "`" + terminal + "` of an infinite iterator (`" + source +
		"`) never finishes; limit it with `Iter.take` or `Iter.take_while`"
}

// SeqMustEnd traps with InfiniteIterationText when src never ends, before
// anything is asked of it. terminal names the operation that would run
// forever.
func SeqMustEnd[T any](fr *Frame, src Seq[T], terminal string) {
	if source := SeqEndless(fr, src); source != "" {
		Trap(InfiniteIterationText(terminal, source))
	}
}

// WithEndless is src answering endless as its Endless: a view of a source
// whose own type says it never ends (a user `impl Iter` that writes
// `is_infinite`).
func WithEndless[T any](src Seq[T], endless func(fr *Frame) string) Seq[T] {
	src.Endless = endless
	return src
}

// takeCount is `Iter.take(src, n)`'s known_count: n of an endless source,
// the lesser of n and the source's count when it has one, and nil (declining)
// otherwise. A non-positive n yields nothing.
func takeCount[T any](src Seq[T], n int64) func(fr *Frame) Maybe[int64] {
	if src.Count == nil && src.Endless == nil {
		return nil
	}
	n = max(n, 0)
	return func(fr *Frame) Maybe[int64] {
		if SeqEndless(fr, src) != "" {
			return Some(n)
		}
		if c := SeqKnownCount(fr, src); c.Tag == TagSome {
			return Some(min(c.Some, n))
		}
		return None[int64]()
	}
}

// endlessEither is concat's answer: endless when either part is, named by
// the first that is.
func endlessEither[T any](a, b Seq[T]) func(fr *Frame) string {
	switch {
	case a.Endless == nil:
		return b.Endless
	case b.Endless == nil:
		return a.Endless
	}
	return func(fr *Frame) string {
		if name := a.Endless(fr); name != "" {
			return name
		}
		return b.Endless(fr)
	}
}

// endlessBoth is zip's answer: endless only when both sides are, since the
// shorter side ends the zip. It is named by the left side.
func endlessBoth[T, U any](a Seq[T], b Seq[U]) func(fr *Frame) string {
	if a.Endless == nil || b.Endless == nil {
		return nil
	}
	return func(fr *Frame) string {
		name := a.Endless(fr)
		if name == "" || b.Endless(fr) == "" {
			return ""
		}
		return name
	}
}

// cycleEndless is cycle's answer: endless when its source is, and named
// `Iter.cycle` when the source is finite but known to be non-empty. A source
// whose count is unknown may be empty, and the cycle of an empty source is
// empty, so it declines.
func cycleEndless[T any](src Seq[T]) func(fr *Frame) string {
	if src.Endless == nil && src.Count == nil {
		return nil
	}
	return func(fr *Frame) string {
		if name := SeqEndless(fr, src); name != "" {
			return name
		}
		if c := SeqKnownCount(fr, src); c.Tag == TagSome && c.Some > 0 {
			return "Iter.cycle"
		}
		return ""
	}
}

// chunksEndless is chunks' answer: a non-positive size yields nothing.
func chunksEndless[T any](src Seq[T], size int64) func(fr *Frame) string {
	if size <= 0 {
		return nil
	}
	return src.Endless
}

// rangeEndless is an unbounded Range's answer. std builds an unbounded Range
// only over Int (`Range.from`, `Range.naturals`), and an Int has no largest
// value, so every one is endless.
func rangeEndless[T any](r Range[T]) func(fr *Frame) string {
	if r.End.Tag == TagSome {
		return nil
	}
	return endlessRange
}

// stepByEndless is `Range.step_by`'s answer over an unbounded range: endless
// when the first step moves. A step that does not move yields nothing.
// Asking takes one step, which is the element type's `Steppable.step_by`.
func stepByEndless[T, S any](r Range[T], by S, cmp func(fr *Frame, a, b T) Ordering, step func(fr *Frame, v T, by S) Maybe[T]) func(fr *Frame) string {
	if r.End.Tag == TagSome {
		return nil
	}
	return func(fr *Frame) string {
		next := step(fr, r.Start, by)
		if next.Tag != TagSome {
			return ""
		}
		if OrderingRank(cmp(fr, next.Some, r.Start)) == 0 {
			return ""
		}
		return "Range.step_by"
	}
}
