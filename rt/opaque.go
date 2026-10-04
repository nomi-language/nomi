package rt

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The Go types behind `std`'s opaque scalar newtypes, and the externs over them.
//
// # What an `opaque type X Int` is, and what it is not
//
// std declares `pub opaque type Duration Int` and `pub opaque type Instant Int`.
// Neither is a `host type`: there is no handle, no descriptor and nothing the
// runtime holds on the Nomi side's behalf. A Duration IS a count of nanoseconds
// and an Instant IS a count of nanoseconds since the Unix epoch. `opaque` is a
// USE-SITE rule — user code outside the declaring file cannot write
// `Duration(n)` or destructure one — enforced entirely by the front end, plus
// one rendering rule (`<opaque T>` for a type with no explicit Debug) that the
// analyzer synthesizes as ordinary Nomi source.
//
// So the representation is a Go DEFINED type over int64, which is what a Nomi
// distinct type lowers to everywhere else (internal/irbuild's distinctDecl).
//
// # Why these are declared HERE
//
// Two reasons, and the second is the one that forces it:
//
//   - A named type's identity in internal/irbuild is the *typeDef its
//     declaration produced, and a std type needs one Go type every module's
//     code can name. rt is that one place. Same answer prelude.go gives for
//     `rt.Maybe[T]`.
//   - The externs below have Duration and Instant IN THEIR SIGNATURES, and the
//     host binding tables derive every signature from the Go function value by
//     reflection, so the type has to be a Go type rt can write down.
//
// This applies to a STDLIB opaque type only. rt knows nothing about user
// declarations and must not: a user type in here would be a user declaration
// inside the runtime library.
//
// # The Go zero value is a REAL value, deliberately
//
// rt.Maybe reserves tag 0 so a Go zero value is detectably invalid. That is the
// opposite of the decision here, and the difference is not taste: tag 0 is not a
// legal Nomi tag, so an enum had a spare state to reserve. int64 has none —
// every one of its values is a legal Duration, and `Duration.nanoseconds(0)` is
// an ordinary Nomi value that a program writes. `Duration(0)` IS that value.
//
// Making a zero value detectable would mean `struct { ns int64; set bool }`,
// doubling a value that rides every deadline computation, to detect a state no
// lowered program can reach: one of these is materialized only through a
// constructor call.

// Duration is `std/duration.Duration`: a signed span in nanoseconds.
//
// Nominally its own type, structurally int64 — so Go refuses to assign one to an
// int64, or to an Instant, without a conversion, which is exactly the rule the
// analyzer enforces for the Nomi types. Two Durations compare with integer
// equality; there is no interning and no handle table, so two different spans
// cannot compare equal.
type Duration int64

// Instant is `std/instant.Instant`: an absolute point in time, as nanoseconds
// since the Unix epoch.
type Instant int64

// --- std/duration ----------------------------------------------------------

// DurationToString is `Duration.to_string`, and through the module's
// `impl Debug for Duration` it is also how a Duration inspects.
//
// The compact form std/duration's Display documents: sign, then every non-zero
// unit component largest-to-smallest concatenated, or "0s" for zero. 0 -> "0s",
// 5e9 -> "5s", 1.5e9 -> "1s500ms", 5.4e12 -> "1h30m", -5e9 -> "-5s".
func DurationToString(d Duration) string {
	nanos := int64(d)
	if nanos == 0 {
		return "0s"
	}
	var b strings.Builder
	if nanos < 0 {
		b.WriteByte('-')
		nanos = -nanos
	}
	for _, u := range durationUnits {
		if nanos >= u.factor {
			fmt.Fprintf(&b, "%d%s", nanos/u.factor, u.suffix)
			nanos %= u.factor
		}
	}
	return b.String()
}

// durationUnits is the largest-to-smallest unit ladder DurationToString walks.
// The order IS the format: reordering it changes Nomi-observable output.
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
// monotonic reading would answer a different question.
func InstantNow() Instant { return Instant(time.Now().UnixNano()) }

// --- std/timer -------------------------------------------------------------

// TimerSleep is `timer.sleep`: suspend for d, or until the frame's context ends.
//
// It takes the frame because the wait is a cancellation point: std/timer
// documents that "cancellation of that block's context ends the sleep early
// without producing a user-visible error", and the frame is where the context
// that cancellation travels on is kept.
//
// A CANCELLED SLEEP UNWINDS THE TASK, which unwinds the enclosing block.
// Returning Unit instead would let a cancelled task RUN ON past its sleep:
// `Task.spawn(|| { timer.sleep(Duration.hours(1)); io.print("done") })` would
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
// The timer handling, including the drain-on-stop below, is ONE implementation
// rather than two that agree today.
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
