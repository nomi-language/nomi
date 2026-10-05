//go:build !(js && wasm)

// Package stdio is std/io's filesystem host implementation, and it is
// deliberately not in rt.
//
// `io.read_file` and `io.write_file` do not go into rt: rt is linked by
// everything that runs Nomi, so an rt implementation would entrench in the
// runtime library exactly the thing that ought to move out of the language.
// internal/stdlibbindings binds ReadFile and WriteFile as `RtFuncs` rows.
//
//   - rt makes no filesystem call. It holds the IOError value (rt/ioerror.go),
//     which is data. TestRuntimeImportsOnlyItsAllowlist
//     and TestRuntimeArtifactLinksNoFrontEnd hold.
//   - This package imports `os`, `errors`, `io/fs` and rt and nothing else, so
//     it is a leaf.
//
// # The Go error string still reaches Nomi-observable output
//
// `Other{reason}` carries `err.Error()`, which is `os`/`io/fs` text. Having one
// implementation makes that text consistent, and it does not make the text
// Nomi's. The real fix is a `std` API change: `IOError` would have to
// name the failures it means (`PermissionDenied`, `IsADirectory`, …) instead of
// having an `Other` variant whose payload is a foreign runtime's prose. Wording
// the Go string differently would only move the leak. This is the file that
// would have to change.
package stdio

import (
	"errors"
	"io/fs"
	"os"

	"github.com/nomi-language/nomi/rt"
)

// ReadFile is std/io's `read_file`: the whole contents of path, or the
// filesystem failure that stopped it.
func ReadFile(path string) rt.Result[string, rt.IOError] {
	data, err := os.ReadFile(path)
	if err != nil {
		return rt.Err[string, rt.IOError](readError(path, err))
	}
	return rt.Ok[string, rt.IOError](string(data))
}

// WriteFile is std/io's `write_file`: content into path, replacing whatever was
// there.
//
// 0o644 is the mode. It is not a parameter because std does not declare one.
//
// EVERY failure is `Other`, INCLUDING absence — which is the asymmetry with
// ReadFile above, and it is deliberate rather than an oversight. A write to a path whose parent directory does not exist returns
// ENOENT, so `errors.Is(err, fs.ErrNotExist)` is TRUE for it, and classifying
// that as `NotFound{path}` would be a wrong answer twice over: the path in hand
// is the file, not the missing directory, and std/io.nomi documents absence for
// READS as the expected-first-run case ("callers can decide whether to treat
// absence as expected") while documenting writes the other way — "The parent
// directory must exist; missing parents do not get auto-created."
//
// So the two classifiers are separate FUNCTIONS rather than one with a flag.
// Sharing one and passing `allowNotFound` would have made the asymmetry a
// parameter a caller could get wrong; two named functions make it a fact about
// each operation. MEASURED: a draft that routed both through one classifier
// changed the answer for a write into a missing directory.
func WriteFile(path, content string) rt.Result[rt.Unit, rt.IOError] {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return rt.Err[rt.Unit, rt.IOError](writeError(err))
	}
	return rt.Ok[rt.Unit, rt.IOError](rt.Unit{})
}

// readError classifies a READ failure into the IOError variant std declares for
// it.
//
// The CLASSIFICATION is the observable rule and it lives here once: absence is
// `NotFound{path}` — carrying the path the caller passed, not the Go error's
// rendering of it — and everything else is `Other{reason}`. std/io.nomi states
// the intent this implements: "NotFound carries the path so callers can decide
// whether to treat absence as expected (e.g., first run of an app that creates
// its data file on demand)."
//
// `errors.Is(err, fs.ErrNotExist)` rather than a string test, because that is
// the only spelling that survives os wrapping its errors in a *PathError and is
// portable across platforms whose message text differs.
func readError(path string, err error) rt.IOError {
	if errors.Is(err, fs.ErrNotExist) {
		return rt.IOError{Tag: rt.TagNotFound, Path: path}
	}
	return writeError(err)
}

// writeError is the unconditional `Other{reason}` form, which is also read's
// fallback. One function so `Other`'s payload has one encoding.
func writeError(err error) rt.IOError {
	return rt.IOError{Tag: rt.TagIOOther, Reason: err.Error()}
}
