package vm_test

// A POSITIONED FAULT TEXT HAS ONE HOME, AND THIS IS THE INSTRUMENT THAT SAYS
// SO.
//
// `internal/irbuild/case.go`'s rule: the text lives in `rt` so no consumer can
// drift from it. `rt/arith.go`'s own header states the arrangement: the
// overflow predicates and the error text live there once.
//
// A string that two places each spell has no mechanism keeping them equal.
// This test is the instrument that catches such a copy.
//
// WHY IT LIVES IN `internal/vm`, which is not obviously its home:
//
//   - It does not live in `rt`. A test there would have to reach into the
//     compiler's source tree and would make the compiler's layout part of
//     rt's test surface.
//   - `internal/ir/arith_test.go` is the nearest sibling: it reads rt's OWN
//     SOURCE to check the operator spellings passed to `rt.OverflowError`.
//     This check does not live there because `internal/ir` is the shared
//     PRODUCER and has no other reason to know where the consumers' sources
//     are.
//   - `internal/vm` is the consumer that spells fault texts at run time, so
//     the instrument is here.
//
// THE OWNED SET IS DERIVED, NOT LISTED. Every positioned fault text `rt`
// declares is found by parsing `rt`'s own non-test sources, so a NEW trap text
// added to `rt` is policed without anyone remembering to add a row.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// trapTextPrefix is the shape of a positioned fault text: the line number
// first, then a colon. Every fault a Nomi program observes with a position
// reads this way.
//
// They live in more than the arithmetic file: `arith.go` (integer overflow,
// division by zero, float modulo, the decimal faults), `assertion.go` (the
// assertion failure's own `line %d: %s`, and `test returned early`),
// `trap.go` (no matching case branch, a missing map destructuring key, no
// active app value, the call-depth fault) and the `dbg` header prefix in
// `dbg.go`. The scan below derives the population from `rt`'s own sources
// rather than a hand-written count, as `rt/faultposition_test.go`'s
// `TestFaultPosition_EveryOwnedTextIsListed` does.
//
// Only the ones listed in knownTrapTextCopies are spelled anywhere else.
const trapTextPrefix = "line %d:"

// knownTrapTextCopies records every spelling of an rt-owned fault text that
// lives OUTSIDE rt, keyed on the text and valued on the site.
//
// IT IS EXACT IN BOTH DIRECTIONS. A new copy fails this test; so does
// repairing one of these and leaving the row behind. That is the shape
// `TestEveryScalarHostFnIsBoundOrDeclared` uses, and it is why the table is a
// ledger rather than a suppression list. It is empty: every rt-owned fault
// text is spelled only in rt.
//
// A repaired copy with its row left behind fails just as loudly as a new
// copy, so deleting a row belongs in the commit that removes the copy.
var knownTrapTextCopies = map[string][]string{}

// TestTrapText_OneHomePerText is the check that fails when a new copy
// appears.
func TestTrapText_OneHomePerText(t *testing.T) {
	root := repoRoot(t)
	owned := ownedTrapTexts(t, root)

	// POSITIVE CONTROL ON THE OWNED SET. A parse that silently answered
	// nothing would make every comparison below vacuous.
	if len(owned) < 11 {
		t.Fatalf("parsing rt found %d positioned fault texts; rt declares 11, "+
			"so the scan is broken and this test would pass for the wrong reason: %v",
			len(owned), sortedKeys(owned))
	}
	for _, want := range []string{
		"line %d: integer overflow: %d %s %d",
		"line %d: division by zero",
		"line %d: modulo not supported on floats",
	} {
		if _, found := owned[want]; !found {
			t.Fatalf("rt declares %q and the scan did not find it; the extractor is wrong", want)
		}
	}

	sites := trapTextSites(t, root, owned)

	// Every owned text is declared EXACTLY ONCE inside rt. Two spellings in
	// one module is the same drift as two across modules.
	for _, text := range sortedKeys(owned) {
		inRT := 0
		for _, s := range sites[text] {
			if strings.HasPrefix(s, "rt/") {
				inRT++
			}
		}
		if inRT != 1 {
			t.Errorf("%q is spelled %d times inside rt; it must have one home there\n  %v",
				text, inRT, sites[text])
		}
	}

	// Every occurrence outside rt is a COPY, and the ledger must name it.
	for _, text := range sortedKeys(owned) {
		var outside []string
		for _, s := range sites[text] {
			if !strings.HasPrefix(s, "rt/") {
				outside = append(outside, s)
			}
		}
		outside = dedupeSorted(outside)
		want := dedupeSorted(knownTrapTextCopies[text])
		if !equalStrings(outside, want) {
			t.Errorf("%q\n  rt owns this text; engines must CALL rt rather than spell it.\n"+
				"  copies outside rt: %v\n  ledger says:       %v\n"+
				"  If a new copy appeared, replace it with the rt constructor.\n"+
				"  If a copy was repaired, delete its row from knownTrapTextCopies.",
				text, outside, want)
		}
	}

	// Every ledger row names a text rt actually owns. A row for a text rt no
	// longer declares is a stale exemption hiding nothing.
	for text := range knownTrapTextCopies {
		if _, isOwned := owned[text]; !isOwned {
			t.Errorf("knownTrapTextCopies has a row for %q, which rt no longer declares", text)
		}
	}
}

