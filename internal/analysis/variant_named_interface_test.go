package analysis_test

import (
	"strings"
	"testing"
)

// An enum variant may carry the name of a prelude interface the compiler
// synthesizes impls for. A variant is only named through its enum, and the
// prelude's injected import takes the bare slot, so the variant must not hide
// the interface from the file's own calls (`Debug.inspect`) or from the
// synthesized `impl Debug` and derive headers, which reported
// "impl block: 'Debug' is not an interface".
func TestEnumVariantNamedLikeAPreludeInterface_DoesNotShadowIt(t *testing.T) {
	src := `enum Op {
  Display
  Debug
  Equatable
  Hashable
  Comparable
  Ordering
}

derive Equatable for Op
derive Hashable for Op
derive Comparable for Op
derive Display for Op

fn main() {
  a = Debug.inspect(Op.Debug)
  b = Op.Debug == Op.Display
  c = Display.to_string(Op.Ordering)
  d = Comparable.compare(Op.Display, Op.Debug)
}
`
	_, errs := checkProjectWithFiles(t, src, nil)
	for _, e := range errs {
		if strings.Contains(e.Message, "is never used") {
			continue
		}
		t.Errorf("%d:%d: %s", e.Line, e.Col, e.Message)
	}
}

// A type named `Debug` is still the reserved-name error, and only that: the
// synthesized `impl Debug` headers resolve to the interface, not to it.
func TestTypeNamedDebug_IsOnlyTheReservedNameError(t *testing.T) {
	src := `struct Debug {
  n: Int
}

fn main() {
  a = Debug{n: 1}
}
`
	_, errs := checkProjectWithFiles(t, src, nil)
	var reserved bool
	for _, e := range errs {
		switch {
		case strings.Contains(e.Message, "type name 'Debug' is reserved by the language"):
			reserved = true
		case strings.Contains(e.Message, "is never used"):
		default:
			t.Errorf("unexpected error beside the reserved name: %d:%d: %s", e.Line, e.Col, e.Message)
		}
	}
	if !reserved {
		t.Fatalf("the front end now admits a struct named Debug; errors: %v", errs)
	}
}
