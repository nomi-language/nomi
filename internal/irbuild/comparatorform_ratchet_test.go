package irbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// maxComparatorSites is how many distinct source sites of each class of
// comparator not chosen from the checker's record (noteComparator,
// comparatorClasses) the programs TestIRGolden_RecordsMatchTheVM runs
// reach. Each count only goes down. The counts were the same with the
// artifact cache empty, warm and off.
//
//   - "no checker type": an element whose checker type the builder does
//     not have where it asks for the comparator, chosen by its kind.
var maxComparatorSites = map[string]int{
	string(comparatorNoType): 0,
}

// comparatorRatchet holds the comparator census to maxComparatorSites:
// every class comparatorClasses lists that is not agreed.
func comparatorRatchet() censusRatchet {
	r := censusRatchet{what: "comparator sites", held: map[string]bool{}, max: maxComparatorSites,
		fix: "give the element its checker type"}
	for class, agreed := range comparatorClasses {
		if !agreed {
			r.held[string(class)] = true
		}
	}
	return r
}

// A comparator chosen in a way comparatorClasses does not list fails the
// lowering, naming the site and the class. With every class listed, the
// same program lowers.
func TestNoteComparator_AnUnlistedClassFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `struct Widget
    weight: Int
end

impl Comparable for Widget
    fn compare(a: Widget, b: Widget) -> Ordering
        Int.compare(b.weight, a.weight)
    end
end

fn main()
    dbg Iter.sort([Widget{weight: 1}, Widget{weight: 2}])
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
	agreed := comparatorClasses[comparatorFromRecord]
	delete(comparatorClasses, comparatorFromRecord)
	defer func() { comparatorClasses[comparatorFromRecord] = agreed }()
	want := `main.nomi:12:18: a comparator is chosen "from the record", which no class lists`
	if failure := lower(); !strings.Contains(failure, want) {
		t.Fatalf("lowering with the class unlisted failed with %q, want it to name %q", failure, want)
	}
}
