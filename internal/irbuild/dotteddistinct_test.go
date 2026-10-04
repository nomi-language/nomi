package irbuild

import (
	"strings"
	"testing"
)

// TestDottedDistinct_BothSpellingsRejectArityAlike.
//
// `Meters(5)` and `Day.Hours(48)` are one construct with two syntaxes. Arity
// was the branch this drove through irbuild's shared distinctCallNamed body, but
// the checker now rejects a distinct call with the wrong argument count
// (analysis/distinct_construction.go), so no front-end-legal program reaches
// irbuild's arity backstop. What stays true is that the two spellings are one
// rule: both must be rejected, each naming its own constructor. If the front end
// admits either again, irbuild's `call arity` backstop is live and needs its
// both-spellings test back.
func TestDottedDistinct_BothSpellingsRejectArityAlike(t *testing.T) {
	const decls = "type Day Int\n\ntype Day.Hours Int\n\ntype Meters Int\n\n"
	cases := []struct{ name, call, want string }{
		{"bare", "Meters(1, 2)", "Meters wraps Int, so Meters(...) takes one argument, an Int; got 2"},
		{"namespaced", "Day.Hours(1, 2)", "Day.Hours wraps Int, so Day.Hours(...) takes one argument, an Int; got 2"},
	}
	for _, tc := range cases {
		_, err := AnalyzeSource("main", decls+"test \"t\" {\n  _ = "+tc.call+"\n  assert 1 == 1\n}\n")
		if err == nil {
			t.Fatalf("%s: the front end now ADMITS %s, so irbuild's `call arity` backstop is live and untested", tc.name, tc.call)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %s rejected for another reason, so this is not the arity wall: %v", tc.name, tc.call, err)
		}
	}
}
