package analysis_test

import "testing"

// A parameter default is checked against the parameter's declared type, in a
// `fn`, in an annotated lambda, and in a lambda passed as an argument. Before, a default under
// an annotation was never checked at all: each of these was accepted.
func TestParamDefault_WrongTypeIsRejected(t *testing.T) {
	for name, src := range map[string]string{
		"fn": `fn f(x: Int = "s") -> Int
  x
end`,
		"lambda": `fn main() -> Unit
  g = fn a: Int = "t", b: Int -> a + b
end`,
		"argument lambda": `fn main() -> Unit
  _ = Iter.map([1, 2], fn c: Int = "u" -> c + 1) |> Iter.to_list()
end`,
	} {
		t.Run(name, func(t *testing.T) {
			expectErrorContaining(t, checkWithStdlib(src), "is String, expected Int")
		})
	}
}

// A default of the parameter's type, including an owner-level `once` and an
// empty list at an annotated element type, is accepted.
func TestParamDefault_RightTypeIsAccepted(t *testing.T) {
	src := `struct Count
  n: Int
end

impl Count
  once zero: Count = Count{n: 0}
end

fn bump(by: Int, c: Count = Count.zero, seen: List<Int> = []) -> Count
  Count{n: c.n + by}
end

fn main() -> Unit
  total = Iter.reduce([1, 2], Count.zero, fn c: Count, x -> Count{n: c.n + x})
  add = fn by: Int, c: Count = Count.zero -> Count{n: c.n + by}
end`
	expectClean(t, checkWithStdlib(src))
}
