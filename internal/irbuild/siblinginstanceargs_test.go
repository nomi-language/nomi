package irbuild

import "testing"

// A generic function whose type parameter appears only inside another
// file's generic type in its parameters (`fn show<T>(p: Pair<T>) -> String`)
// solves it from the argument's instance, as for one of its own file's
// types: here Pair and its impl of shape.nomi's Shape are in pair.nomi, and
// the callers are in main.nomi. Through a type-qualified call to a function
// the block writes and to a default it inherits, at a builtin type argument,
// at main.nomi's own struct, and nested in a List.
//
// A mirror of another file's instance carries no type arguments of its own,
// and unifyTypeParams read only those, so T was left unsolved whenever the
// result did not name it, and the call declined as "this call to `show` is
// not supported yet". verifyLambdaProgram fails if the front end rejects
// the program.
func TestSiblingInstanceArgs_SolveAGenericCallersTypeParameter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const shape = `pub interface Shape<T>
    fn first(s: self) -> T

    fn describe(s: self) -> String
        "first of ${Iter.count([first(s)])}"
    end
end
`
	const pair = `import shape.Shape

pub struct Pair<E>
    a: E
    b: E
end

impl Shape for Pair<E>
    fn first(p: Pair<E>) -> E
        p.a
    end
end

pub fn width<E>(p: Pair<E>) -> Int
    Iter.count([p.a, p.b])
end
`
	const main = `import std::io
import pair.{Pair, width}

struct Point
    x: Int
end

fn show<T>(p: Pair<T>) -> String
    Pair.describe(p)
end

fn firsts<T>(ps: List<Pair<T>>) -> List<T>
    ps |> Iter.map(&Pair.first(&1)) |> Iter.to_list()
end

fn total<T>(p: Pair<T>) -> Int
    width(p) + 1
end

fn main()
    io.print(show(Pair{a: 1, b: 2}))
    io.print(show(Pair{a: Point{x: 1}, b: Point{x: 2}}))
    io.print(firsts([Pair{a: "x", b: "y"}, Pair{a: "z", b: "w"}]))
    io.print(total(Pair{a: True, b: False}))
end
`
	verifyLambdaProgram(t, main, "first of 1\nfirst of 1\n[x, z]\n3\n",
		map[string]string{"shape.nomi": shape, "pair.nomi": pair})
}
