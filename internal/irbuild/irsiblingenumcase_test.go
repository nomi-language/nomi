package irbuild

import "testing"

// A `case` over an enum declared in another file runs on the VM whatever
// spelling its patterns use: the selectively imported `Status.Open`, the
// file-qualified `ticket.Status.Open`, the dot shorthand, and an enum from
// another module named both ways (testdata/sibling_enum_case).
//
// declaredAs asks the resolver, which answers by declaration. This file's type
// table holds a spelling only once something resolved it, so a pattern that is
// the first mention of `Status` (the subject came from `ticket.make`) or of
// `ticket.Status` (the subject was typed `Status`) would miss the table and
// decline the whole function, leaving `describe`, `short` and `rank` BLOCKED.
func TestIRSiblingEnumCase_PatternsNameTheDeclaringFilesEnum(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := fixture("sibling_enum_case/app/main.nomi")
	want := "open | O | open\n" +
		"assigned to nobody | A:nobody | assigned\n" +
		"assigned to Ada | A:Ada | assigned\n" +
		"blocked on 7 tickets | B | blocked!\n" +
		"blocked on a few (2) | B | blocked\n" +
		"paired 3+4 | P3 | sum 7\n" +
		"noted: see thread | N | noted\n" +
		"closed as a duplicate | C | closed\n" +
		"closed today: fixed | C | closed\n" +
		"closed 45 days ago: stale | C (old) | stale\n" +
		"low\n" +
		"very high 9\n" +
		"high 2\n" +
		"custom odd\n"
	got := vmReference(path)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
	golden := goldenReference(t, path)
	if golden.stdout != got.stdout || golden.exit != got.exit {
		t.Errorf("golden record differs from the VM (exit %d):\n%s", golden.exit, golden.stdout)
	}
}
