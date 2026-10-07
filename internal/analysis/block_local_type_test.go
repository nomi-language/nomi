package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// A type declared in a block names a type in the annotations of that block
// and its inner blocks, and nowhere else.
func TestBlockLocalType_IsKnownToAnnotationsInItsBlockOnly(t *testing.T) {
	errs := checkWithStdlib(`
fn main() {
  inner = {
    type Meters Int
    m: Meters = Meters(3)
    f = |d: Meters = Meters(1)| Int(d)
    g: (Meters) -> Int = |d| Int(d)
    _ = {
      n: Meters = m
      n
    }
    f(m) + g(m)
  }
  _ = inner
  outside: Meters = 1
  _ = outside
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{
		`15:12 unknown type "Meters"`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
