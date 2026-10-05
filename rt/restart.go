package rt

// Restart is Nomi's `std/supervisors.Restart`: whether a stopped supervised task
// runs again. A disposition, not a schedule — `Backoff` is the schedule and is
// deliberately not here; see below.
//
// Here for NormalForm's and RoundingMode's reason — declared in a std module,
// named from user packages, so no generated package can own it. See
// internal/irbuild/stdenum.go.
//
// The supervisor that reads this value is rt/supervisor.go.
//
// `Backoff` lives in rt/supervisorpolicy.go. `Backoff.Exponential` is a
// struct-shaped variant with two named fields carrying defaults, which needs
// the `payloadStructFields` payload form plus a defaulted-field rule. The
// defaults are stated in Go in rt/supervisorpolicy.go and held against std's
// declaration by a test, so no gen is asked to lower another module's Nomi
// source.
type Restart struct {
	// Tag is 1..3 in std/supervisors.nomi's declaration order. 0 means never
	// constructed.
	Tag uint8
}

// Tag values for Restart, in std/supervisors.nomi's declaration order. Two
// independently-written encodings of one fact, held equal by
// TestStdEnumTagsMatchRT; see rt/roundingmode.go.
const (
	TagTemporary uint8 = 1
	TagTransient uint8 = 2
	TagPermanent uint8 = 3
)
