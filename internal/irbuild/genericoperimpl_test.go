package irbuild

import "testing"

// An operator impl on a generic receiver lowers at the left operand's
// instance. A user type's block is registered per instance and selected from
// operOrder; a std container's is the impl body instantiated at the element
// type, as `Add.add(a, b)` is.

func TestIRGenericOperImpl_UserType(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Box<T> {
    items: List<T>
}

impl Add<T, Box<T>> for Box<T> {
    fn add(b: Box<T>, rhs: T): Box<T> {
        Box{items: b.items + [rhs]}
    }
}

fn grow<T>(b: Box<T>, x: T): Box<T> {
    b + x
}

fn main() {
    io.print((Box{items: [1]} + 2).items)
    io.print((Box{items: ["a"]} + "b").items)
    io.print(grow(Box{items: [True]}, False).items)
}
`, "[1, 2]\n[a, b]\n[True, False]\n")
}

func TestIRGenericOperImpl_StdSet(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn plus<T>(a: Set<T>, b: Set<T>): Set<T> where T: Hashable {
    a + b
}

fn left(): Set<Int> {
    io.print("left")
    #{1, 2}
}

fn main() {
    io.print(#{1, 2} + #{2, 3})
    io.print(#{1, 2} + #{})
    io.print(#{"a"} + #{"b"})
    io.print(#{1, 2, 3} - #{2})
    io.print(#{1, 2} - #{9})
    io.print(Add.add(#{1}, #{2}))
    io.print(Subtract.subtract(#{1, 2}, #{1}))
    io.print(plus(#{"x"}, #{"y"}))
    io.print(left() + #{3} - #{1})
}
`, "#{1, 2, 3}\n#{1, 2}\n#{a, b}\n#{1, 3}\n#{1, 2}\n#{1, 2}\n#{2}\n#{x, y}\nleft\n#{2, 3}\n")
}

// A std operator body that calls a bare host member of its receiver's inherent
// block (`concat(lhs, rhs)` in Vector's and List's `Add` impls) lowers that
// call as the owner-qualified `Vector.concat` / `List.concat` it names.
func TestIRGenericOperImpl_StdBareHostSibling(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Point {
    x: Int
}

fn join<T>(a: Vector<T>, b: Vector<T>): Vector<T> {
    a + b
}

fn main() {
    a = #[1, 2]
    io.print(a + #[3])
    io.print(Add.add(a, #[3]))
    io.print(#["x"] + #["y", "z"])
    io.print(join(#[True], #[False]))
    io.print(Vector.length(#[Point{x: 1}] + #[Point{x: 2}]))
    io.print(Add.add([1], [2]))
}
`, "#[1, 2, 3]\n#[1, 2, 3]\n#[x, y, z]\n#[True, False]\n2\n[1, 2]\n")
}

// `Map + Map` lowers through std/maps' generic `Add` instance, whose body is
// the owner-qualified `Map.merge`: the right-hand map wins on a shared key.
func TestIRGenericOperImpl_StdMap(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Point {
    x: Int
}

derive Hashable for Point

fn merged<K, V>(a: Map<K, V>, b: Map<K, V>): Map<K, V> {
    a + b
}

fn main() {
    io.print({"a" => 1} + {"a" => 9, "b" => 2})
    io.print(Add.add({"a" => 1}, {"b" => 2}))
    io.print(merged({1 => "x"}, {2 => "y", 1 => "z"}))
    io.print(Map.size({Point{x: 1} => True} + {Point{x: 2} => False}))
    io.print({"p" => Point{x: 1}} + {"p" => Point{x: 2}} == {"p" => Point{x: 2}})
    e: Map<String, Int> = Map.empty()
    io.print(e + e)
}
`, "{a => 9, b => 2}\n{a => 1, b => 2}\n{1 => z, 2 => y}\n2\nTrue\n{=>}\n")
}

// Instant has two `Subtract` impls, told apart by the right operand: `Instant -
// Duration` is an Instant and `Instant - Instant` is a Duration. Operator and
// interface-qualified calls each select the impl by the right operand's type.
func TestIRGenericOperImpl_InstantSubtract(t *testing.T) {
	verifyLambdaProgram(t, `import std/duration.Duration
import std/instant.Instant
import std/io

fn main() {
    a = Instant.from_seconds(15)
    b = Instant.from_seconds(10)
    io.print(Duration.as_nanos(a - b))
    io.print(Duration.as_nanos(b - a))
    io.print(Instant.to_seconds(a - Duration.seconds(5)))
    io.print(Duration.as_nanos(Subtract.subtract(a, b)))
    io.print(Instant.to_seconds(Subtract.subtract(a, Duration.seconds(1))))
    io.print(Instant.to_seconds(a + Duration.seconds(1)))
    io.print(b + (a - b) == a)
}
`, "5000000000\n-5000000000\n10\n5000000000\n14\n16\nTrue\n")
}
