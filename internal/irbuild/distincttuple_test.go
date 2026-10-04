package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTemp puts one in-memory program on disk so Analyze — not AnalyzeSource —
// checks it. The difference matters here: the universal `impl Debug` this test
// is about is SYNTHESIZED by the project-level front end, so AnalyzeSource sees
// a program with no Debug impl at all and the guard would read as a refusal
// rather than as the bare conversion it is looking for.
func writeTemp(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "entry.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A distinct type over a TUPLE constructed FLAT, and a distinct over a
// CONTAINER rendered by `Debug.inspect`.
//
// The two arrive together because clearing the first exposed the second: the
// corpus files carrying `Coord(3, 4)` also carry `type Kvs Map<String, Int>`
// and `type Items List<Int>`, and neither could render until the guard over
// synthesized distinct impls came down.

// TestDistinctTuple_ArityIsTheFrontEndsJobForADistinctToo pins, as a FRONT-END
// claim, that the builder's `call arity` arm for a distinct is a BACKSTOP.
//
// THIS TEST EXISTS BECAUSE IT REFUTED THE COMMENT IT WAS WRITTEN TO CONFIRM.
// enumtuple.go's first draft said the checker was LOOSER for a distinct than
// for a variant, "which is how those fourteen sites arose". Running it says
// otherwise: `Coord(1, 2, 3)` on a 2-tuple distinct is a front-end error, word
// for word "Coord takes 2 arguments, got 3". The fourteen sites arose because
// the BUILDER accepted only one of the two LEGAL counts, not because the
// checker let a wrong one through.
//
// Stated as a claim about the front end rather than skipped past, so the expiry
// event is visible: if the checker ever relaxes, the builder's arm stops being
// a backstop and this test says so at that moment rather than passing quietly.
func TestDistinctTuple_ArityIsTheFrontEndsJobForADistinctToo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entry_test.nomi")
	src := "type Coord (Int, Int)\n\ntest \"t\" {\n  assert Debug.inspect(Coord(1, 2, 3)) == \"x\"\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Analyze(path)
	if err == nil {
		t.Fatal("the front end now ACCEPTS a mismatched distinct-tuple arity, so the " +
			"builder's `call arity` arm is reachable from source and must be exercised " +
			"directly rather than left as a backstop")
	}
	if !strings.Contains(err.Error(), "Coord takes 2 arguments, got 3") {
		t.Fatalf("the front end still rejects this, but with different words, so the "+
			"claim above needs re-reading: %v", err)
	}
}
