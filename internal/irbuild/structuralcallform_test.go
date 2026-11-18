package irbuild

import "testing"

// A qualified interface call answered structurally (analysis.Callee.Form)
// and the operator or rendering form of the same operation on the same
// type print the same: `Equatable.equal` and `==` on a tuple, an anonymous
// struct and a struct with no impl, in a body and through a generic's
// bound; `Comparable.compare` and `<` on tuples; `Display.to_string` and
// interpolation of a tuple and a record; `Debug.inspect` and `dbg`'s text
// of a tuple; and `Hashable.hash` of a tuple, a record and a struct with no
// impl, which agrees with `==`.
func TestStructuralCallForm_CallAndOperatorAgree(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io

struct Plain
    x: Int
end

fn same<T>(a: T, b: T) -> List<Bool> where T: Equatable
    [Equatable.equal(a, b), a == b]
end

fn main()
    t = (1, "a")
    u = (2, "a")
    r = {a: 1, b: "x"}
    p = Plain{x: 1}
    q = Plain{x: 2}
    io.print("${Equatable.equal(t, u)} ${t == u}")
    io.print("${Equatable.equal(r, r)} ${r == r}")
    io.print("${Equatable.equal(p, q)} ${p == q}")
    io.print("${Comparable.compare(t, u) == Ordering.Less} ${t < u}")
    io.print("${Display.to_string(t)} ${t}")
    io.print("${Display.to_string(r)} ${r}")
    io.print(Debug.inspect(t))
    io.print("${same(p, p)} ${same(t, u)} ${same(r, r)}")
    io.print("${Hashable.hash(t) == Hashable.hash((1, "a"))} ${Hashable.hash(r) == Hashable.hash({b: "x", a: 1})} ${Hashable.hash(p) == Hashable.hash(Plain{x: 1})}")
end
`, `False False
True True
False False
True True
(1, a) (1, a)
{a: 1, b: x} {a: 1, b: x}
(1, "a")
[True, True] [False, False] [True, True]
True True True
`)
}
