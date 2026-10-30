package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A Float in a generic parameter's Float slot lowers and runs; an Int there is
// the checker's argument mismatch, never a call the builder declines.
func TestGenericFloatSlot_FloatsRunAndIntsAreRejected(t *testing.T) {
	got := runSourceOutput(t, `import std/io
import std/random.Generator

fn weight<T>(pair: #(Float, T)) -> Float {
    pair.0
}

fn main() {
    io.print(weight(#(10.0, "x")))
    io.print(Generator.weighted(#(-10.0, "bad"), []) |> Result.is_err())
}
`)
	if want := "10.0\nTrue\n"; got != want {
		t.Fatalf("output: got %q, want %q", got, want)
	}

	_, err := vmhost.LoadSource("main.nomi", `import std/random.Generator

fn main() {
    _ = Generator.weighted(#(-10, "bad"), [])
}
`)
	if err == nil {
		t.Fatal("the front end accepts an Int in weighted's Float slot")
	}
	if want := "argument 1: expected #(Float, T), got #(Int, String)"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error: got %v, want one containing %q", err, want)
	}
}
