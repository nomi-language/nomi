package irbuild

import (
	"sort"
	"strings"
	"sync"
	"testing"
)

// Every decline names its reason. A BLOCKED line that read "no decline reason
// recorded" told the user nothing; irDeclineClose now records a reason that
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
	return "declined without naming a reason (call irDeclineNote at the decline): " + strings.Join(names, ", ")
}

// An attempt that declines with no note gets the builder-bug reason, and the
// hook hears of it; one that noted a reason keeps it.
func TestIRDecline_AnUnnamedDeclineIsRecordedAndReported(t *testing.T) {
	var heard []string
	prevHook, prevObserved := irDeclineUnnamed, IRDeclineObserved
	reasons := map[string]string{}
	IRDeclineObserved = func(fn, reason string) { reasons[fn] = reason }
	irDeclineUnnamed = func(fn string) { heard = append(heard, fn) }
	defer func() { irDeclineUnnamed, IRDeclineObserved = prevHook, prevObserved }()

	g := &gen{}
	g.irDeclineOpen("silent")
	irDeclineClose()
	g.irDeclineOpen("named")
	irDeclineNote("a named reason")
	irDeclineClose()

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
