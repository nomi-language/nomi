package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// `Iter.sort`'s Direction operand, AND WHY THE BUILDER'S CHECK IS A FENCE.
//
// iterCall (iriterbody.go) admits a sort's Direction operand only when its
// kind is std's own def, by POINTER IDENTITY against
// `stdEnumDefs()[stdEnumDirection]`. Every wrong-`Direction` spelling is
// rejected by the FRONT END first, with a message that discriminates the two
// declarations:
//
//	argument 2: expected std/comparable.Direction, got main.Direction
//
// So no user source reaches the builder's arm. It is kept because a front end
// that stops checking nominal identity at an argument position must decline
// here rather than lower a wrong sort.
// TestSortDirection_TheFrontEndRejectsEveryWrongSpelling is what would fail
// the moment that happens. TestIRIterSort_DescendingDirection
// (iritersort_test.go) runs the spelling that lowers.

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
			"import\n  std::io\nend\n\nenum Direction\n  Ascending\n  Descending\nend\n\n" +
				"fn main()\n  xs = [3, 1, 2] |> Iter.sort(Direction.Descending)\n  io.print(\"${xs}\")\nend\n"},
		{"an unrelated type entirely",
			"import\n  std::io\nend\n\n" +
				"fn main()\n  xs = [3, 1, 2] |> Iter.sort(7)\n  io.print(\"${xs}\")\nend\n"},
		{"a variant of a DIFFERENT std enum",
			"import\n  std::io\nend\n\n" +
				"fn main()\n  xs = [3, 1, 2] |> Iter.sort(Ordering.Less)\n  io.print(\"${xs}\")\nend\n"},
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
