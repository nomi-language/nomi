package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// sameNamesText declares an interface, a type, an impl of one for the
// other and a derived Equatable, and uses each; imports opens the file and
// call opens the function that uses them.
func sameNamesText(imports, call string) string {
	return imports + `
interface Announcer<T> {
    fn item(s: self) -> T

    fn announce(_s: self, loud: Bool = False) -> String {
        if loud { "item!" } else { "item" }
    }
}

struct Box {
    value: Int
}

impl Announcer for Box {
    fn item(s: Box) -> Int {
        s.value
    }
}

interface Labeled {
    fn label(v: self) -> String
}

impl Labeled for Box {
    fn label(v: Box) -> String {
        "box ${v.value}"
    }
}

derive Equatable for Box

` + call + `
    b = Box{value: 7}
    io.print(Announcer.announce(b))
    io.inspect(Announcer.item(b))
    io.print(Labeled.label(b))
    io.inspect(b == Box{value: 7})
}
`
}

// sameNamesProject is two files that each declare the same interface,
// type and impls: two declarations of each, which the program's impl
// tables, keyed by name, must still tell apart.
func sameNamesProject() incrementalProject {
	return incrementalProject{name: "same names", entry: "main", texts: map[string]string{
		"main":  sameNamesText("import {\n    std::io\n    other\n}\n", "fn main() {\n    other.run()"),
		"other": sameNamesText("import std::io\n", "pub fn run() {"),
	}}
}

// TestSameNamedDeclarationsInTwoFilesStayApart: two files that each
// declare an interface, a type and an impl of one for the other, under the
// same names, check, and each file's uses reach its own declarations. The
// conformance check once read one recorded interface per receiver and
// interface name, so one file's `Announcer.announce(b)` was refused, and a
// call's dispatch and an `==`'s impl were left unnamed as ambiguous.
func TestSameNamedDeclarationsInTwoFilesStayApart(t *testing.T) {
	p := sameNamesProject()
	full := fullCheck(p, p.texts)
	if errs := full.Errors; len(errs["main"])+len(errs["other"]) > 0 {
		t.Fatalf("the project does not check: %v", errs)
	}
	dump := analysis.DumpProgramForTest(full)
	items := 0
	for _, line := range strings.Split(dump, "\n") {
		if strings.HasPrefix(line, "ref ") && strings.Contains(line, " label -> label ") && strings.Contains(line, " dispatch ") {
			items++
		}
	}
	if items != 2 {
		t.Errorf("the two files' `Labeled.label(b)` name %d impls, want one each:\n%s", items, dump)
	}
	// The entry's impls are its file's, "" in the program's impl index.
	for _, file := range []string{"", "other"} {
		if !strings.Contains(dump, `= Equatable<> for Box at "`+file+`"`) {
			t.Errorf("file %q's `==` does not name its own derived Equatable:\n%s", file, dump)
		}
	}
}
