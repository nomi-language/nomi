package analysis_test

import (
	"testing"
)

// A literal pattern is its literal's type, so the value at its position must
// have that type, at every depth a pattern can appear.
func TestLiteralPatternMustMatchTheValuesType(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"string arm over Int", `fn f(n: Int): Int {
  case n {
    "a" -> 1
    _ -> 2
  }
}
`, `pattern "a" is a String, but the value is an Int`},
		{"int arm over String", `fn f(s: String): Int {
  case s {
    3 -> 1
    _ -> 2
  }
}
`, `pattern 3 is an Int, but the value is a String`},
		{"negative int over Float", `fn f(x: Float): Int {
  case x {
    -3 -> 1
    _ -> 2
  }
}
`, `pattern -3 is an Int, but the value is a Float`},
		{"float over Int", `fn f(n: Int): Int {
  case n {
    2.5 -> 1
    _ -> 2
  }
}
`, `pattern 2.5 is a Float, but the value is an Int`},
		{"decimal over Float", `fn f(x: Float): Int {
  case x {
    1.5d -> 1
    _ -> 2
  }
}
`, `pattern 1.5d is a Decimal, but the value is a Float`},
		{"raw string over Int", "fn f(n: Int): Int {\n  case n {\n    `a` -> 1\n    _ -> 2\n  }\n}\n",
			"pattern `a` is a String, but the value is an Int"},
		{"int over Bool", `fn f(b: Bool): Int {
  case b {
    1 -> 1
    _ -> 2
  }
}
`, `pattern 1 is an Int, but the value is a Bool`},
		{"variant payload", `fn f(m: Maybe<Int>): Int {
  case m {
    Some("z") -> 1
    _ -> 2
  }
}
`, `pattern "z" is a String, but the value is an Int`},
		{"tuple element", `fn f(p: (Int, String)): Int {
  case p {
    (1, 2) -> 1
    _ -> 2
  }
}
`, `pattern 2 is an Int, but the value is a String`},
		{"struct field", `struct P {
  x: Int
}

fn f(p: P): Int {
  case p {
    P { x: "no" } -> 1
    _ -> 2
  }
}
`, `pattern "no" is a String, but the value is an Int`},
		{"list element", `fn f(xs: List<Int>): Int {
  case xs {
    ["q", .._] -> 1
    _ -> 2
  }
}
`, `pattern "q" is a String, but the value is an Int`},
		{"map value", `fn f(m: Map<String, Int>): Int {
  case m {
    { "k" => "v" } -> 1
    _ -> 2
  }
}
`, `pattern "v" is a String, but the value is an Int`},
		{"binding else", `fn f(m: Maybe<Int>): Int {
  Some("q") = m else {
    return 0
  }
  1
}
`, `pattern "q" is a String, but the value is an Int`},
		{"assert pattern", `test "t" {
  assert Some("w") = Some(3)
}
`, `pattern "w" is a String, but the value is an Int`},
		{"lambda parameter", `fn f(): Int {
  g: ((Int, Int)) -> Int = |(a, "x")| a
  g((1, 2))
}
`, `pattern "x" is a String, but the value is an Int`},
		{"generic scrutinee", `fn f<T>(x: T): Int {
  case x {
    1 -> 1
    _ -> 2
  }
}
`, `pattern 1 is an Int, but the value is a T`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			expectStdlibError(t, errs, tc.want)
		})
	}
}

func TestLiteralPatternsOfTheValuesTypeCheck(t *testing.T) {
	_, errs := checkSourceWithStdlib("fn f(n: Int, s: String, x: Float, d: Decimal, p: (Int, String), m: Maybe<String>): Int {\n" +
		"  a = case n {\n    -1 -> 1\n    0x10 -> 2\n    _ -> 3\n  }\n" +
		"  b = case s {\n    \"x\" -> 1\n    `y` -> 2\n    \"\"\"z\"\"\" -> 3\n    _ -> 4\n  }\n" +
		"  c = case x {\n    1.5 -> 1\n    -2.0 -> 2\n    _ -> 3\n  }\n" +
		"  e = case d {\n    1.5d -> 1\n    _ -> 2\n  }\n" +
		"  g = case p {\n    (1, \"a\") -> 1\n    _ -> 2\n  }\n" +
		"  h = case m {\n    Some(\"a\") -> 1\n    _ -> 2\n  }\n" +
		"  a + b + c + e + g + h\n}\n")
	expectNoStdlibErrors(t, errs)
}
