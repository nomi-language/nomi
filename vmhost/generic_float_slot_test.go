package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A Float in a generic parameter's Float slot lowers and runs, as a Float
// literal does in weighted's NonNegativeFloat slot beside a T; an Int there is
// the checker's type mismatch, never a call the builder declines.
func TestGenericFloatSlot_FloatsRunAndIntsAreRejected(t *testing.T) {
	got := runSourceOutput(t, `import std::io
import std::random.Generator

fn weight<T>(pair: (Float, T)) -> Float
    pair.first
end

fn main()
    io.print(weight((10.0, "x")))
    io.print(Generator.weighted([(0.0, "never")]) |> Result.is_err())
end
`)
	if want := "10.0\nTrue\n"; got != want {
		t.Fatalf("output: got %q, want %q", got, want)
	}

	_, err := vmhost.LoadSource("main.nomi", `import std::random.Generator

fn main()
    _ = Generator.weighted([(10, "bad")])
end
`)
	if err == nil {
		t.Fatal("the front end accepts an Int in weighted's Float slot")
	}
	if want := "type mismatch: expected NonNegativeFloat, got Int"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error: got %v, want one containing %q", err, want)
	}

	// A negative literal there is a NonNegativeFloat literal that breaks
	// its rule, rejected when the program is compiled.
	_, err = vmhost.LoadSource("main.nomi", `import std::random.Generator

fn main()
    _ = Generator.weighted([(1.0, "ok"), (-1.0, "bad")])
end
`)
	if err == nil {
		t.Fatal("the front end accepts a negative weight in weighted's list")
	}
	if want := "-1.0 is not a NonNegativeFloat"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error: got %v, want one containing %q", err, want)
	}
}
