package rt

import (
	"math"
	"testing"
	"time"
)

// A timeout counts elapsed time and a named deadline counts the wall clock
// (deadlineClock). A test cannot set the system clock, so these read a chain
// at hand-made readings of both clocks (deadlineBound.inForce), in which the
// wall clock has been set while the monotonic one ran on.

// stepped is a reading `ran` of monotonic time after `at`, with the wall clock
// set by `step` in between.
func stepped(at clockReading, ran, step time.Duration) clockReading {
	return clockReading{wall: at.wall + Instant(ran+step), mono: at.mono + Monotonic(ran)}
}

// made is the reading each chain here is made at.
var made = clockReading{wall: Instant(1_800_000_000 * time.Second), mono: Monotonic(50 * time.Second)}

func TestContextTimeout_ASetWallClockDoesNotMoveIt(t *testing.T) {
	c := contextWithTimeout(ContextRoot(), Duration(10*time.Second), made.mono)
	for _, step := range []time.Duration{-time.Hour, 0, time.Hour} {
		now := stepped(made, 3*time.Second, step)
		left, at := chainBound(c).inForce(now)
		if left != Duration(7*time.Second) {
			t.Errorf("wall clock set by %v: %v left, want 7s", step, time.Duration(left))
		}
		// The Instant it ends at is on the wall clock as it reads now.
		if want := now.wall + Instant(7*time.Second); at != want {
			t.Errorf("wall clock set by %v: ends at %d, want %d", step, at, want)
		}
	}
}

func TestContextDeadline_ASetWallClockMovesIt(t *testing.T) {
	named := made.wall + Instant(20*time.Second)
	c := ContextWithDeadline(ContextRoot(), named)
	cases := []struct {
		step time.Duration
		left time.Duration
	}{
		{-time.Hour, time.Hour + 17*time.Second},
		{0, 17 * time.Second},
		{time.Hour, 17*time.Second - time.Hour},
	}
	for _, tc := range cases {
		left, at := chainBound(c).inForce(stepped(made, 3*time.Second, tc.step))
		if left != Duration(tc.left) || at != named {
			t.Errorf("wall clock set by %v: %v left, ends at %d; want %v and %d",
				tc.step, time.Duration(left), at, tc.left, named)
		}
	}
}

// The floor keeps prev's earliest link on each clock, since which of them
// ends first is the clocks' to say: here the timeout, until the wall clock is
// set forward past the named deadline.
func TestContextWithFloor_KeepsTheEarliestLinkOnEachClock(t *testing.T) {
	named := made.wall + Instant(20*time.Second)
	prev := ContextWithDeadline(contextWithTimeout(ContextRoot(), Duration(10*time.Second), made.mono), named)
	floored := ContextWithFloor(prev, ContextWithDeadline(ContextRoot(), named+Instant(time.Hour)))

	left, _ := chainBound(floored).inForce(stepped(made, 3*time.Second, 0))
	if left != Duration(7*time.Second) {
		t.Errorf("wall clock not set: %v left, want the timeout's 7s", time.Duration(left))
	}
	left, at := chainBound(floored).inForce(stepped(made, 3*time.Second, time.Hour))
	if at != named || left >= 0 {
		t.Errorf("wall clock set an hour on: ends at %d with %v left, want the named %d, passed",
			at, time.Duration(left), named)
	}
}

// Each end of both ranges reads as itself: a timeout past the latest reading
// ends at the latest Instant, and a named Instant is reported as named even
// where the time left to it is past a Duration's range.
func TestContextDeadline_SaturatesAtTheEnds(t *testing.T) {
	never := contextWithTimeout(ContextRoot(), Duration(math.MaxInt64), made.mono)
	if left, at := chainBound(never).inForce(made); at != Instant(math.MaxInt64) || left <= 0 {
		t.Errorf("an unending timeout ends at %d with %v left", at, time.Duration(left))
	}
	spent := contextWithTimeout(ContextRoot(), Duration(math.MinInt64), made.mono)
	if left, _ := chainBound(spent).inForce(made); left >= 0 {
		t.Errorf("a timeout of the least Duration has %v left", time.Duration(left))
	}
	for _, named := range []Instant{math.MinInt64, math.MaxInt64} {
		c := ContextWithDeadline(ContextRoot(), named)
		if _, at := chainBound(c).inForce(made); at != named {
			t.Errorf("a deadline at %d is reported at %d", named, at)
		}
	}
}
