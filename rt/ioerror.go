package rt

// IOError is Nomi's `std/io.Error`: which failure an input or output
// operation reports, for files, the standard streams, sockets and the
// streams an adapter decodes (std::compress's readers).
//
// Here for the reason every shared-def family is here: the type is declared in a
// std module and named from user modules, so no one module can own it.
// See internal/irbuild/stdenum.go.
//
// Its payloads are struct-shaped: a pattern binds them by name
// (`io.Error.NotFound{path} -> …`). Variants that declare a field of one name
// share its slot, as stdenum.go's dedup rule allocates them: every `path` is
// Path, `InvalidPath` and `Corrupt` share Reason, `Unsupported` and `Other`
// share Operation.
//
// The operations are not here. Real filesystem effect stays out of rt, so
// internal/stdio holds std/fs's crossings, and rt links no filesystem code.
// What rt owns is the value, which is data and carries no effect at all,
// and the one table that sorts a Go error into it (IOErrorOf), which every
// stream reaches.
type IOError struct {
	// Tag is 1..19 in std/io.nomi's declaration order (the TagIO constants).
	// 0 means never constructed.
	Tag uint8
	// Path is the path the program passed, for every variant that names one.
	Path string
	// Reason is `InvalidPath`'s and `Corrupt`'s: what is wrong with the
	// path or the data.
	Reason string
	// Offset is `InvalidText`'s: the byte offset of the first sequence that
	// is not UTF-8. An Int of any size, since a program's own Reader may
	// report one.
	Offset Int
	// Operation is `Unsupported`'s and `Other`'s: the operation that failed
	// (`process.run`, `open`).
	Operation string
	// Target is `Unsupported`'s: the target that cannot do the operation
	// (`js/wasm`).
	Target string
	// Code is `Other`'s: the operating system's error code (`EIO`).
	Code string
}

// Tag values for IOError, in std/io.nomi's declaration order. Two independently
// written encodings of one fact, held equal by TestStdEnumTagsMatchRT; see
// rt/roundingmode.go.
const (
	TagIONotFound uint8 = iota + 1
	TagIOAlreadyExists
	TagIOPermissionDenied
	TagIONotADirectory
	TagIOIsADirectory
	TagIODirectoryNotEmpty
	TagIOInvalidPath
	TagIOInvalidText
	TagIOCorrupt
	TagIOTruncated
	TagIONoSpace
	TagIOReadOnly
	TagIOTooManyOpen
	TagIOClosed
	TagIOBrokenPipe
	TagIOReset
	TagIOTimedOut
	TagIOUnsupported
	TagIOOther
)
