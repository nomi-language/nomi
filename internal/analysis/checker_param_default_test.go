package analysis_test

import "testing"

// A parameter default is checked against the parameter's declared type, in a
// `fn`, in an annotated lambda, and in a reduce seed. Before, a default under
// an annotation was never checked at all: each of these was accepted.
func TestParamDefault_WrongTypeIsRejected(t *testing.T) {
	for name, src := range map[string]string{
		"fn": `fn f(x: Int = "s"): Int {
  x
}`,
		"lambda": `fn main(): Unit {
  g = |a: Int = "t", b: Int| a + b
}`,
		"reduce seed": `fn main(): Unit {
  n = Iter.reduce([1, 2], |c: Int = "u", x| c + x)
}`,
	} {
		t.Run(name, func(t *testing.T) {
			expectErrorContaining(t, checkWithStdlib(src), "is String, expected Int")
		})
	}
}

// A default of the parameter's type, including an owner-level `once` and an
// empty list at an annotated element type, is accepted.
func TestParamDefault_RightTypeIsAccepted(t *testing.T) {
	src := `struct Count {
  n: Int
}

impl Count {
  once zero: Count = Count{n: 0}
}

fn bump(by: Int, c: Count = Count.zero, seen: List<Int> = []): Count {
  Count{n: c.n + by}
}

fn main(): Unit {
  total = Iter.reduce([1, 2], |c: Count = Count.zero, x| Count{n: c.n + x})
  add = |by: Int, c: Count = Count.zero| Count{n: c.n + by}
}`
	expectClean(t, checkWithStdlib(src))
}
