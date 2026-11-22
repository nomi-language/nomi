package irbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// maxElemImplSites is how many distinct source sites of each class of
// comparator and of a part's equality not chosen from the checker's
// record (noteElemImpl, elemImplClasses) the programs
// TestIRGolden_RecordsMatchTheVM runs reach. Each count only goes down.
// The counts were the same with the artifact cache empty, warm and off.
// Over `nomi test tests --all`, `nomi test std --all` and vmhost's spec,
// tour and grid programs both classes had 0 sites.
//
//   - "no checker type": an element whose checker type the builder does
//     not have where it asks for its impl, chosen by its kind.
//   - "per instance, unresolved": an element whose checker type is over
//     a type parameter its instance does not resolve, chosen by its kind.
var maxElemImplSites = map[string]int{
	string(elemImplNoType):      0,
	string(elemImplPerInstance): 0,
}

// elemImplRatchet holds the element impl census to maxElemImplSites:
// every class elemImplClasses lists that is not agreed.
func elemImplRatchet() censusRatchet {
	r := censusRatchet{what: "element impl sites", held: map[string]bool{}, max: maxElemImplSites,
		fix: "give the element its checker type"}
	for class, agreed := range elemImplClasses {
		if !agreed {
			r.held[string(class)] = true
		}
	}
	return r
}

// A comparator chosen in a way elemImplClasses does not list fails the
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
	agreed := elemImplClasses[elemImplFromRecord]
	delete(elemImplClasses, elemImplFromRecord)
	defer func() { elemImplClasses[elemImplFromRecord] = agreed }()
	want := `main.nomi:12:18: a comparator is chosen "from the record", which no class lists`
	if failure := lower(); !strings.Contains(failure, want) {
		t.Fatalf("lowering with the class unlisted failed with %q, want it to name %q", failure, want)
	}
}
