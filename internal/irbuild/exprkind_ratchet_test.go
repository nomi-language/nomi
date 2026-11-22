package irbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// The expression-kind verifier (verifyExprKind) holds the kind the
// builder lowers each expression at to the type the checker recorded for
// it, or the type a record that changes it gives (exprKindAgreement), and
// fails on any other answer. It lists no known disagreement.
//
// The verifier runs on every expression lowerExact answers, in every test
// binary, and in any binary under NOMI_SUBST_VERIFY=1 (substVerify): the
// corpus, std's cases, the tour, the spec's programs, the type grid, the
// feature matrix and the lowering fuzzer's seeds all lower through it, and
// a disagreement fails there at once. So does every program any test of
// this package or of vmhost lowers: a change to the verifier or to what
// the checker records is reported only after the whole of both packages
// passes (`go test ./internal/irbuild -count=1 -timeout 30m` and the same
// for ./vmhost), since a narrow run met none of the disagreements those
// found. TestIRGolden_RecordsMatchTheVM counts its programs' sites
// (exprKindCensus), with
//
//	go test ./internal/irbuild -run '^TestIRGolden_RecordsMatchTheVM$' -count=1 -v
//
// whose log gives the sites that agree by the record they agree with
// (`agrees: ...`), the sites with no record by why (`no record: ...`), the
// sites of a construct that reads its kind from a record it does not have
// (`no kind record, ...`), and the same for std's own lowerings
// (stdExprKindCensus), which are reported and never held.
// NOMI_CALLEE_LOG=<file> writes a disagreement to the file instead of
// failing, to list them all in one run. The held counts are the same with
// NOMI_ARTIFACT_CACHE set to an empty directory, to the same directory
// warm, and to off; with it off a program also lowers std instances the
// cache would supply, which add agreeing sites.
//
// An `if` or `case` used for its value, a block used as an operand, a
// `concurrent` body and a `todo` or `unreachable` read their kind, and a
// lambda its result kind, from the record the verifier compares with
// (recordedKind, settledKind), so for them the comparison holds by
// construction (a lambda's parameter kinds are still computed and
// compared). A node the builder writes carries the type of the source it
// stands for (carryType). A List, Map or Vector function's call reads its
// result, a collection, tuple or record literal its kind (literalKind,
// convertedKind), an `Iter.loop` its state and a partial capture its open
// slots from the record too. Where such a node has no record, a block
// computes its kind from its tail, a List, Map or Vector call takes its
// row's result, a literal the builder made takes its position's kind, a
// tuple or record with no position its parts' kinds, and the others
// decline, and maxUnrecordedKindSites counts the site. The verifier still holds every other node type.

// maxUnrecordedKindSites is how many distinct source sites of a construct
// whose kind the builder reads from the checker's record have none it can
// read (noteUnrecordedKind), by family and by why there is no record, in
// the programs TestIRGolden_RecordsMatchTheVM runs. Each count only goes down:
// a node the builder writes carries the record of the source it stands
// for (carryType), as a lambda a capture, a partial or a nested `fn`
// stands for does. A class with no entry is held at 0.
var maxUnrecordedKindSites = map[string]int{
	// A generic declaration's own body, lowered with no instance's frame
	// (generic_loop_break.nomi's `loop(x, ...)` in `first<T>` and
	// `echo_state<T>`): the loop's state is the declaration's type
	// parameter, which has a kind only inside an instance, so the loop
	// declines there. This is representation, not a missing record: the
	// checker records T, and each instance the program calls lowers the
	// loop at its type argument.
	exprKindUnrecordedPrefix + "Iter.loop state: a record that names no kind": 2,
}

// exprKindRatchet holds the expression kind census to
// maxUnrecordedKindSites, for every site with no record where the record
// is read.
func exprKindRatchet(program map[string][]string) censusRatchet {
	r := censusRatchet{what: "expressions", held: map[string]bool{}, max: map[string]int{},
		fix: "lower them at the kind the checker records"}
	for class := range program {
		if strings.HasPrefix(class, exprKindUnrecordedPrefix) {
			r.held[class] = true
		}
	}
	for class, n := range maxUnrecordedKindSites {
		r.held[class] = true
		r.max[class] = n
	}
	return r
}

// checkExprKindCensus holds the programs' census to exprKindRatchet and
// logs, for it and for std's own lowerings, the share of the sites with a
// record that agree.
func checkExprKindCensus(t *testing.T, program, std map[string][]string) {
	t.Helper()
	checkSiteCensus(t, exprKindRatchet(program), program)
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
		case strings.HasPrefix(class, exprKindUnrecordedPrefix):
			// A site the verifier also counts, under the record it agrees
			// with or as one with none.
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

// lowerForVerifier analyzes src as main.nomi and answers the program and a
// function that lowers it and returns the lowering's failure, the
// verifier's panic included, or "".
func lowerForVerifier(t *testing.T, src string) (*Program, func() string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects this, so the test checks nothing: %v", err)
	}
	return p, func() (failure string) {
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

// An expression lowered at a kind its record disagrees with fails the
// lowering, naming the expression's position and both answers: the
// program lowers, and with the record of its `7` changed to String it
// fails there.
func TestVerifyExprKind_ADisagreementFails(t *testing.T) {
	p, lower := lowerForVerifier(t, `fn main()
    n = 1
    dbg n + 7
end
`)
	if failure := lower(); failure != "" {
		t.Fatalf("the program does not lower: %s", failure)
	}
	changed := false
	for _, m := range p.Modules {
		if m.Name != "main" || m.FA == nil {
			continue
		}
		for n := range m.FA.ExprTypes {
			if lit, isInt := n.(*ast.IntLit); isInt && lit.Line == 3 {
				m.FA.ExprTypes[n] = analysis.TypeString
				changed = true
			}
		}
	}
	if !changed {
		t.Fatal("the checker recorded no type for the `7`, so the test checks nothing")
	}
	failure := lower()
	want := "the checker records it as String (String)"
	if !strings.Contains(failure, "main.nomi:3:") || !strings.Contains(failure, want) {
		t.Fatalf("lowering with the record changed failed with %q, want it to name line 3 and %q", failure, want)
	}
}

// A function value of an impl function whose `where` bound the instance it
// is taken in meets (`Bag.add` inside `Bag<Int>`'s `from`) is the plain
// function its record names, `True` and a name a `Bool.False(x)` pattern
// binds are Bools, a partial capture over a List of Lists takes each List
// (`&Iter.sum(&1)`), and an `Iter.loop` over a Display state is one: every
// expression agrees with its record.
func TestVerifyExprKind_BoundFunctionValuesAndBoolVariantsAgree(t *testing.T) {
	_, lower := lowerForVerifier(t, `pub opaque struct Bag<T>
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
    xss = [[1, 2], [3]]
    dbg xss |> Iter.map(&Iter.sum(&1)) |> Iter.to_list()
    d: Display = Iter.loop(Set[1, 2], fn s -> if True then break s else s)
    dbg Display.to_string(d)
end
`)
	if failure := lower(); failure != "" {
		t.Fatalf("the program does not lower: %s", failure)
	}
}
