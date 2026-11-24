package analysis_test

import "testing"

// A std body's operator on a type with no impl of the operator's interface
// is an error in std's own check, as the same body is in a program: a
// program's check leaves std's demands to std's, and std's checked none
// but a derive's, so `a <= b` on std/instant's Instant passed the front end
// and the builder declined the body.
func TestStdOperatorWithoutTheImplIsRefused(t *testing.T) {
	src := `pub struct Stamp
  n: Int
end

pub fn earlier(a: Stamp, b: Stamp) -> Bool
  a <= b
end
`
	errs := buildStdFileForDerive(t, "stampgap", src)
	msg := "no impl of `Comparable` for `Stamp` (required via ordering operator `<=`)"
	e, ok := deriveDiag(errs, msg)
	if !ok {
		t.Fatalf("a std body's `<=` on a type with no Comparable is admitted: want %q, got:\n  %s", msg, errMsgs(errs))
	}
	if e.File != "std/stampgap.nomi" || e.Line != 6 || e.Col != 5 {
		t.Errorf("at %s:%d:%d, want std/stampgap.nomi:6:5 (the operator)", e.File, e.Line, e.Col)
	}
	// Std may derive or implement its own type's interface, so the help is
	// the one a program gets for its own type.
	help := "add `derive Comparable` to `Stamp` or write an `impl Comparable for Stamp` block"
	if len(e.Hints) != 1 || e.Hints[0] != help {
		t.Errorf("help = %q, want %q", e.Hints, help)
	}
}

// The mirror: with the impl, the std body is accepted.
func TestStdOperatorWithTheImplIsAccepted(t *testing.T) {
	src := `pub struct Stamp
  n: Int
end

derive Comparable for Stamp

pub fn earlier(a: Stamp, b: Stamp) -> Bool
  a <= b
end
`
	if errs := buildStdFileForDerive(t, "stampok", src); len(errs) > 0 {
		t.Fatalf("a std body's `<=` on a type with Comparable is refused:\n  %s", errMsgs(errs))
	}
}
