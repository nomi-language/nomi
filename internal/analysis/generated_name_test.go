package analysis_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// generatedName is how a name the compiler writes into a synthesized body
// starts (`__value_email`, `__err`, `__a0`). A diagnostic raised in such a
// body says the name in the programmer's terms where it moves to the
// declaration the body was written for (repointSynthErrors), so no
// diagnostic shows one.
var generatedName = regexp.MustCompile(`__[a-z]`)

// generatedNameIn answers the first of errs whose message or hint shows a
// generated name, and the text that shows it.
func generatedNameIn(errs []analysis.TypeError) (analysis.TypeError, string, bool) {
	for _, e := range errs {
		for _, text := range append([]string{e.Message}, e.Hints...) {
			if generatedName.MatchString(text) {
				return e, text, true
			}
		}
	}
	return analysis.TypeError{}, "", false
}

// checkNoGeneratedName fails t when one of errs shows a generated name. The
// helpers that check the derive tests' programs and the unknown-type table
// call it on every diagnostic they return.
func checkNoGeneratedName(t testing.TB, errs []analysis.TypeError) {
	t.Helper()
	if e, text, found := generatedNameIn(errs); found {
		t.Errorf("a diagnostic shows a name the compiler wrote: %q (%v)", text, e)
	}
}

// TestFailureRecords_ShowNoGeneratedName holds the recorded transcript of
// every deliberately failing fixture to the same rule as the diagnostics
// the checker's tests see: none shows a name the compiler wrote.
func TestFailureRecords_ShowNoGeneratedName(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "expectations", "failure.expect")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	transcript := 0
	for i, line := range lines {
		if !strings.HasPrefix(line, "T ") {
			continue
		}
		transcript++
		if generatedName.MatchString(line) {
			t.Errorf("%s:%d shows a name the compiler wrote: %s", path, i+1, line)
		}
	}
	if transcript == 0 {
		t.Fatalf("%s holds no transcript line", path)
	}
	t.Logf("%d transcript lines", transcript)
}
