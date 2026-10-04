package irbuild

// `break` / `continue` / `return` in a LAZY ADAPTER callback and in `Iter.each`.
//
// Checked with golden text for a fixture's output, and refusal witnesses by
// name, each a program that checks clean.
//
// This family also needs PER-FAMILY DISCRIMINATION. `map`, `filter` and
// `take_while` share a signature and a boundary form, and differ only in what
// the driver does with the value, so a mutation collapsing any two of them is
// caught only by a fixture containing a shape the collapsed pair gets wrong.

// --- per-family discrimination ----------------------------------------------
//
// Each of the three asserts the rt entry point AND the family's own rule, over
// the SAME callback text where the rule allows it. Reading the emitted call is
// not decoration: `rt.SeqMapCtl` and `rt.SeqFilterCtl` take identical Go
// argument lists, so a routing mutation that swapped them is invisible in every
// signature and visible only here and in the fixture's answers.

// --- the refused families ----------------------------------------------------

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
