package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/std"
)

// The ERROR side of `assert` / `refute` / `testing.check` is std/assertions'
// `AssertionFailure`, and it is decided by std/testing's own declaration —
// `pub host fn check<T>(subject: T): Result<T, AssertionFailure>`, whose name
// resolves in std/testing's scope. The CALLING file's scope has no say in it.
//
// Resolving the name in the caller's scope goes wrong in two directions:
//
//   - A file declaring its own `AssertionFailure` would RETYPE check's error to
//     that declaration, so a field read on the Err payload type-checks against
//     the wrong struct and the program dies at run time. That is the second
//     case below, and it is the one that matters: a type error delivered as a
//     runtime fault is the failure mode a checker exists to prevent.
//   - A file that never mentions the name, which is every ordinary test file,
//     would get a placeholder type that unifies with nothing.
//
// The checker uses a canonical singleton instead, the TypeBool / TypeOrdering
// pattern applied to one more type; analysis cannot import std, so std.Load()
// writes it.

// TestAssertionFailureType_IsTheCanonicalDeclaration is the anti-vacuity half:
// without it the shadowing case could pass because nothing resolved at all.
func TestAssertionFailureType_IsTheCanonicalDeclaration(t *testing.T) {
	std.Load()
	st, isStruct := analysis.TypeAssertionFailure.(*analysis.StructType)
	if !isStruct {
		t.Fatalf("TypeAssertionFailure is %T, want std/assertions' *StructType: "+
			"std.Load did not replace the fallback, so every assertion below is vacuous",
			analysis.TypeAssertionFailure)
	}
	if st.Origin != "std/assertions" || st.Name != "AssertionFailure" {
		t.Fatalf("TypeAssertionFailure is %s.%s, want std/assertions.AssertionFailure", st.Origin, st.Name)
	}
	// The field the third case reads. Named rather than counted: a count is
	// churn every time assertions.nomi grows a row.
	hasReason := false
	for _, f := range st.Fields {
		if f.Name == "reason" {
			hasReason = true
		}
	}
	if !hasReason {
		t.Errorf("std/assertions.AssertionFailure has no `reason` field: %v", st.Fields)
	}
}

// TestAssertionFailureType_IgnoresAShadowingDeclaration fails on the ANSWER
// rather than on a type: if the caller's declaration retyped check's error,
// this source would produce no error naming `bogus` and the program would die
// at run time instead, with `struct 'assertions.AssertionFailure' has no field
// 'bogus'`.
func TestAssertionFailureType_IgnoresAShadowingDeclaration(t *testing.T) {
	src := `import {
  std/io
  std/testing
}

struct AssertionFailure {
  bogus: Int
}

fn main(): Unit {
  case testing.check(1 == 2) {
    Err(f) -> io.print("${f.bogus}")
    Ok(_) -> io.print("ok")
  }
}`
	_, errs := checkSourceWithStdlib(src)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "bogus") {
			found = true
		}
	}
	if !found {
		t.Errorf("reading `bogus` off testing.check's Err payload produced no error, so the "+
			"caller's own `struct AssertionFailure` retyped check's error and the mistake "+
			"is left for run time; errors were %v", errs)
	}
}

// TestAssertionFailureType_ResolvesWithoutAnImport is the other direction: an
// ordinary test file names `AssertionFailure` nowhere, and the Err payload must
// still be the real struct.
//
// This one does not pin the canonical type by itself: with a placeholder
// `PrimitiveType` in its place it would still pass, because a field read
// against the placeholder is not checked at all. The placeholder's cost is not
// an analysis error but that nothing downstream can NAME the type, and the IR
// builder pins that half; see internal/irbuild/testdata/check_shape.nomi.
// This case guards against the checker starting to REJECT ordinary programs,
// which is a different failure with no other cover.
func TestAssertionFailureType_ResolvesWithoutAnImport(t *testing.T) {
	src := `import {
  std/io
  std/testing
}

fn main(): Unit {
  case testing.check(1 == 2) {
    Err(f) -> io.print(f.reason)
    Ok(_) -> io.print("ok")
  }
}`
	_, errs := checkSourceWithStdlib(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "reason") || strings.Contains(e.Message, "AssertionFailure") {
			t.Errorf("reading `reason` off testing.check's Err payload was rejected: %s", e.Message)
		}
	}
}
