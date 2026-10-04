package irbuild

import "testing"

// TestUnsupported_DestructureShapesStillRefused covers the parameter patterns
// destructure's `default:` arm would receive. Only list and map patterns reach
// it, and both are refutable (a list pattern's arity is not in its type, a map
// pattern's keys may be absent), so the checker rejects both in a parameter
// with "refutable pattern in parameter; bind the parameter and use a `case` in
// the body".
//
// The rows record which diagnostic catches each shape, and the assertion is on
// the diagnostic text: if one of those checks is relaxed, the row starts
// reaching the builder and fails here.
func TestUnsupported_DestructureShapesStillRefused(t *testing.T) {
	cases := []struct{ name, src, frontEnd string }{{
		"a list pattern: its arity is not in the type, so it is refutable",
		"fn main() {\n  _ = |[a, b]: List<Int>| a + b\n}\n",
		"refutable pattern in parameter",
	}, {
		"a map pattern: its keys may be absent, so it is refutable",
		"fn main() {\n  _ = |{\"a\" => v}: Map<String, Int>| v\n}\n",
		"refutable pattern in parameter",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AnalyzeSource("main", tc.src)
			requireFrontEndRejection(t, err, tc.name, tc.frontEnd)
		})
	}
}
