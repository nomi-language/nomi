package irbuild

import (
	"testing"
)

// THE OWNER NAME IS RESERVED, which is what lets `vectorCall` skip the
// local-declaration guard `listCall` and `setCall` also skip.
//
// Asserted where it holds rather than fenced inside `vectorCall`: if the front end
// ever stops reserving `Vector`, a program declaring `struct Vector` would have
// `g.types["Vector"]` populated and `vectorCall` would silently redirect its
// methods to `rt.Vector`. This fails NAMING that arm instead.
func TestVector_TheOwnerNameIsReserved(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := stagedProgram(t, "main.nomi", map[string]string{
		"main.nomi": "import std/io\n\n" +
			"struct Vector {\n  n: Int\n}\n\n" +
			"fn main() {\n  v = Vector{n: 1}\n  io.print(\"${v.n}\")\n}\n",
	})
	if _, err := Analyze(path); err == nil {
		t.Fatal("the front end ACCEPTED a user `struct Vector`. vectorCall has no " +
			"local-declaration guard because `g.types[\"Vector\"]` could never be " +
			"populated; it needs one now, or a user's own Vector has its methods " +
			"redirected to rt.Vector")
	}
}

// `Vector<T>` IS THE FIRST NON-CHANNEL ROW IN stdGenHostSpecs, and this pins the
// property that made the family's guard wrong when it arrived.
//
// `TestChannelSpecsMatchStdSource` resolved EVERY row of that table against
// `std/channels`'s FileAnalysis, which was correct while every row was a channel
// half and became a FALSE FAILURE the moment a `std/vectors` row joined. That is
// rule (6) from the other direction: a loop is only as correct as the table is
// homogeneous, and the guard read a constant where the table varies.
//
// Asserted here rather than only fixed there, because the fix is invisible: the
// widened loop passes on a one-module table too.
func TestVector_SpecTableIsNoLongerOneModule(t *testing.T) {
	origins := map[string]int{}
	for i := range stdGenHostSpecs {
		origins[stdGenHostSpecs[i].origin]++
	}
	if len(origins) < 2 {
		t.Fatalf("stdGenHostSpecs covers %d module(s) (%v). This test exists because a "+
			"guard over that table hard-coded ONE module; with the table back to one "+
			"module that guard's bug is unreachable and the widening in "+
			"TestChannelSpecsMatchStdSource is unexercised — say which row was removed "+
			"and whether the widening should stay", len(origins), origins)
	}
	if origins["std/vectors"] != 1 {
		t.Fatalf("stdGenHostSpecs has %d std/vectors row(s), want exactly 1: %v",
			origins["std/vectors"], origins)
	}
}
