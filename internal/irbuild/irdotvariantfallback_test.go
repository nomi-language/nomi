package irbuild

import (
	"testing"
)

// The Polygon payload, a tuple holding a function value, is one the builder
// declines, so the enum and every body that builds it stay unretained.
func TestIRDotVariant_UnsupportedPayloadPreservesFallback(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", `enum Shape {
  Circle Float
  Polygon { sides: (Int, (Int) -> Int) }
}
fn make(): Shape { .Circle(1.0) }
fn main(): Shape { make() }
`)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range got.IR {
		for _, f := range mod.Funcs() {
			if f.Name() == "make" {
				t.Fatal("unsupported enum retained")
			}
		}
	}
}
