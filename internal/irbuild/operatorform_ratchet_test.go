package irbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// maxOperatorFormSites is how many distinct source sites of each known
// disagreement between an operator's record and the route the builder
// lowers it by (verifyOperatorForm, operatorPairs) the programs
// TestIRGolden_RecordsMatchTheVM runs reach. Each count only goes down,
// as the stage named removes the class:
//
//   - "written, structural": an `==` the builder writes to reuse compare
//     (structuralEqualCall, containerEqualCall). Stage 8a: the switch on
//     the record makes the detour unnecessary.
//
// An operator over a type parameter resolves at its instance's checker
// types (checkSubstCensus), so a per-instance record left unresolved is
// no listed pair and fails the run.
// Agreed pairs and pairs by design are counted and logged, not held: an
// impl called as its record names it, an impl the machine computes
// itself lowered by the route machineImpls gives it (Int's `+` natively,
// List's `+` as its concatenation, List's and a derived enum's Equatable
// by shape), and the Int arithmetic of tupleHashOf's hash mix.
var maxOperatorFormSites = map[string]int{
	"written, structural": 0,
}

// heldOperatorClasses are the classes maxOperatorFormSites holds: every
// pair of operatorPairs neither agreed nor by design.
func heldOperatorClasses() map[string]bool {
	held := map[string]bool{}
	for _, c := range operatorPairs {
		if !c.agreed && !c.byDesign {
			held[c.name] = true
		}
	}
	return held
}

// checkOperatorCensus fails when a held class's sites rise past its
// maximum, naming them, and logs one that fell. A fall is logged, not
// failed, as checkSubstCensus does, so the check never depends on what
// else the process lowered or read from the artifact cache. The counts
// were the same with an empty artifact cache, with a warm one, and after
// the package's other ratchet tests ran first; lower a maximum from a run
// with NOMI_ARTIFACT_CACHE set to an empty directory.
func checkOperatorCensus(t *testing.T, sites map[string][]string) {
	t.Helper()
	held := heldOperatorClasses()
	for class := range maxOperatorFormSites {
		if !held[class] {
			t.Errorf("maxOperatorFormSites holds %q, which is no known disagreement of operatorPairs: remove it", class)
		}
	}
	var lines []string
	for class := range sites {
		lines = append(lines, fmt.Sprintf("%s: %d", class, len(sites[class])))
	}
	for class := range held {
		n := len(sites[class])
		max, listed := maxOperatorFormSites[class]
		switch {
		case !listed:
			t.Errorf("%q is a known disagreement with no maxOperatorFormSites entry: add it at %d", class, n)
		case n > max:
			list := append([]string(nil), sites[class]...)
			sort.Strings(list)
			t.Errorf("%d operator sites are lowered as %q, and at most %d may: lower the operator as its record says:\n  %s",
				n, class, max, strings.Join(list, "\n  "))
		case n < max:
			t.Logf("%d operator sites are lowered as %q: lower its maxOperatorFormSites entry to %d", n, class, n)
		}
	}
	sort.Strings(lines)
	t.Logf("operator sites by class: %s", strings.Join(lines, ", "))
}

// An operator lowered by a route its record is not listed with fails the
// lowering, naming the operator's position and both answers. With the
// pair listed, the same program lowers.
func TestVerifyOperatorForm_AnUnlistedPairFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `type Score Int

impl Add<Score, Score> for Score
  fn add(_lhs: Score, _rhs: Score) -> Score
    Score(1)
  end
end

fn main()
  dbg Score(2) + Score(3)
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
		t.Fatalf("the program does not lower with every pair listed: %s", failure)
	}
	pair := operatorPair{answerProgramImpl, routeImpl}
	class := operatorPairs[pair]
	delete(operatorPairs, pair)
	defer func() { operatorPairs[pair] = class }()
	want := "main.nomi:10:16: the builder lowers the operator through an impl, and the record answers it by the program's impl"
	if failure := lower(); !strings.Contains(failure, want) {
		t.Fatalf("lowering with the pair unlisted failed with %q, want it to name %q", failure, want)
	}
}
