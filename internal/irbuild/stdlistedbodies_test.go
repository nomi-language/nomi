package irbuild

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// listedBodiesEnv runs TestStdObjects_HowListedBodiesLowerShared.
const listedBodiesEnv = "NOMI_LISTED_BODIES"

// TestStdObjects_HowListedBodiesLowerShared lowers the bodies of every
// module declared with each program that TestStdObjects_WhatStopsEachListedModule
// finds no cause in (listedModuleStops' "none") as the shared lowering
// lowers its own modules: it takes them out of programStdModules for the
// length of one fresh lowering of the stdlib (buildStdlibIndex) and logs,
// per module, how many of its functions lower and why each other one does
// not, by class: the signature's refusal (stdFunc.why) or the body's
// decline (the index's unlowered record). It pins nothing; its table is
// what moving those modules into the shared lowering has left to do.
//
// It changes process-wide state while it runs, so it runs only on its
// own, with NOMI_LISTED_BODIES=1.
func TestStdObjects_HowListedBodiesLowerShared(t *testing.T) {
	if os.Getenv(listedBodiesEnv) == "" {
		t.Skip("lowers listed modules as shared ones, which changes process-wide state; set " + listedBodiesEnv + "=1 and run it alone")
	}
	var moved []string
	for module, stop := range listedModuleStops {
		if stop == "none" {
			moved = append(moved, module)
		}
	}
	sort.Strings(moved)
	for _, m := range moved {
		delete(programStdModules, m)
	}
	defer func() {
		for _, m := range moved {
			programStdModules[m] = true
		}
	}()
	idx := buildStdlibIndex()
	insts := newStdInstances(idx, newDeclineCensus(DeclineHooks{}))
	total := map[string]int{}
	lowered, all := 0, 0
	for _, module := range moved {
		pkg := idx.modulePkg[module]
		classes := map[string][]string{}
		ok := 0
		for _, f := range idx.modFuncs[module] {
			all++
			if f.lowerable() {
				ok++
				continue
			}
			class := "signature: " + f.why
			if f.why == "stdlib generic function" {
				// Not a decline: each caller instantiates it (stdinst.go).
				class = "generic, instantiated where called"
			}
			if rec, found := idx.unlowered[pkg][f.key]; found {
				// Recorded as "key|reason" (emitStdFunc).
				_, why, _ := strings.Cut(rec, "|")
				class = "body: " + why
				if insts.stdUnretainedMonoBody(f) {
					// Not a decline: it reaches a generic template, and each
					// program builds it as an instance with no arguments.
					class = "built per program: " + why
				}
			}
			classes[class] = append(classes[class], f.key)
		}
		lowered += ok
		var line []string
		names := make([]string, 0, len(classes))
		for c := range classes {
			names = append(names, c)
		}
		sort.Strings(names)
		for _, c := range names {
			total[c] += len(classes[c])
			line = append(line, fmt.Sprintf("%s: %d (%s)", c, len(classes[c]), strings.Join(first(classes[c], 3), ", ")))
		}
		t.Logf("std/%s: %d of %d functions lower; %s", module, ok, len(idx.modFuncs[module]), strings.Join(line, "; "))
	}
	names := make([]string, 0, len(total))
	for c := range total {
		names = append(names, c)
	}
	sort.Slice(names, func(i, j int) bool { return total[names[i]] > total[names[j]] })
	var summary strings.Builder
	for _, c := range names {
		fmt.Fprintf(&summary, "\n  %s: %d", c, total[c])
	}
	t.Logf("%d modules, %d of %d functions lower as shared ones; the rest by class:%s", len(moved), lowered, all, summary.String())
	if len(moved) == 0 || all == 0 {
		t.Fatal("no listed module has no cause, or none declares a function; the table says nothing")
	}
}

// first is at most n of xs.
func first(xs []string, n int) []string {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}