// TestTrapText_TheVMRetypesNone establishes, rather than reads, that this
// engine spells none of rt's fault texts.
//
// The VM's contribution to the copy census must be EMPTY, for every owned
// text.
func TestTrapText_TheVMRetypesNone(t *testing.T) {
	root := repoRoot(t)
	owned := ownedTrapTexts(t, root)
	sites := trapTextSites(t, root, owned)

	var mine []string
	for _, text := range sortedKeys(owned) {
		for _, s := range sites[text] {
			if strings.HasPrefix(s, "internal/vm/") {
				mine = append(mine, fmt.Sprintf("%s spells %q", s, text))
			}
		}
	}
	if len(mine) != 0 {
		t.Errorf("github.com/nomi-language/nomi/internal/vm spells a fault text rt owns:\n  %s",
			strings.Join(mine, "\n  "))
	}
	// The VM must nevertheless REACH the text, or this zero is the zero of an
	// engine that simply has no Float `%` arm. `arith`'s FaultUndefined arm is
	// the caller and `rt.FloatModuloText` is what it calls.
	src, err := os.ReadFile(filepath.Join(root, "internal", "vm", "vm.go"))
	if err != nil {
		t.Fatalf("reading vm.go: %v", err)
	}
	if !strings.Contains(string(src), "rt.FloatModuloText(line)") {
		t.Error("vm.go does not call rt.FloatModuloText, so the zero above is the zero of " +
			"an engine that never reaches the text rather than one that reads it")
	}
}

// TestTrapText_APlantedCopyIsFound validates the detector against a copy it
// did not have to find in real source.
//
// A ZERO IS VALIDATED BY PLANTING A POSITIVE, and the specific way the check
// above could be vacuous is a string extractor that misses the spelling a
// new copy would actually use. Three spellings are planted and all three
// must be found: a plain literal, a CONCATENATION of adjacent literals (which
// is how rt itself spells the non-terminating text, and a per-literal scan
// cannot see it), and a raw-string literal.
func TestTrapText_APlantedCopyIsFound(t *testing.T) {
	const planted = `package fourthengine

import "fmt"

func a(line int) error { return fmt.Errorf("line %d: modulo not supported on floats", line) }

func b(line int) error {
	return fmt.Errorf("line %d: non-terminating decimal division; use "+
		"Decimal.divide(a, b, scale, mode) for explicit rounding", line)
}

func c() string { return ` + "`line %d: division by zero`" + ` }

func d() string { return "line %d: nothing rt owns" }
`
	got, err := staticStringsIn("fourthengine/engine.go", []byte(planted))
	if err != nil {
		t.Fatalf("parsing the planted source: %v", err)
	}
	for _, want := range []string{
		"line %d: modulo not supported on floats",
		"line %d: non-terminating decimal division; use Decimal.divide(a, b, scale, mode) for explicit rounding",
		"line %d: division by zero",
	} {
		if !got[want] {
			t.Errorf("the extractor missed a planted copy of %q; a fourth engine spelling it "+
				"this way would not be caught", want)
		}
	}
	// The negative half: a positioned text rt does NOT own must not be
	// reported as a copy, or the check would fire on every engine's own
	// diagnostics.
	root := repoRoot(t)
	owned := ownedTrapTexts(t, root)
	if _, isOwned := owned["line %d: nothing rt owns"]; isOwned {
		t.Error("the owned set contains a text rt does not declare")
	}
}

