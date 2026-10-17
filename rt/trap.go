package rt

import (
	"fmt"
	"strconv"
	"strings"
)

// Error is a Nomi runtime fault: integer overflow, a zero divisor, an operation
// with no definition for its operands.
//
// It travels as a panic rather than as a returned error. The alternative —
// `(T, error)` on every function — puts an error check between every
// pair of Nomi operations, and the two Go passes native representations exist to
// unlock, `inline` and `devirtualize`, are the two that give up first on that
// shape. A fault aborts the program, so the cost of a panic is paid once on a
// path that was about to exit.
//
// Msg is the complete text the user sees, already carrying its `line N:` prefix.
// Nothing wraps it and nothing prefixes it: `nomi run` prints it verbatim.
type Error struct {
	Msg string
	// Line is the line Msg's `line N:` prefix names, or 0 for a fault whose
	// text names no line (a host function's trap, a `todo`, which names its
	// file and line in its own words). A positioned constructor in this
	// package sets both, so the prefix and the field always agree.
	Line int
	// Awaited says the fault is a task's failure reaching the code that
	// awaited it (TrapAwaited), rather than one that started here. A
	// debugger stops for it under its own filter.
	Awaited bool
}

func (e *Error) Error() string { return e.Msg }

// positioned is the fault text, already opening with line's `line N:`
// prefix, carried with its line.
func positioned(line int, text string) *Error { return &Error{Msg: text, Line: line} }

// At is e reported at line instead: the same text after e's own `line N:`
// prefix (all of it when e names no line) behind line's prefix.
//
// A trap raised inside the standard library is reported at the program's
// own call into it, the line its author wrote, rather than at a line of
// std's source (internal/vm's Machine.fault decides where). The words after
// the line are the trap's, unchanged, so `Int.abs(Int.min_value)` reads as
// `-Int.min_value` does: `line N: integer overflow: 0 - -9223372036854775808`.
func (e *Error) At(line int) *Error {
	text := e.Msg
	if e.Line > 0 {
		text = strings.TrimPrefix(text, linePrefix(e.Line))
	}
	return &Error{Msg: linePrefix(line) + text, Line: line, Awaited: e.Awaited}
}

// linePrefix is the `line N: ` every positioned fault text opens with.
func linePrefix(line int) string { return "line " + strconv.Itoa(line) + ": " }

// Trap aborts with a Nomi runtime fault.
//
// Not inlinable and not meant to be. Every caller is a cold path guarded by a
// check that the optimizer can see through, which is what keeps the checks
// themselves cheap.
func Trap(msg string) { panic(&Error{Msg: msg}) }

// trapAt aborts with the positioned fault text for line, as Trap does.
func trapAt(line int, text string) { panic(positioned(line, text)) }

// TrapAwaited aborts the awaiter of a failed task with the task's fault.
func TrapAwaited(msg string) { panic(&Error{Msg: msg, Awaited: true}) }

// NoCaseMatchError is the failure a `case` whose arms all missed reports.
//
// It lives here for the reason the package comment gives: an observable string
// has one owner.
//
// Exhaustiveness is the Nomi checker's job: it rejects a `case` whose
// unguarded arms leave some value of the scrutinee's type unmatched, so on
// checked code this is unreachable. Every lowered `case` still ends in a
// fall-through block that reports it.
func NoCaseMatchError(line int) error { return positioned(line, noCaseMatchText(line)) }

func noCaseMatchText(line int) string {
	return fmt.Sprintf("line %d: no matching case branch", line)
}

// IndexOutOfRangeText is the trap a positional access whose caller promised
// the position exists (`Vector.at`, `Vector.set`, `Bytes.at`) raises when it
// does not: `index 7 is out of range for a Vector of length 5`. A negative
// index is out of range the same way. owner is the type the index is into.
func IndexOutOfRangeText(owner string, index, length int64) string {
	return fmt.Sprintf("index %d is out of range for a %s of length %d", index, owner, length)
}

// TodoError is the failure a `todo` reports when a program reaches it: the
// file and line of the `todo`, then the programmer's reason when there is one.
// file is the path the front end read the `todo` from, shown relative to the
// working directory when it is under it (DisplayPath).
//
// Unlike the other traps the text does not open with `line N:`: it names the
// file too, because a `todo` is a note to find and finish, and it is read
// outside the file being edited.
func TodoError(file string, line int, reason string) error {
	return &Error{Msg: todoText(file, line, reason)}
}

func todoText(file string, line int, reason string) string {
	s := fmt.Sprintf("todo reached at %s:%d", DisplayPath(file), line)
	if reason != "" {
		s += ": " + reason
	}
	return s
}

// EnumFieldMissingError is the failure a field read on an enum value reports
// when the value's variant carries a struct payload without that field. The
// checker types `s.f` from the first variant that declares f, so a value of
// another variant type-checks and has nowhere to read from. The VM's enum
// field read returns it.
func EnumFieldMissingError(line int, variant, field string) error {
	return positioned(line, fmt.Sprintf("line %d: variant '%s' has no field '%s'", line, variant, field))
}

// EnumFieldAccessError is EnumFieldMissingError's sibling for a variant whose
// payload is not a struct, or that has none.
func EnumFieldAccessError(line int, variant, field string) error {
	return positioned(line, fmt.Sprintf("line %d: cannot access field '%s' on variant '%s'", line, field, variant))
}

// MaxCallDepth is how many Nomi activations one goroutine may have in progress
// before the call that would start one more faults with CallDepthError.
//
// It exists because a Go stack overflow is not a panic. Past the goroutine's
// maximum stack (1 GB by default, so 512 MiB in practice, since stacks grow by
// doubling) the Go runtime prints "goroutine stack exceeds" and kills the
// process: nothing recovers it, so `nomi test` cannot fail one case and go on,
// a task cannot settle as Failed, and a test binary dies with it.
//
// The VM enforces this count. The number comes from the Go stack
// each activation uses on darwin/arm64: plain non-tail recursion takes about
// 1.9 KB per activation and overflows the default stack between 250,000 and
// 300,000 nested calls, and recursion through an `Iter.map` callback, which
// puts rt's iteration driver between every two activations, takes about
// 3.4 KB per activation. 100,000 keeps the heavier path at roughly 340 MB,
// under the 512 MiB ceiling with room for a longer chain of host frames per
// activation.
// It is a count and not a byte budget because Go exposes no cheap reading of
// the current goroutine's stack size, so a sufficiently long chain of Go
// frames between two activations can still overflow first.
//
// The browser build (GOOS=js GOARCH=wasm) uses a smaller number; see
// calldepth_js.go.
const MaxCallDepth = maxCallDepth

// CallDepthError is the fault the call that would exceed MaxCallDepth reports.
// line is that call's line.
func CallDepthError(line int) error { return positioned(line, callDepthText(line)) }

func callDepthText(line int) string {
	return fmt.Sprintf("line %d: stack overflow: more than %d nested calls", line, MaxCallDepth)
}

// UnreachableError is the fault of a call the compiler proved no execution
// reaches: a bound dispatch on a type argument it filled itself because the
// call left it unconstrained. reason is the compiler's account of why.
// Arriving here means that proof was wrong.
func UnreachableError(line int, reason string) error {
	return positioned(line, fmt.Sprintf("line %d: unreachable: %s", line, reason))
}
