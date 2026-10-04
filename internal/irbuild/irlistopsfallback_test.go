package irbuild

import (
	"testing"
)

// head, tail and concat never read an element, so a list of retained structs
// takes the same cell operations as a list of scalars.
func TestIRListOps_NominalElementsRetain(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", `struct Box { value: Int }
fn choose(xs: List<Box>): Maybe<Box> { List.head(xs) }
fn main(): Maybe<Box> { choose([Box{value: 7}]) }
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
			if f.Name() == "choose" {
				return
			}
		}
	}
	t.Fatal("choose, List.head over a List<Box>, was not retained")
}