// --- the scanner -----------------------------------------------------------

// repoRoot walks up to the repository root, which holds go.mod and rt/.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		_, rtErr := os.Stat(filepath.Join(dir, "rt", "arith.go"))
		_, langErr := os.Stat(filepath.Join(dir, "go.mod"))
		if rtErr == nil && langErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no directory above %s holds both rt/arith.go and go.mod", dir)
		}
		dir = parent
	}
}

// ownedTrapTexts is every positioned fault text rt's non-test sources declare.
func ownedTrapTexts(t *testing.T, root string) map[string]bool {
	t.Helper()
	owned := map[string]bool{}
	for _, f := range goSources(t, filepath.Join(root, "rt")) {
		for s := range staticStringsFile(t, f) {
			if strings.HasPrefix(s, trapTextPrefix) {
				owned[s] = true
			}
		}
	}
	return owned
}

// trapTextSites maps each owned text to the repo-relative files that spell it.
func trapTextSites(t *testing.T, root string, owned map[string]bool) map[string][]string {
	t.Helper()
	sites := map[string][]string{}
	scanned := 0
	for _, f := range goSources(t, root) {
		scanned++
		rel, err := filepath.Rel(root, f)
		if err != nil {
			t.Fatalf("relativizing %s: %v", f, err)
		}
		rel = filepath.ToSlash(rel)
		for s := range staticStringsFile(t, f) {
			if owned[s] {
				sites[s] = append(sites[s], rel)
			}
		}
	}
	// POSITIVE CONTROL ON THE SCAN ITSELF. A walk that found no files would
	// report every text as having one home, which is the answer this test is
	// looking for — the worst possible failure mode.
	if scanned < 100 {
		t.Fatalf("the scan read %d non-test Go sources across the repository; "+
			"that is too few for the repository, so the walk is broken", scanned)
	}
	return sites
}

// goSources is dir's non-test Go sources, skipping testdata.
func goSources(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "vendor", ".git", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	sort.Strings(out)
	return out
}

func staticStringsFile(t *testing.T, path string) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	got, err := staticStringsIn(path, src)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return got
}

// staticStringsIn is every compile-time-constant string in one Go source.
//
// IT FOLDS `+` OVER ADJACENT LITERALS, which is not optional: rt spells
// `DecimalNonTerminatingText` as two literals joined by `+`, and a copy could
// spell the same text as one. A per-literal scan sees two different strings
// and reports the drift as absent, which is precisely the false negative this
// instrument exists to prevent.
//
// COMMENTS ARE NOT STRINGS, which is why this parses rather than greps. Every
// file here discusses these texts in prose; a textual scan would report each
// discussion as a copy.
func staticStringsIn(path string, src []byte) (map[string]bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.BasicLit:
			if s, ok := staticString(e); ok {
				out[s] = true
			}
		case *ast.BinaryExpr:
			if s, ok := staticString(e); ok {
				out[s] = true
				// The whole concatenation is recorded; its pieces are
				// recorded too by the BasicLit arm as Inspect descends, which
				// is harmless — a fragment is not an owned text.
			}
		}
		return true
	})
	return out, nil
}

func staticString(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		l, lok := staticString(x.X)
		if !lok {
			return "", false
		}
		r, rok := staticString(x.Y)
		if !rok {
			return "", false
		}
		return l + r, true
	case *ast.ParenExpr:
		return staticString(x.X)
	}
	return "", false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedupeSorted(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	cp := append([]string(nil), ss...)
	sort.Strings(cp)
	out := cp[:1]
	for _, s := range cp[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
