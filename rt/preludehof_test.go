package rt

import "testing"

// ResultMapErr, the higher-order prelude driver rt keeps.
//
// What a program cannot reach is the never-constructed value: Nomi has no zero
// values, so no Nomi program can produce a `Result[T, E]` with `TagInvalid`,
// and the property that a Go zero value stays detectably unconstructed rather
// than being promoted to a legitimate variant has to be asserted absolutely.
// That is what this file is for, and it is the reason the driver copies the tag
// instead of naming a variant.

// TestHofAbsentArmPreservesTheTag: writing a variant on the fall-through would
// agree with std on every input a Nomi program can build, and would also
// silently convert a never-constructed value into a legitimate one.
func TestHofAbsentArmPreservesTheTag(t *testing.T) {
	text := func(fr *Frame, e string) string { return e + "!" }

	var noResult Result[int64, string]
	if got := ResultMapErr(nil, noResult, text).Tag; got != TagInvalid {
		t.Fatalf("ResultMapErr over an unconstructed Result answered tag %d, want %d", got, TagInvalid)
	}
	// The complement, so the check above cannot pass by answering TagInvalid
	// for everything.
	if got := ResultMapErr(nil, Ok[int64, string](1), text).Tag; got != TagOk {
		t.Fatalf("ResultMapErr over an Ok answered tag %d, want %d", got, TagOk)
	}
}

// TestHofDoesNotCallTheCallbackOnTheAbsentArm is the property that a Go zero
// value makes possible to get wrong and impossible to see from the answer
// alone. map_err's absent arm is the Ok one, which is the mirror and the
// easiest to get backwards: a driver testing `TagErr` where it meant `TagOk`
// inverts both arms and still type-checks.
func TestHofDoesNotCallTheCallbackOnTheAbsentArm(t *testing.T) {
	calls := 0
	countErr := func(fr *Frame, e string) string { calls++; return e }

	ResultMapErr(nil, Ok[int64, string](1), countErr)
	if calls != 0 {
		t.Fatalf("the callback ran %d time(s) on an Ok; std's Nomi body is a `case` that never reaches it", calls)
	}
	ResultMapErr(nil, Err[int64, string]("e"), countErr)
	if calls != 1 {
		t.Fatalf("the callback ran %d time(s) on one Err, want 1 — the row above proves nothing if the present arm is also silent", calls)
	}
}

// TestHofCarriesTheUntouchedPayloadAcross pins the field the driver does not
// transform: `map_err` leaves `T` alone, and a driver that rebuilt the struct
// without copying it would answer `Ok(0)`.
func TestHofCarriesTheUntouchedPayloadAcross(t *testing.T) {
	shout := func(fr *Frame, e string) string { return e + "!" }

	kept := ResultMapErr(nil, Ok[int64, string](41), shout)
	if kept.Ok != 41 {
		t.Fatalf("ResultMapErr dropped the Ok payload: %d", kept.Ok)
	}
	retyped := ResultMapErr(nil, Ok[int64, string](41), func(fr *Frame, e string) bool { return true })
	if retyped.Ok != 41 {
		t.Fatalf("ResultMapErr dropped the Ok payload when the error type changed: %d", retyped.Ok)
	}
}
