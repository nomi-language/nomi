package irbuild

import (
	"strings"
	"testing"
)

// The guards over std/dynamic's struct-over-enum pair.
//
// `impl Display for DecodeError` and `derive Display for PathSegment` are
// ordinary Nomi in std/dynamic.nomi, so the text comes from one renderer. What
// needs proving is the LOWERING around it -- that the spec's layout is the one
// std declares, that `case` discriminates the enum tag, that the two payload
// positions read back out of the right rt fields, and that the value resolves
// to std's own impl rather than to an anonymous struct of the same shape.
//
// # The mutation results
//
// Each run separately. THIS ROW IS NOT WHERE THE COVERAGE IS: the rt-reflection
// and shape guards catch every spec mistake, and the pinned text catches NONE
// of them.
//
//  1. Point both PathSegment variants at ONE rt field (`field: "Field"` for
//     Index too). -> TestStdEnumSlotsMatchRT,
//     TestStdEnumSpecReachesTheRealRtType, TestStdEnumDefsArePackageNeutral,
//     because rt has no second field for the tag to store into.
//
//  2. Swap the two variants' ORDER in the spec. -> TestStdEnumShapeMatchesStdSource,
//     TestStdStructSpecsMatchStdSource, TestStdEnumSpecsAreReachable. The shape
//     check reads std's declaration order directly, so it sees this without
//     running anything.
//
//  3. Replace the `List<PathSegment>` field with a String one. -> NOT a Go
//     compile error; `go vet` passes. Caught by
//     TestStdStructGoWidthMatchesTheDeclaredField (the spec's kind against rt's
//     actual Go type) and TestStdStructSpecsMatchStdSource.
//
// So the pinned text below earns its place on a NARROWER argument than the one
// in stdstructdefault_test.go: it caught none of these three, because all three
// are spec-side. What it guards is a change in std/dynamic's own renderer or
// declaration, and the fixture going silent.

// TestStdStructDynamic_PinnedText spells the transcript out absolutely.
func TestStdStructDynamic_PinnedText(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a fixture")
	}
	path := fixture("stdstruct_dynamic.nomi")
	want := strings.Join([]string{
		"expected = Int",
		"got = String",
		// The two variants store into DIFFERENT rt fields. One row cannot see a
		// spec that collapsed them; these two can.
		"path.first = field:user",
		"path.second = index:3",
		// Tags come from the spec's variant order and nowhere else.
		"tag_order = field:a/index:1",
		// IDENTITY. Both of these reach an impl declared in std/dynamic.nomi,
		// so they only read this way if the emitted value is the type std
		// declares rather than an anonymous struct of the same shape.
		"display = decode error at user[3]: expected Int, got String",
		"seg_display = Field(k)",
		// The empty path is the nil *List, and std's renderer spells it
		// `<root>` -- so a nil that failed to be walkable would not reach here.
		"empty.display = decode error at <root>: expected List, got Int",
		"",
	}, "\n")
	if got := vmReference(path); got.stdout != want || got.exit != 0 {
		t.Fatalf("reference output is not what this fixture pins: %s\nwant:\n%s", got, want)
	}
}
