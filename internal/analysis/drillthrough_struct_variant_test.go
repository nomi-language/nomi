package analysis_test

import (
	"strings"
	"testing"
)

// A struct-shaped variant imported by name (`import
// std/supervisors.Backoff.Exponential`) builds the variant from a bare brace
// literal, as `Backoff.Exponential{}` does. The checker typed `Exponential{}`
// as the variant's constructor function and checked none of its fields.
func TestDrillThroughStructVariantLit_TypesAsTheEnum(t *testing.T) {
	_, errs := checkSourceWithStdlib(`import std/supervisors.Backoff
import std/supervisors.Backoff.Exponential

fn main() {
  a: Backoff = Exponential{}
  b: Backoff = Exponential{max_restarts: 3}
  _pair = (a, b)
}
`)
	if len(errs) != 0 {
		t.Fatalf("a bare imported struct variant literal must type as Backoff; got %v", errs)
	}
}

func TestDrillThroughStructVariantLit_ChecksFields(t *testing.T) {
	_, errs := checkSourceWithStdlib(`import std/supervisors.Backoff.Exponential

fn main() {
  _a = Exponential{max_restarts: "three"}
  _b = Exponential{bogus: 1}
}
`)
	var mismatch, unknown bool
	for _, e := range errs {
		mismatch = mismatch || strings.Contains(e.Message, "max_restarts")
		unknown = unknown || strings.Contains(e.Message, "no field 'bogus' on variant Exponential")
	}
	if !mismatch || !unknown {
		t.Fatalf("want a field type error on max_restarts and an unknown-field error on bogus; got %v", errs)
	}
}
