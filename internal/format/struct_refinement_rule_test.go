package format

import "testing"

// A struct refinement's rule is its body's last item, on its own line below
// the fields (spec §4 *Refinements*). It stays out of the fields'
// alignment block, a rule over several lines keeps its own `end` as any
// lambda body does, and the comments around it stay where they were.
func TestFormat_StructRefinementRule(t *testing.T) {
	for name, c := range map[string]struct{ src, want string }{
		"one line": {
			"struct Span\n    low: Int\n    high: Int\n    when fn s -> s.low <= s.high\nend\n",
			"struct Span\n    low:  Int\n    high: Int\n    when fn s -> s.low <= s.high\nend\n",
		},
		"a named rule": {
			"struct Span\n    low: Int\n    when ok\nend\n",
			"struct Span\n    low: Int\n    when ok\nend\n",
		},
		"over several lines": {
			"struct Span\n    low: Int\n    highest: Int\n    when fn s ->\n        d = s.highest - s.low\n        d >= 0\n    end\nend\n",
			"struct Span\n    low:     Int\n    highest: Int\n    when fn s ->\n        d = s.highest - s.low\n        d >= 0\n    end\nend\n",
		},
		"comments around the rule": {
			"struct Span\n    low: Int\n\n    // ordered\n    when fn s -> s.low >= 0 // nonnegative\n    // after\nend\n",
			"struct Span\n    low: Int\n\n    // ordered\n    when fn s -> s.low >= 0 // nonnegative\n    // after\nend\n",
		},
	} {
		t.Run(name, func(t *testing.T) { migrates(t, c.src, c.want) })
	}
}
