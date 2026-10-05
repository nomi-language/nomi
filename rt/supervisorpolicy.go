package rt

import "time"

// The policy vocabulary of `std/supervisors`: the four enums a
// `Supervisor.new` call names. `Restart` is next door in restart.go.
//
// # Why these are here rather than in a generated package
//
// NormalForm's and RoundingMode's reason, which restart.go states: each is
// declared in a std module and named from user packages, so no generated
// package can own the type. internal/irbuild/stdenum.go anchors them.
//
// # `Backoff`'s shape
//
// `Backoff.Exponential` is a struct-shaped variant of two named fields
// carrying defaults. internal/irbuild has a struct-fields payload form that
// carries per-field Go defaults. The defaults are stated in Go here and in
// Nomi in std/supervisors.nomi, which is two encodings of one fact, held equal
// by TestBackoffDefaultsMatchTheStdDeclaration, exactly as
// TestStdEnumTagsMatchRT holds the tag numbering.

// Backoff is `std/supervisors.Backoff`: when a stopped task runs again and how
// many times, which is a separate question from `Restart`'s whether.
//
// A flat struct rather than a sum with a payload pointer, for Failure's reason:
// only one variant is live at a time, so the fields of the others are dead
// weight of two machine words rather than an indirection on every read.
type Backoff struct {
	// Tag is 1 for Exponential, std/supervisors.nomi's one variant. 0 means
	// never constructed, which the runtime reads as the built-in schedule —
	// see backoffOrDefault.
	Tag uint8
	// MaxRestarts and MaxElapsed are `Exponential`'s two fields.
	//
	// `max_restarts` counts restarts rather than runs, which std states and
	// which the loop in supervisor.go honours: `max_restarts: 3` runs the body
	// at most four times.
	MaxRestarts int64
	MaxElapsed  Duration
}

// TagExponential is Backoff's tag for `Exponential`.
const TagExponential uint8 = 1

// The built-in schedule's two bounds: ten restarts or fifteen minutes,
// whichever comes first.
//
// These are the second encoding of `Backoff.Exponential`'s declared field
// defaults — std/supervisors.nomi carries the first, and a caller meets it
// there. Two encodings because rt's own callers and a Go embedder can hand in
// a never-constructed Backoff, which never went through the declaration; held
// equal by TestBackoffDefaultsMatchTheStdDeclaration, which reads the std
// source rather than restating the numbers.
const (
	BackoffDefaultMaxRestarts int64    = 10
	BackoffDefaultMaxElapsed  Duration = Duration(15 * time.Minute)

	// The delay curve, deliberately not configurable: start at a second,
	// double, level off at a minute. std/supervisors.nomi says why — a caller
	// thinks in "how many times" and "for how long".
	backoffFrom = time.Second
	backoffCap  = time.Minute
)

// GiveUp is `std/supervisors.GiveUp`: what happens at the moment the runtime
// decides a task will not run again.
type GiveUp struct {
	// Tag is 1 for Report and 2 for Exit.
	Tag uint8
}

// Tag values for GiveUp, in declaration order.
const (
	TagReport uint8 = 1
	TagExit   uint8 = 2
)

// Wait is `std/supervisors.Wait`: how long `Supervisor.flush` is willing to
// block.
//
// A union rather than a Duration with a magic "forever", which std states as a
// design decision: an unbounded wait is a different thing from a long one.
type Wait struct {
	// Tag is 1 for Forever and 2 for UpTo.
	Tag uint8
	// UpTo is the bound, meaningful only at Tag 2.
	UpTo Duration
}

// Tag values for Wait, in declaration order.
const (
	TagForever uint8 = 1
	TagUpTo    uint8 = 2
)

// FlushOutcome is `std/supervisors.FlushOutcome`: how a `Supervisor.flush`
// ended.
type FlushOutcome struct {
	// Tag is 1 for Flushed and 2 for TimedOut.
	Tag uint8
}

// Tag values for FlushOutcome, in declaration order.
const (
	TagFlushed  uint8 = 1
	TagTimedOut uint8 = 2
)

// backoffOrDefault reads a schedule, treating a never-constructed Backoff as
// the built-in one.
//
// The zero value cannot arrive from lowered Nomi — a Backoff is materialized
// only through a construction site, and `Supervisor.new`'s declared default
// fills both fields — so this covers rt's own callers and a Go embedder.
func backoffOrDefault(b Backoff) Backoff {
	if b.Tag != 0 {
		return b
	}
	return Backoff{
		Tag:         TagExponential,
		MaxRestarts: BackoffDefaultMaxRestarts,
		MaxElapsed:  BackoffDefaultMaxElapsed,
	}
}

// restartsOnReturn reports whether a task that returned on its own counts as a
// failure. Only `Permanent` says yes; everywhere else returning is how work
// finishes.
func restartsOnReturn(r Restart) bool { return r.Tag == TagPermanent }

// restartsAtAll reports whether any ending puts the task back to work.
func restartsAtAll(r Restart) bool { return r.Tag != TagTemporary && r.Tag != 0 }
