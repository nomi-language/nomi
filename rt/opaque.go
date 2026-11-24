package rt

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// The Go types behind `std`'s opaque scalar newtypes, and the externs over them.
//
// # What an `opaque type X Int` is, and what it is not
//
// std declares `pub opaque type Duration Int` and `pub opaque type Instant Int`.
// Neither is a `host type`: there is no handle, no descriptor and nothing the
// runtime holds on the Nomi side's behalf. A Duration is a count of nanoseconds
// and an Instant is a count of nanoseconds since the Unix epoch. `opaque` is a
// use-site rule — user code outside the declaring file cannot write
// `Duration(n)` or destructure one — enforced entirely by the front end, plus
// one rendering rule (`<opaque T>` for a type with no explicit Debug) that the
// analyzer synthesizes as ordinary Nomi source.
//
// So the representation is a Go defined type over int64, which is what a Nomi
// distinct type lowers to everywhere else (internal/irbuild's distinctDecl).
//
// # Why these are declared here
//
// Two reasons, and the second is the one that forces it:
//
//   - A named type's identity in internal/irbuild is the *typeDef its
//     declaration produced, and a std type needs one Go type every module's
//     code can name. rt is that one place. Same answer prelude.go gives for
//     `rt.Option[T]`.
//   - The externs below have Duration and Instant in their signatures, and the
//     host binding tables derive every signature from the Go function value by
//     reflection, so the type has to be a Go type rt can write down.
//
// This applies to a stdlib opaque type only. rt knows nothing about user
// declarations and must not: a user type in here would be a user declaration
// inside the runtime library.
//
// # The Go zero value is a real value, deliberately
//
// rt.Option reserves tag 0 so a Go zero value is detectably invalid. That is the
// opposite of the decision here, and the difference is not taste: tag 0 is not a
// legal Nomi tag, so an enum had a spare state to reserve. int64 has none —
// every one of its values is a legal Duration, and `Duration.nanoseconds(0)` is
// an ordinary Nomi value that a program writes. `Duration(0)` is that value.
//
// Making a zero value detectable would mean `struct { ns int64; set bool }`,
// doubling a value that rides every deadline computation, to detect a state no
// lowered program can reach: one of these is materialized only through a
// constructor call.

// Duration is `std/duration.Duration` as Go code takes it: a signed span in
// nanoseconds. The Nomi value is an Int of any size; a host function that
// takes a Duration gets it saturated at int64 (hostadapt.DurationNanos), a
// wait no clock outlasts, and one that has to keep every length takes the
// nanoseconds as an Int (DurationNanosToString).
//
// Nominally its own type, structurally int64 — so Go refuses to assign one to an
// int64, or to an Instant, without a conversion, which is exactly the rule the
// analyzer enforces for the Nomi types. Two Durations compare with integer
// equality; there is no interning and no handle table, so two different spans
// cannot compare equal.
type Duration int64

// Instant is `std/instant.Instant` as Go code takes it: an absolute point in
// time, as nanoseconds since the Unix epoch in 64 bits, the years 1677 to
// 2262. The Nomi value is an Int of any size. A host function that takes an
// Instant gets it clamped to int64 (hostadapt.InstantNanosClamped), which for
// a deadline is one that never comes or has passed; one that has to keep
// every moment takes the nanoseconds as an Int and reads them with
// InstantTimeOf.
type Instant int64

// --- std/duration ----------------------------------------------------------

// DurationToString is DurationNanosToString for a Duration Go code holds,
// such as a supervisor's shutdown timeout in a trap message.
func DurationToString(d Duration) string {
	return DurationNanosToString(IntOf(int64(d)))
}

// DurationNanosToString is std/duration's `render_nanos`, the body of
// `Duration.to_string`, and through the module's `impl Debug for Duration`
// it is also how a Duration inspects. It takes the nanoseconds as an Int
// because a Duration is an Int count of any size.
//
// The compact form std/duration's Display documents: sign, then every non-zero
// unit component largest-to-smallest concatenated, or "0s" for zero. 0 -> "0s",
// 5e9 -> "5s", 1.5e9 -> "1s500ms", 5.4e12 -> "1h30m", -5e9 -> "-5s". The hours
// have no bound, so a length past int64 is split there: the hours are an Int,
// and what is left is under an hour.
func DurationNanosToString(ns Int) string {
	if ns.Sign() == 0 {
		return "0s"
	}
	var b strings.Builder
	if ns.Sign() < 0 {
		b.WriteByte('-')
	}
	var nanos uint64
	if n, ok := ns.Int64(); ok {
		// The magnitude as a uint64: negating the most negative int64
		// overflows, and its magnitude fits a uint64.
		nanos = uint64(n)
		if n < 0 {
			nanos = -nanos
		}
	} else {
		mag := new(big.Int).Abs(ns.Big())
		hours, rest := new(big.Int).QuoRem(mag, big.NewInt(int64(time.Hour)), new(big.Int))
		fmt.Fprintf(&b, "%sh", hours)
		nanos = rest.Uint64()
	}
	for _, u := range durationUnits {
		if factor := uint64(u.factor); nanos >= factor {
			fmt.Fprintf(&b, "%d%s", nanos/factor, u.suffix)
			nanos %= factor
		}
	}
	return b.String()
}

