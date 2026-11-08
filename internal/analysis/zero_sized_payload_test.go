package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// A positional payload of a type with one value (`Ping(Unit)`, `Marked(Mark)`)
// is a payload like any other: `Signal.Ping(Unit)` constructs the variant,
// `Signal.Ping` is its constructor as a function value, and a pattern spells
// the payload. Before, the variant was typed as a value, so the call had no
// type and a tuple holding it was a `(Unit, String)`. An `embeds` of a
// zero-sized type is still the variant's one value.
func TestZeroSizedPositionalPayload_IsAPayload(t *testing.T) {
	errs := checkWithStdlib(`
type Mark

type Expired

enum Signal {
  Ping(Unit)
  Marked(Mark)
  embeds Expired
  Stop
}

fn main() {
  pairs: List<(Signal, String)> = [(Signal.Ping(Unit), "a"), (Signal.Stop, "b")]
  _ = pairs
  _ = Signal.Marked(Mark)
  _ = Signal.Expired
  f: (Unit) -> Signal = Signal.Ping
  _ = f
  n: Int = Signal.Ping(Unit)
  _ = n
  s = Signal.Stop
  _ = case s {
    Signal.Ping -> 1
    Signal.Marked(_) -> 2
    Signal.Expired -> 3
    Signal.Stop -> 4
  }
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{
		"20:12 type mismatch: expected Int, got Signal",
		"24:5 variant Ping carries data; pattern needs an explicit payload (e.g. Ping(_) or Ping(name))",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
