package irbuild

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// Every decline names its reason. A BLOCKED line that read "no decline reason
// recorded" told the user nothing; the census's close now records a reason that
// blames the builder, and TestMain fails the package for every attempt that
// reached it in any test (unnamedDeclineViolation).

var (
	unnamedDeclinesMu sync.Mutex
	unnamedDeclines   = map[string]bool{}
)

func recordUnnamedDecline(fn string) {
	unnamedDeclinesMu.Lock()
	defer unnamedDeclinesMu.Unlock()
	unnamedDeclines[fn] = true
}

// unnamedDeclineViolation names every attempt that declined without a reason,
// or answers "".
func unnamedDeclineViolation() string {
	unnamedDeclinesMu.Lock()
	defer unnamedDeclinesMu.Unlock()
	if len(unnamedDeclines) == 0 {
		return ""
	}
	var names []string
	for fn := range unnamedDeclines {
		names = append(names, fn)
	}
	sort.Strings(names)
	return "declined without naming a reason (note a reason at the decline): " + strings.Join(names, ", ")
}

// An attempt that declines with no note gets the builder-bug reason, and the
// hook hears of it; one that noted a reason keeps it.
func TestIRDecline_AnUnnamedDeclineIsRecordedAndReported(t *testing.T) {
	var heard []string
	prevHook := irDeclineUnnamed
	reasons := map[string]string{}
	irDeclineUnnamed = func(fn string) { heard = append(heard, fn) }
	defer func() { irDeclineUnnamed = prevHook }()

	g := &gen{dc: newDeclineCensus(DeclineHooks{Observed: func(fn, reason string) { reasons[fn] = reason }})}
	g.irDeclineOpen("silent")
	g.dc.close()
	g.irDeclineOpen("named")
	g.dc.note("a named reason")
	g.dc.close()

	if len(heard) != 1 || heard[0] != "silent" {
		t.Errorf("the hook heard %v; want only the silent attempt", heard)
	}
	if reasons["silent"] != irDeclineUnnamedReason {
		t.Errorf("the silent attempt recorded %q; want %q", reasons["silent"], irDeclineUnnamedReason)
	}
	if reasons["named"] != "a named reason" {
		t.Errorf("the named attempt recorded %q", reasons["named"])
	}
}

// A map pattern's key that declines is reported at the key, with the key's
// reason, whether the pattern is a `case` arm's, a binding's with `else`, or
// an `assert`'s: the callers that gave up on the pattern add no reason.
func TestIRDecline_AMapPatternKeyDeclinesAtTheKey(t *testing.T) {
	plantDecline(t)
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `fn planted_decline(s: String) -> String
    s
end

fn arm(m: Map<String, Int>) -> Int
    case m
        {planted_decline("a") => v} -> v
        _ -> 0
    end
end

fn bound(m: Map<String, Int>) -> Int
    {planted_decline("b") => v} = m else
        return 0
    end
    v
end

test "asserted"
    assert {planted_decline("c") => 1} = {"c" => 1}
end
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects this, so it measures nothing: %v", err)
	}
	var got []*Decline
	p.Declines.At = func(d *Decline) { got = append(got, d) }
	if _, _, err := GenerateIR(p); err != nil {
		t.Fatal(err)
	}
	// A call is placed at its parenthesis.
	want := map[string][2]int{"arm": {7, 25}, "bound": {13, 21}}
	seen := map[string]bool{}
	for _, d := range got {
		pos, ok := want[d.Fn]
		if !ok {
			if strings.Contains(d.Fn, "asserted") {
				pos, ok = [2]int{20, 28}, true
				d.Fn = "asserted"
			}
		}
		if !ok {
			continue
		}
		seen[d.Fn] = true
		if d.Reason != "a planted decline" || d.Line != pos[0] || d.Col != pos[1] || d.Pattern {
			t.Errorf("%s declined %+v; want the planted reason at the key, %d:%d", d.Fn, *d, pos[0], pos[1])
		}
	}
	for _, fn := range []string{"arm", "bound", "asserted"} {
		if !seen[fn] {
			t.Errorf("%s did not decline; got %v", fn, got)
		}
	}
}

// A pattern a callee could not test is reported with the callee's reason, at
// the innermost pattern; a callee that named none is the builder's bug,
// recorded at the pattern as close records one.
func TestIRDecline_APatternFailureKeepsItsCalleesReason(t *testing.T) {
	var heard []string
	prevHook := irDeclineUnnamed
	irDeclineUnnamed = func(fn string) { heard = append(heard, fn) }
	defer func() { irDeclineUnnamed = prevHook }()

	var got []*Decline
	g := &gen{dc: newDeclineCensus(DeclineHooks{At: func(d *Decline) { got = append(got, d) }})}
	inner := &ast.WildcardPattern{Line: 4, Col: 9}
	outer := &ast.TuplePattern{Patterns: []ast.Node{inner}, Line: 4, Col: 8}

	g.irDeclineOpen("named")
	g.dc.note("the callee's reason")
	g.dc.patternFailed(inner)
	g.dc.patternFailed(outer)
	g.dc.close()

	g.irDeclineOpen("silent")
	g.dc.patternFailed(inner)
	g.dc.close()

	if len(got) != 2 {
		t.Fatalf("reported %d declines; want 2", len(got))
	}
	if d := got[0]; d.Reason != "the callee's reason" || d.Line != 4 || d.Col != 9 || !d.Pattern {
		t.Errorf("the named attempt reported %+v; want the callee's reason at the inner pattern, 4:9", *d)
	}
	if d := got[1]; d.Reason != irPatternUnnamedReason || d.Line != 4 || d.Col != 9 || !d.Pattern {
		t.Errorf("the silent attempt reported %+v; want the builder-bug reason at the pattern, 4:9", *d)
	}
	if len(heard) != 1 || heard[0] != "silent" {
		t.Errorf("the unnamed hook heard %v; want only the silent attempt", heard)
	}
}
