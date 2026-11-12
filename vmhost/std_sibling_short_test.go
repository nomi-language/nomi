package vmhost

import (
	"strings"
	"testing"
)

// A std module the process lowers once calls its own functions as any
// module does: omitting a defaulted argument, directly or through a
// capture, from a function body, a `once` initializer, a parameter default
// and a generic instance, and passing an Iter. Each source is appended to
// std/hex and its "matrix" case runs as `nomi test std/hex.nomi` runs it.
func TestStdModuleBodiesCallTheirSiblingsShort(t *testing.T) {
	const add3 = "fn add3(a: Int, b: Int, c: Int = 100) -> Int\n    a + b + c\nend\n\n"
	cases := []struct{ name, src string }{
		{"a function body", add3 +
			"fn make1() -> Int\n    add3(1, 2)\nend\n\n" +
			"test \"matrix\"\n    assert make1() == 103\nend\n"},
		{"a capture", add3 +
			"fn make1() -> Int\n    f = &add3(1, &1)\n    f(2)\nend\n\n" +
			"test \"matrix\"\n    assert make1() == 103\nend\n"},
		{"a once initializer", add3 +
			"once made = add3(1, 2)\n\n" +
			"fn make1() -> Int\n    made\nend\n\n" +
			"test \"matrix\"\n    assert make1() == 103\nend\n"},
		{"a parameter default", add3 +
			"fn make1(n: Int = add3(1, 2)) -> Int\n    n\nend\n\n" +
			"fn make2() -> Int\n    make1()\nend\n\n" +
			"test \"matrix\"\n    assert make2() == 103\nend\n"},
		{"a generic instance", add3 +
			"fn make1<T>(_x: T) -> Int\n    add3(1, 2)\nend\n\n" +
			"fn make2() -> Int\n    make1(\"x\")\nend\n\n" +
			"test \"matrix\"\n    assert make2() == 103\nend\n"},
		{"an Iter argument",
			"fn total(xs: Iter<Int>) -> Int\n    xs |> Iter.sum()\nend\n\n" +
				"fn make1() -> Int\n    total([1, 2, 100])\nend\n\n" +
				"test \"matrix\"\n    assert make1() == 103\nend\n"},
		{"an Iter argument piped",
			"fn total(xs: Iter<Int>) -> Int\n    xs |> Iter.sum()\nend\n\n" +
				"fn make1() -> Int\n    [1, 2, 100] |> Iter.map(&(&1)) |> total()\nend\n\n" +
				"test \"matrix\"\n    assert make1() == 103\nend\n"},
		{"an Iter and a default",
			"fn total(xs: Iter<Int>, plus: Int = 100) -> Int\n    (xs |> Iter.sum()) + plus\nend\n\n" +
				"fn make1() -> Int\n    total([1, 2])\nend\n\n" +
				"test \"matrix\"\n    assert make1() == 103\nend\n"},
		{"a generic Iter",
			"fn total<T>(xs: Iter<T>) -> Int\n    (xs |> Iter.count()) + 100\nend\n\n" +
				"fn make1() -> Int\n    total([1, 2, 3])\nend\n\n" +
				"test \"matrix\"\n    assert make1() == 103\nend\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, from := stdEdited("hex", tc.src)
			lo, k := checkStdLowers("hex", src, from)
			if !lo.accepted {
				t.Fatalf("the front end rejects this, so it says nothing about the lowering:\n%s", lo.rejection)
			}
			if lo.internal != "" || len(lo.declines) > 0 {
				t.Fatalf("the module does not lower:\n%s%s\n%s", lo.internal, strings.Join(lo.declines, "\n"), strings.Join(lo.reasons, "\n"))
			}
			if k.Name == "" || k.Err != nil {
				t.Fatalf("the case does not pass: %v\n%s", k.Err, k.Output)
			}
		})
	}
}
