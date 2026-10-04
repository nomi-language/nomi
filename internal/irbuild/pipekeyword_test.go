package irbuild

import (
	"strings"
	"testing"
)

// TestPipeKeyword_OperandIsEvaluatedOnce pins ONCE-ness absolutely, because a
// comparison against a recorded output proves nothing about a bug the record
// already holds — see differential_test.go's DIFFED caveat. A `case` stage's
// scrutinee is tested against every arm, so a builder that spliced the operand
// NODE rather than a held temporary would run it once per arm.
func TestPipeKeyword_OperandIsEvaluatedOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	obs := vmReference(fixture("pipe_keyword_stages.nomi"))
	vmSkipIfKnownBlocked(t, obs)
	for _, tag := range []string{"eval case", "eval if", "eval kw"} {
		if n := strings.Count(obs.stdout, tag+"\n"); n != 1 {
			t.Errorf("%q appears %d times in the reference output, want exactly 1:\n%s",
				tag, n, obs.stdout)
		}
	}
}
