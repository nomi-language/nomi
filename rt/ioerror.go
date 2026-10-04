package rt

// IOError is Nomi's `std/io.IOError`: which filesystem failure `read_file` and
// `write_file` report.
//
// Here for the reason every shared-def family is here: the type is declared in a
// std module and named from user modules, so no one module can own it.
// See internal/irbuild/stdenum.go.
//
// THE FIRST STRUCT-SHAPED PAYLOAD in that family, and the shape is what makes it
// so: std declares `NotFound {path: String}` and `Other {reason: String}`, which
// a pattern binds BY NAME (`IOError.NotFound{path} -> …`) rather than by
// position. Every earlier carrying row — calendar.Error's five, PathSegment's
// two, Json's six — is positional.
//
// TWO FIELDS RATHER THAN ONE SHARED `Msg`, unlike rt.CalendarError, whose five
// String variants share a slot. Sharing would be sound (only one variant is live
// at a time) and it is refused here because a struct-shaped payload's field name
// is part of the DECLARATION this type is anchored against: the spec states
// `path` and `reason`, and giving them one Go field would put two Nomi names on
// one slot for no gain in a type this small.
//
// THE OPERATIONS ARE NOT HERE, and that is the whole architecture of this
// binding. Real filesystem effect stays out of rt, so
// `nomi/stdio` holds `ReadFile`/`WriteFile`, and rt links no filesystem code. What rt owns is the VALUE, which is data and carries no
// effect at all.
type IOError struct {
	// Tag is 1 for NotFound and 2 for Other, in std/io.nomi's declaration
	// order. 0 means never constructed.
	Tag uint8
	// Path is `NotFound`'s payload: the path that was not there.
	Path string
	// Reason is `Other`'s payload: the underlying failure's own text.
	Reason string
}

// Tag values for IOError, in std/io.nomi's declaration order. Two independently
// written encodings of one fact, held equal by TestStdEnumTagsMatchRT; see
// rt/roundingmode.go.
const (
	TagNotFound uint8 = 1
	TagIOOther  uint8 = 2
)
