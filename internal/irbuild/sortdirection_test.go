package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// `Iter sort direction | Iter.sort wants a Direction, got Direction` — THE
// DIAGNOSIS, AND WHY THE REFUSAL IS NOW A FENCE.
//
// That message refused three example programs and was routed as a suspected
// identity defect on the strength of its own text: the same printed name on both
// sides is the signature of comparing types by rendered name. It is neither of
// the two things the message lets a reader guess, and iterext.go's iterDirection
// header carries the measurement. In one sentence: there was ONE `Direction`
// def, the argument held it, and `stdEnumNamed("Direction")` could not FIND it,
// because a file that imports only `std/comparable.Direction.Descending`
// — the VARIANT — so the type NAME is never bound in that file's module scope.
//
// Two things follow, and they need different tests.
//
//  1. The three files' spelling must LOWER, and its result must match the
//     golden output. TestSortDirection_EverySpellingOfStdsDirectionLowers.
//
//  2. The refusal is now a FENCE rather than a live tally row, and this file
//     says so with evidence rather than by assertion. Every wrong-`Direction`
//     spelling is rejected by the FRONT END, with a message that already
//     discriminates the two declarations better than the builder's did:
//
//     argument 2: expected std/comparable.Direction, got main.Direction
//
//     So no user source reaches the builder's arm. It is kept and its detail
//     made readable anyway, because a front end that stops checking
//     nominal identity at an argument position must refuse here rather than
//     lower a wrong sort. TestSortDirection_TheFrontEndRejectsEveryWrongSpelling
//     is what would fail the moment that happens, and
//     TestSortDirection_TheFenceNamesTwoDifferentThings is what the message
//     would then say.

// TestSortDirection_TheFrontEndRejectsEveryWrongSpelling is what makes the
// builder's arm a FENCE rather than dead code.
//
// The arm now tests POINTER IDENTITY against `stdEnumDefs()[stdEnumDirection]`,
// and no user source can fail that test and still reach it: the analyzer checks
// the argument's nominal identity first. This asserts exactly that — so if the
// front end ever stops, the failure lands here, naming the arm that then becomes
// the only thing standing between a same-named type and a wrong sort.
func TestSortDirection_TheFrontEndRejectsEveryWrongSpelling(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"a LOCAL enum of the same name and shape",
			"import {\n  std/io\n}\n\nenum Direction {\n  Ascending\n  Descending\n}\n\n" +
				"fn main() {\n  xs = [3, 1, 2] |> Iter.sort(Direction.Descending)\n  io.print(\"${xs}\")\n}\n"},
		{"an unrelated type entirely",
			"import {\n  std/io\n}\n\n" +
				"fn main() {\n  xs = [3, 1, 2] |> Iter.sort(7)\n  io.print(\"${xs}\")\n}\n"},
		{"a variant of a DIFFERENT std enum",
			"import {\n  std/io\n}\n\n" +
				"fn main() {\n  xs = [3, 1, 2] |> Iter.sort(Ordering.Less)\n  io.print(\"${xs}\")\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.nomi")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Analyze(path); err != nil {
				return
			}
			t.Fatal("the front end ACCEPTED a wrong direction. iterDirection's pointer-identity " +
				"arm is now the only thing between this program and a wrong sort — check that its " +
				"refusal fires, and that its detail names the two declarations apart")
		})
	}
}
