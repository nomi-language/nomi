package irbuild

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// This file holds the two shapes a row may use when it asks the front end a
// question before asking this builder one, and a test that forbids the third.
//
// A row shaped
//
//	p, err := AnalyzeSource("main", src)
//	if err != nil {
//	    t.Skipf("front end: %v", err)
//	}
//	... assertions about what the builder does with p ...
//
// is invisible the moment `src` stops parsing. The suite reports `ok`, and
// nothing distinguishes "correctly discharged by a stronger wall" from "dead
// since the language changed under it": `--- SKIP` and an absent line are the
// same thing in every summary but `-v`.
//
// So a row must state, in the source, which of the two things it claims:
//
//   - the front end accepts this and the builder's answer is the claim: a
//     front-end error fails the row with t.Fatalf, because a rejection makes
//     the row hold no coverage.
//   - requireFrontEndRejection: the front end rejects this, and that is the
//     claim. The expected diagnostic is pinned, so a rejection for a
//     different reason fails, an acceptance fails, and the row reports a pass
//     backed by a real assertion. The discharge reason goes to t.Logf.

// requireFrontEndRejection pins a row whose claim IS the front-end rejection.
// `want` is a substring of the expected diagnostic and must not be empty: an
// unpinned rejection is the hazard this file exists to remove.
func requireFrontEndRejection(t *testing.T, err error, what, want string) {
	t.Helper()
	if want == "" {
		t.Fatalf("%s pins a front-end rejection with no expected diagnostic, which "+
			"accepts every rejection including one from a source that stopped parsing "+
			"for an unrelated reason", what)
	}
	if err == nil {
		t.Fatalf("the front end ACCEPTS %s, so it reaches this builder and the row's "+
			"discharge reason (%q) does not hold; decide whether the shape is lowerable "+
			"and make the row assert that answer", what, want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("the front end rejects %s with %v, not with %q; the row's discharge "+
			"reason names the wrong diagnostic, so it is pinning a wall that is not "+
			"the one holding this shape", what, err, want)
	}
	t.Logf("discharged by the front end (%s), so the builder is never asked", want)
}

// analyzeCall matches the two entry points a row uses to ask the front end a
// question. `Analyze(` is anchored on the assignment so `AnalyzeSource(` and
// helper names ending in `Analyze` are not double-counted.
var analyzeCall = regexp.MustCompile(`(:?=|:=)\s*Analyze(Source)?\(`)

// TestFrontEndGuards_NoSkipOnAFrontEndError is the structural half, which
// makes the shape enforceable rather than a convention: it reads this
// package's own test sources and fails on the shape above.
//
// A skip on an environment condition (no module root, no go directive, an
// unreadable source tree, an unset opt-in variable) is a different thing and
// is not matched: those guards are conditioned on the machine, not on whether
// a Nomi source still parses, and they cannot go dead from a language change.
//
// The window is the body of the `err != nil` block and nothing else, closed by
// brace indentation. Rows that handle the front-end error with `t.Fatalf` and
// skip later, on what the builder did, are a different question, and the
// narrow window is what separates them.
func TestFrontEndGuards_NoSkipOnAFrontEndError(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("cannot read the package directory, so this guard would pass vacuously: %v", err)
	}
	var offenders []string
	files := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, readErr := os.ReadFile(filepath.Join(".", name))
		if readErr != nil {
			t.Fatalf("cannot read %s: %v", name, readErr)
		}
		files++
		lines := strings.Split(string(b), "\n")
		for i, ln := range lines {
			if !analyzeCall.MatchString(ln) || strings.HasPrefix(strings.TrimSpace(ln), "//") {
				continue
			}
			// The error test is either on the analyze line itself —
			// `if _, err := Analyze(path); err != nil {` — or within the
			// two lines after it. Anything further is not this guard.
			opener := -1
			for j := i; j < len(lines) && j <= i+2; j++ {
				if strings.Contains(lines[j], "err != nil") && strings.HasSuffix(strings.TrimSpace(lines[j]), "{") {
					opener = j
					break
				}
			}
			if opener < 0 {
				continue
			}
			indent := lines[opener][:len(lines[opener])-len(strings.TrimLeft(lines[opener], " \t"))]
			for j := opener + 1; j < len(lines); j++ {
				trimmed := strings.TrimSpace(lines[j])
				if trimmed == "}" && strings.HasPrefix(lines[j], indent+"}") {
					break
				}
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				if strings.Contains(lines[j], "t.Skipf(") || strings.Contains(lines[j], "t.Skip(") {
					offenders = append(offenders, name+":"+strconv.Itoa(j+1)+": "+trimmed)
				}
			}
		}
	}
	if files < 300 {
		t.Fatalf("only %d *_test.go files scanned; this guard is meant to see the whole "+
			"package and a short read would make it pass vacuously", files)
	}
	if len(offenders) > 0 {
		t.Fatalf("%d row(s) skip on a front-end error, which reports `ok` and holds nothing "+
			"once the source stops parsing. Fail with t.Fatalf on the error (the front end "+
			"should accept this) or use requireFrontEndRejection (the rejection IS the claim, "+
			"so pin its diagnostic):\n  %s",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}
