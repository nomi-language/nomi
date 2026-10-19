package irbuild

import "testing"

// A receiverless interface function (`fn zero(n: Int) -> self`, as
// Summable's `empty_sum`) called through a bounded type parameter names
// its impl by the signature the checker records for the call, `(Int) -> T`:
// no operand carries a T. Inside the instance `Box<Box<String>>`, the
// inner `T.zero(n)` is `Box<String>`'s, and in `make_all<T>` it is the
// caller's T. Each is called with a turbofish, through an annotation, and
// in a lambda and a placeholder.
func TestVM_ReceiverlessInterfaceFunctionAtAGenericInstance(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

interface Zero {
    fn zero(n: Int) -> self
}

impl Zero for Int {
    fn zero(n: Int) -> Int {
        n
    }
}

impl Zero for String {
    fn zero(n: Int) -> String {
        "s${Int.to_string(n)}"
    }
}

struct Box<T> {
    v: T
}

impl Zero for Box<T> where T: Zero {
    fn zero(n: Int) -> Box<T> {
        #Box{v: T.zero(n)}
    }
}

fn make_all<T>(ns: List<Int>) -> List<T> where T: Zero {
    Iter.map(ns, &T.zero(&1)) |> Iter.to_list()
}

fn make_all_lambda<T>(ns: List<Int>) -> List<T> where T: Zero {
    Iter.map(ns, fn n -> T.zero(n)) |> Iter.to_list()
}

fn main() {
    io.inspect(Zero.zero<Box<Int>>(1))
    io.inspect(Zero.zero<Box<Box<String>>>(2))
    nested: Box<Box<Int>> = Zero.zero(3)
    io.inspect(nested)
    io.inspect(Iter.map([4, 5], fn n -> Zero.zero<Box<Box<Int>>>(n)) |> Iter.to_list())
    io.inspect(Iter.map([6], &Zero.zero<Box<Box<String>>>(&1)) |> Iter.to_list())
    boxes: List<Box<String>> = make_all([7, 8])
    io.inspect(boxes)
    deep: List<Box<Box<Int>>> = make_all_lambda([9])
    io.inspect(deep)
}
`
	irRunSource(t, src, `#Box{v: 1}
#Box{v: #Box{v: "s2"}}
#Box{v: #Box{v: 3}}
[#Box{v: #Box{v: 4}}, #Box{v: #Box{v: 5}}]
[#Box{v: #Box{v: "s6"}}]
[#Box{v: "s7"}, #Box{v: "s8"}]
[#Box{v: #Box{v: 9}}]
`)
}
