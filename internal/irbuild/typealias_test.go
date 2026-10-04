package irbuild

import (
	"strings"
	"testing"
)

// TestTypeAlias_ACyclicAliasIsTheFrontEndsJob is the claim `aliasResolving`
// rests on, stated as a front-end claim so its expiry is visible.
//
// typealias.go expands an alias by recursing into its target. That is only safe
// because the alias graph is a DAG, and it is a DAG because the checker rejects
// an alias naming a type not yet resolved, INCLUDING itself. Asserted in both
// the direct and the mutual shape.
//
// If this ever stops holding, `aliasResolving` stops being a belt and becomes
// the mechanism, and the test that says so is this one rather than a stack
// overflow in a corpus sweep.
func TestTypeAlias_ACyclicAliasIsTheFrontEndsJob(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"self", "typealias A (A) -> Int\n\nfn run(_f: A): Int {\n  0\n}\n"},
		{"mutual", "typealias A B\ntypealias B A\n\nfn run(_f: A): Int {\n  0\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Analyze(writeTemp(t, tc.src))
			if err == nil {
				t.Fatal("the front end now ACCEPTS a cyclic alias, so typealias.go's " +
					"aliasResolving guard is load-bearing rather than a belt and needs " +
					"exercising directly")
			}
			if !strings.Contains(err.Error(), "unknown type") {
				t.Fatalf("the front end rejects a cycle, but with different words, "+
					"so typealias.go's account of the rule needs re-reading: %v", err)
			}
		})
	}
}
