package irbuild

import "testing"

// TestUnsupported_BranchTypesStillDisagree covers the two branch-disagreement
// keys. Every row is rejected by the front end, so no source reaches either
// builder arm. Each row records which diagnostic catches its shape, so if one
// of those front-end checks is relaxed, the row starts reaching the builder
// and fails.
func TestUnsupported_BranchTypesStillDisagree(t *testing.T) {
	cases := []struct{ construct, name, src, frontEnd string }{{
		"case branches with different types",
		"Int against String",
		"fn f(n: Int): Int {\n  case n {\n    0 -> 1\n    _ -> \"two\"\n  }\n}\n\n" +
			"fn main() {\n  _ = f(0)\n}\n",
		"case branch type mismatch",
	}, {
		"case branches with different types",
		"two DIFFERENT typed lists, neither untyped",
		"fn f(n: Int): List<Int> {\n  case n {\n    0 -> [\"a\"]\n    _ -> [1]\n  }\n}\n\n" +
			"fn main() {\n  _ = f(0)\n}\n",
		"list element type mismatch",
	}, {
		"if branches with different types",
		"Int against String",
		"fn f(b: Bool): Int {\n  if b {\n    1\n  } else {\n    \"two\"\n  }\n}\n\n" +
			"fn main() {\n  _ = f(True)\n}\n",
		"if/else branch type mismatch",
	}}
	for _, tc := range cases {
		t.Run(tc.construct+"/"+tc.name, func(t *testing.T) {
			_, err := AnalyzeSource("main", tc.src)
			requireFrontEndRejection(t, err, tc.construct+" / "+tc.name, tc.frontEnd)
		})
	}
}
