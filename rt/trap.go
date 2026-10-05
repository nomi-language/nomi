package rt

import (
	"errors"
	"fmt"
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
}

func (e *Error) Error() string { return e.Msg }

// Trap aborts with a Nomi runtime fault.
//
// Not inlinable and not meant to be. Every caller is a cold path guarded by a
// check that the optimizer can see through, which is what keeps the checks
// themselves cheap.
func Trap(msg string) { panic(&Error{Msg: msg}) }

// NoCaseMatchError is the failure a `case` whose arms all missed reports.
//
// It lives here for the reason the package comment gives: an observable string
// has one owner.
//
// Exhaustiveness is the Nomi checker's job: it rejects a `case` whose
// unguarded arms leave some value of the scrutinee's type unmatched, so on
// checked code this is unreachable. Every lowered `case` still ends in a
// fall-through block that reports it.
func NoCaseMatchError(line int) error { return errors.New(noCaseMatchText(line)) }

func noCaseMatchText(line int) string {
	return fmt.Sprintf("line %d: no matching case branch", line)
}

// MapKeyMissingError is the failure a map destructuring reports when a key it
// names is absent.
//
// Here for the reason NoCaseMatchError is: an observable string has one owner.
//
// A map destructuring is refutable and this is its miss, which is what makes it
// a trap rather than an assertion failure — the sibling statement form
// `assert {"a" => v} = m` reports through the assertion channel instead, and
// nothing but the two call sites keeps the exits apart.
//
// `key` arrives already rendered, by Display and not by Debug, so a String key
// prints bare with no quotes. Taking the text rather than a value is what lets
// one function serve every caller whatever the key's Go type.
func MapKeyMissingError(line int, key string) error {
	return errors.New(mapKeyMissingText(line, key))
}

func mapKeyMissingText(line int, key string) string {
	return fmt.Sprintf("line %d: key %s not found in map", line, key)
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
	return errors.New(todoText(file, line, reason))
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
	return fmt.Errorf("line %d: variant '%s' has no field '%s'", line, variant, field)
}

// EnumFieldAccessError is EnumFieldMissingError's sibling for a variant whose
// payload is not a struct, or that has none.
func EnumFieldAccessError(line int, variant, field string) error {
	return fmt.Errorf("line %d: cannot access field '%s' on variant '%s'", line, field, variant)
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
func CallDepthError(line int) error { return errors.New(callDepthText(line)) }

func callDepthText(line int) string {
	return fmt.Sprintf("line %d: stack overflow: more than %d nested calls", line, MaxCallDepth)
}

// UnreachableError is the fault of a call the compiler proved no execution
// reaches: a bound dispatch on a type argument it filled itself because the
// call left it unconstrained. reason is the compiler's account of why.
// Arriving here means that proof was wrong.
func UnreachableError(line int, reason string) error {
	return fmt.Errorf("line %d: unreachable: %s", line, reason)
}
