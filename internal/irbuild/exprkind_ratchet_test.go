package irbuild

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// maxExprKindSites is how many distinct source sites of each known way the
// kind the builder lowers an expression at and the type the checker
// recorded for it differ (verifyExprKind, knownExprKindClasses) the
// programs TestIRGolden_RecordsMatchTheVM runs reach, in their own
// lowerings (exprKindCensus). Each count only goes down. Every class's
// cause is in knownExprKindClasses.
//
// The verifier runs on every expression lowerExact answers, in every test
// binary, and in any binary under NOMI_SUBST_VERIFY=1 (substVerify): the
// corpus, std's cases, the tour, the spec's programs, the type grid, the
// feature matrix and the lowering fuzzer's seeds all lower through it, and
// an unlisted disagreement fails there at once. So does every program any
// test of this package or of vmhost lowers: a change to the verifier, to
// a class or to what the checker records is reported only after the whole
// of both packages passes (`go test ./internal/irbuild -count=1 -timeout
// 30m` and the same for ./vmhost), since a narrow run met none of the
// disagreements those found. Only this test's population is counted and
// held, with
//
//	go test ./internal/irbuild -run '^TestIRGolden_RecordsMatchTheVM$' -count=1 -v
//
// whose log gives the sites by class: the held classes, the sites that
// agree by the record they agree with (`agrees: ...`), the sites with no
// record by why (`no record: ...`), and the same for std's own lowerings
// (stdExprKindCensus), which are reported and never held.
// NOMI_CALLEE_LOG=<file> writes an unlisted disagreement to the file
// instead of failing, to list them all in one run. The held counts are
// the same with NOMI_ARTIFACT_CACHE set to an empty directory, to the same
// directory warm, and to off; with it off a program also lowers std
// instances the cache would supply, which add agreeing sites.
//
// The classes the other populations reach and this one does not are held
// at 0 here, and pass wherever they are met: a generic call instantiated
// at its arguments' kinds (the type grid's `d: Display = Iter.loop(...)`),
// a partial's Iter parameter and a literal recorded at its position's
// element types (tests/, the lowering fuzzer's seeds, an embedded value
// in a Vector literal in TestIREmbeddedSourceAsIterOfItsEnum_Runs).
var maxExprKindSites = map[exprKindClass]int{
	exprKindHeldConcrete:      0,
	exprKindCaptureView:       0,
	exprKindLiteralAtPosition: 0,
}

// exprKindRatchet holds the expression kind census to maxExprKindSites:
// every class knownExprKindClasses lists.
func exprKindRatchet() censusRatchet {
	r := censusRatchet{what: "expressions", held: map[string]bool{}, max: map[string]int{},
		fix: "lower them at the kind the checker records"}
	for class := range knownExprKindClasses {
		r.held[string(class)] = true
	}
	for class, n := range maxExprKindSites {
		r.max[string(class)] = n
	}
	return r
}

// checkExprKindCensus holds the programs' census to exprKindRatchet and
// logs, for it and for std's own lowerings, the share of the sites with a
// record that agree.
func checkExprKindCensus(t *testing.T, program, std map[string][]string) {
	t.Helper()
	checkSiteCensus(t, exprKindRatchet(), program)
	t.Logf("expressions in the programs' own lowerings: %s", exprKindAgreedShare(program))
	checkSiteCensus(t, censusRatchet{what: "expressions in std's own lowerings"}, std)
	t.Logf("expressions in std's own lowerings: %s", exprKindAgreedShare(std))
}

// exprKindAgreedShare is the share of a census's sites with a record that
// agree.
func exprKindAgreedShare(sites map[string][]string) string {
	agreed, recorded := 0, 0
	for class, list := range sites {
		switch {
		case strings.HasPrefix(class, exprKindAgreePrefix):
			agreed += len(list)
			recorded += len(list)
		case !strings.HasPrefix(class, exprKindNoRecordPrefix):
			recorded += len(list)
		}
	}
	if recorded == 0 {
		return "no site with a record"
	}
	return fmt.Sprintf("%d of %d sites with a record agree (%.3f%%)", agreed, recorded, 100*float64(agreed)/float64(recorded))
}

// lowerForVerifier analyzes src as main.nomi and answers a function that
// lowers it and returns the lowering's failure, the verifier's panic
// included, or "".
func lowerForVerifier(t *testing.T, src string) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects this, so the test checks nothing: %v", err)
	}
	return func() (failure string) {
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
}

// withoutExprKindClasses runs f with every class unlisted, so that any
// disagreement fails the lowering.
func withoutExprKindClasses(f func()) {
	saved := maps.Clone(knownExprKindClasses)
	clear(knownExprKindClasses)
	defer maps.Copy(knownExprKindClasses, saved)
	f()
}

// An expression lowered at a kind its record disagrees with in a way no
// listed class covers fails the lowering, naming the expression's position
// and both answers. With the class listed, the same program lowers.
func TestVerifyExprKind_AnUnlistedDisagreementFails(t *testing.T) {
	lower := lowerForVerifier(t, `fn main()
    d: Display = Iter.loop(Set[1, 2], fn s -> if True then break s else s)
    dbg Display.to_string(d)
end
`)
	if failure := lower(); failure != "" {
		t.Fatalf("the program does not lower with every class listed: %s", failure)
	}
	var failure string
	withoutExprKindClasses(func() { failure = lower() })
	want := "the checker records it as Display (Display)"
	if !strings.Contains(failure, "main.nomi:2:") || !strings.Contains(failure, want) {
		t.Fatalf("lowering with the class unlisted failed with %q, want it to name line 2 and %q", failure, want)
	}
}

// A function value of an impl function whose `where` bound the instance it
// is taken in meets (`Bag.add` inside `Bag<Int>`'s `from`) is the plain
// function its record names, and `True` and a name a `Bool.False(x)` pattern
// binds are Bools: every expression agrees with its record, no class listed.
func TestVerifyExprKind_BoundFunctionValuesAndBoolVariantsAgree(t *testing.T) {
	lower := lowerForVerifier(t, `pub opaque struct Bag<T>
    items: List<T>
end

impl Bag<T>
    pub fn from(values: List<T>) -> Bag<T> where T: Comparable
        Iter.reduce(values, Bag{items: []}, Bag.add)
    end

    pub fn add(b: Bag<T>, x: T) -> Bag<T> where T: Comparable
        {items: [..b.items, x]}
    end

    pub fn items(b: Bag<T>) -> List<T>
        b.items
    end
end

fn flip(b: Bool) -> Bool
    case b
        Bool.False(x) -> x
        Bool.True(_) -> False
    end
end

fn main()
    dbg Bag.from([1, 2]) |> Bag.items()
    t = True
    dbg flip(t)
end
`)
	var failure string
	withoutExprKindClasses(func() { failure = lower() })
	if failure != "" {
		t.Fatalf("the program does not lower with no class listed: %s", failure)
	}
}
