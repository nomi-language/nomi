package rt

// Restart is Nomi's `std/supervisors.Restart`: whether a stopped supervised task
// runs again. A disposition, not a schedule — `Backoff` is the schedule and is
// deliberately NOT here; see below.
//
// Here for NormalForm's and RoundingMode's reason — declared in a std module,
// named from user packages, so no generated package can own it. See
// internal/irbuild/stdenum.go.
//
// THE SUPERVISOR IS NOW HERE, AND THIS PARAGRAPH USED TO SAY IT WAS NOT.
// Corrected in place rather than deleted, because the SHAPE of the correction
// is the useful part: what this file said was "the row buys that the
// disposition is NAMEABLE, and anyone reading it as evidence that supervision
// is partially implemented is reading it wrong — the same trap RoundingMode's
// header names". That was true and it was the right warning; `Supervisor` had
// no representation and `supervisors.Supervisor.new` refused on its own terms.
// See rt/supervisor.go, which is that representation and its runtime.
//
// `Backoff` IS NOW A ROW. `Backoff.Exponential` is a STRUCT-SHAPED variant
// with two named fields carrying DEFAULTS, which took a fifth payload form,
// `payloadStructFields`, plus a defaulted-field rule. The defaulted-field
// decision was taken and NOT by weakening the existing rule — the defaults are
// stated in Go in rt/supervisorpolicy.go and held against std's declaration by
// a test, so no gen is asked to lower another module's Nomi source.
//
// WHAT IS STILL ABSENT, so this file is not read the wrong way a second time:
// `Supervisor.spawn` and `Supervisor.spawn_all` have no internal/irbuild call
// arm. Their
// rt implementations exist and are tested; what is missing is the arm that
// lowers a CALLBACK argument, beside concurrent.go's `taskSpawnCall`. No corpus
// file that uses a supervisor compiles yet.
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