// durationUnits is the largest-to-smallest unit ladder DurationToString walks.
// The order is the format: reordering it changes Nomi-observable output.
var durationUnits = [...]struct {
	factor int64
	suffix string
}{
	{int64(time.Hour), "h"},
	{int64(time.Minute), "m"},
	{int64(time.Second), "s"},
	{int64(time.Millisecond), "ms"},
	{int64(time.Microsecond), "µs"},
	{1, "ns"},
}

// --- std/instant -----------------------------------------------------------

// InstantNow is `Instant.now`: the wall clock, as nanoseconds since the epoch.
//
// Deliberately not monotonic. std/instant documents an Instant as an absolute
// point on the wall clock with `Instant.to_seconds` giving Unix seconds, so a
// monotonic reading would answer a different question; MonotonicNow answers
// that one.
func InstantNow() Instant { return Instant(time.Now().UnixNano()) }

// Monotonic is `std/instant.Monotonic` as Go code takes it: nanoseconds
// on the monotonic clock since monotonicOrigin, a reading taken when the
// process started.
type Monotonic int64

// monotonicOrigin is the reading every Monotonic counts from. A time.Now
// carries the system's monotonic clock beside the wall clock, and Sub
// between two times that both carry it reads that clock alone, so setting
// the wall clock moves no Monotonic.
var monotonicOrigin = time.Now()

// MonotonicNow is `Monotonic.now`: the nanoseconds since monotonicOrigin
// on the monotonic clock.
//
// Under `clock Clock.Virtual` the test runs in a testing/synctest bubble
// (rt/vclock), where time.Now reads the bubble's clock and carries no
// monotonic reading, so Sub falls back to the wall readings: the bubble's
// clock minus the origin's. The bubble's clock moves only as its timers
// fire and is never set, so the difference of two readings is the virtual
// time between them, as `timer.sleep` counts it.
func MonotonicNow() Monotonic { return Monotonic(time.Since(monotonicOrigin)) }

// InstantFromTime is the nanoseconds from the epoch of a Go time, the Int a
// Nomi Instant is: exact for every time Go represents, from Unix seconds,
// which do not wrap, never UnixNano, which wraps outside the years 1677 to
// 2262. Every place a Go time becomes an Instant goes through it.
func InstantFromTime(t time.Time) Int {
	sec, nsec := t.Unix(), int64(t.Nanosecond())
	if sec > -9_000_000_000 && sec < 9_000_000_000 {
		return SmallInt(sec*int64(time.Second) + nsec)
	}
	z := new(big.Int).Mul(big.NewInt(sec), big.NewInt(int64(time.Second)))
	return BigIntOf(z.Add(z, big.NewInt(nsec)))
}

// InstantTimeOf is the Go time ns nanoseconds from the epoch, in UTC: exact
// while the seconds fit in 62 bits, about 146 billion years either way, and
// clamped to that past it, where time.Unix's own arithmetic would wrap. A
// Nomi Instant is an Int of any size, and this is where one becomes a Go
// time that keeps its moment (`http.format_time`, `mail.rfc5322_date`, a Go
// binding's time.Time).
func InstantTimeOf(ns Int) time.Time {
	const limit = int64(1) << 62
	billion := big.NewInt(int64(time.Second))
	sec, nsec := new(big.Int).DivMod(ns.Big(), billion, new(big.Int))
	switch {
	case sec.Cmp(big.NewInt(limit)) > 0:
		return time.Unix(limit, 0).UTC()
	case sec.Cmp(big.NewInt(-limit)) < 0:
		return time.Unix(-limit, 0).UTC()
	}
	return time.Unix(sec.Int64(), nsec.Int64()).UTC()
}

// --- std/timer -------------------------------------------------------------

// TimerSleep is `timer.sleep`: suspend for d, or until the frame's context ends.
//
// It takes the frame because the wait is a cancellation point: std/timer
// documents that "cancellation of that block's context ends the sleep early
// without producing a user-visible error", and the frame is where the context
// that cancellation travels on is kept.
//
// A cancelled sleep unwinds the task, which unwinds the enclosing block.
// Returning Unit instead would let a cancelled task run on past its sleep:
// `Task.spawn(fn -> { timer.sleep(Duration.hours(1)); io.print("done") })` would
// print, and `Task.outcome` would report `Completed` where std says
// `Cancelled`. `16-concurrency/supervisors/supervisors_test.nomi`
// asserts exactly the second of those.
//
// The raise's own guard is in concurrent.go: a frame with no task to unwind to
// gets a named fault rather than a Go traceback. See raiseCanceled.
func TimerSleep(fr *Frame, d Duration) Unit {
	if !SleepCtx(fr.Context(), d) {
		CancelIfDone(fr)
	}
	return Unit{}
}

// SleepCtx waits out d and reports whether it completed rather than being
// cancelled.
//
// Exported for callers outside rt that have to tell the two outcomes apart.
// The timer handling, including the drain-on-stop below, is one implementation
// rather than two that happen to agree.
func SleepCtx(ctx context.Context, d Duration) bool {
	timer := time.NewTimer(time.Duration(d))
	defer func() {
		// Drain-on-stop. On Go 1.26 timer.C may carry a pending tick if the
		// timer expired around the same instant as ctx.Done(); from 1.27 Stop
		// is synchronous and the drain is unconditional cleanup. Safe on both.
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
