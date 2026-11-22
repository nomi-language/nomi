package irbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// maxIrrefutableSites is how many distinct source sites of each class of
// pattern the builder answers whether it always matches for with no
// record (patternAlwaysMatches, irrefutableClasses) the programs
// TestIRGolden_RecordsMatchTheVM runs reach. Each count only goes down.
// The counts were the same with the artifact cache empty, warm and off.
//
//   - "no record: a pattern the builder made": none; each pattern the
//     builder makes carries the record of the one it stands for.
var maxIrrefutableSites = map[string]int{
	string(irrefutableUnrecorded): 0,
}

// irrefutableRatchet holds the irrefutable pattern census to
// maxIrrefutableSites: every class irrefutableClasses holds.
func irrefutableRatchet() censusRatchet {
	r := censusRatchet{what: "pattern refutability sites", held: map[string]bool{}, max: maxIrrefutableSites,
		fix: "give the pattern the record of the checked pattern it stands for (carryPattern)"}
	for class, held := range irrefutableClasses {
		if held {
			r.held[string(class)] = true
		}
	}
	return r
}

// An answer to whether a pattern always matches that comes from a class
// irrefutableClasses does not list fails the lowering, naming the site
// and the class. With every class listed, the same program lowers.
func TestNoteIrrefutable_AnUnlistedClassFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `import std::io

fn main()
    pair = (1, 2)
    case pair
        (a, b) -> io.print("${a + b}")
    end
end
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects this, so the test checks nothing: %v", err)
	}
	lower := func() (failure string) {
		defer func() {
			if r := recover(); r != nil {
				failure = fmt.Sprint(r)
			}
		}()
		if _, _, err := GenerateIR(p); err != nil {
			return err.Error()
		}
		return ""
	}
	if failure := lower(); failure != "" {
		t.Fatalf("the program does not lower with every class listed: %s", failure)
	}
	held := irrefutableClasses[irrefutableFromRecord]
	delete(irrefutableClasses, irrefutableFromRecord)
	defer func() { irrefutableClasses[irrefutableFromRecord] = held }()
	want := `main.nomi:6:9: whether a pattern always matches is answered "from the record", which no class lists`
	if failure := lower(); !strings.Contains(failure, want) {
		t.Fatalf("lowering with the class unlisted failed with %q, want it to name %q", failure, want)
	}
}
