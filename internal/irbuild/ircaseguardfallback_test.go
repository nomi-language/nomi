package irbuild

import (
	"testing"
)

// TestIRCaseGuard_GuardsOverAnyPatternRun pins guards the builder once
// declined: over a literal pattern, and a guard that is itself a region. The
// guard runs after the pattern tests and binds, and its failure reaches the
// next arm.
func TestIRCaseGuard_GuardsOverAnyPatternRun(t *testing.T) {
	for _, tc := range []struct{ arm, want string }{
		{"0 when n >= 0 -> 1", "0\n1\n0\n"},
		{"value when (if value > 0 { True } else { False }) -> 1", "1\n0\n0\n"},
	} {
		verifyLambdaProgram(t, "import std/io\nfn choose(n: Int): Int {\n case n {\n"+tc.arm+
			"\n _ -> 0\n }\n}\nfn main() {\n io.print(choose(7))\n io.print(choose(0))\n io.print(choose(-1))\n}\n",
			tc.want)
	}
}
