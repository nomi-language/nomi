package irbuild

import (
	"strings"
	"testing"
)

// Test declarations and assertions, pinned against `nomi test`.
//
// vmReference dispatches on whether the file declares tests, so every fixture
// here is run as `nomi test <file>` and never as a program run. That dispatch
// is the whole reason these checks mean anything — a test file run as
// `nomi run` would be a different program.
//
// Each test also pins the reference TEXT. A comparison passes when both sides
// silently produce nothing, so agreement alone is not evidence; the expected
// bytes are written out.

// TestTests_LiteralOperandsAreSuppressed pins the one rule that decides whether
// an operand row appears at all: an operand that reads exactly like its value
// explains nothing, so `assert double(3) == 7` shows `double(3) = 6` and not
// `7 = 7`. It is rt.RecordOperand's rule, applied at run time — the builder
// does not pre-decide it, which is why a Nomi literal and a Nomi
// expression that happens to render identically are treated the same way.
//
// A PREDICATE's literal arguments are the exception, and tests_call_boundary
// pins it: `holds?(3, 4)` does show `3 = 3`, because for a Bool-returning call
// the literal inputs are the interesting part.
func TestTests_LiteralOperandsAreSuppressed(t *testing.T) {
	got := vmReference(fixture("tests_fail.nomi"))
	if strings.Contains(got.stdout, "      7\n") {
		t.Fatalf("a literal operand that reads like its value was shown:\n%s", got.stdout)
	}
}

// --- groups, nesting, and attached tests -----------------------------------
