package irbuild

import (
	"testing"
)

func TestIRIterSort_CompleteGenericProgram(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn first_two_sorted<T>(xs: List<T>): List<T> where T: Comparable {
 xs |> Iter.sort() |> Iter.take(2) |> Iter.to_list()
}
fn main() {
 io.inspect(first_two_sorted([3, 1, 4, 1, 5, 9, 2, 6]))
 io.inspect(first_two_sorted(["banana", "apple", "cherry"]))
}
`, "[1, 1]\n[\"apple\", \"banana\"]\n")
}

func TestIRIterSort_StableExplicitComparator(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 xs = [21, 12, 11, 22, 31]
 ys = Iter.sort_with(xs, |a, b| Int.compare(a % 10, b % 10))
 io.inspect(ys)
 io.inspect(xs)
}
`, "[21, 11, 31, 12, 22]\n[21, 12, 11, 22, 31]\n")
}

// A Direction operand retains: Descending compares (b, a), and sort_by
// projects b's key before a's (the comparator's second operand first, as
// std's `compare(key(b), key(a))`), which the printing key makes visible. Equal
// keys keep their input order either way.
func TestIRIterSort_DescendingDirection(t *testing.T) {
	verifyLambdaProgram(t, `import {
 std/io
 std/comparable.Direction
}
fn main() {
 io.inspect(Iter.sort([2, 3, 1], Direction.Descending))
 io.inspect(Iter.sort([2, 3, 1], Direction.Ascending))
 io.inspect(Iter.sort_by([21, 12, 11, 22], Direction.Descending, |n| n % 10))
 io.inspect(Iter.sort_by([2, 1], Direction.Descending, |n| { io.print(n); n }))
}
`, "[3, 2, 1]\n[1, 2, 3]\n[12, 22, 21, 11]\n2\n1\n[2, 1]\n")
}

func TestIRIterSort_TakeIsLazyAndRepeatable(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn empty(): List<Int> { [] }
fn main() {
 xs = Iter.map([3, 1, 2], |n| { io.print(n); n })
 ys = Iter.take(xs, 2)
 io.print("created")
 io.inspect(Iter.to_list(ys))
 io.inspect(Iter.to_list(ys))
 io.inspect(Iter.to_list(Iter.take(xs, 0)))
 io.inspect(Iter.to_list(Iter.take(xs, -1)))
 io.inspect(Iter.sort(empty()))
}
`, "created\n3\n1\n[3, 1]\n3\n1\n[3, 1]\n[]\n[]\n[]\n")
}

func TestIRIterSort_CallbackFault(t *testing.T) {
	verifyIterFault(t, `import std/io
fn main() {
 xs = Iter.map([2, 1], |n| { io.print(n); n })
 io.inspect(Iter.sort_with(xs, |a, b| {
  io.print("compare")
  Int.compare(a / 0, b)
 }))
}
`, "line 6: division by zero", "2\n1\ncompare\n")
}

// Decimal's `compare` is a `host fn`: the sort's comparator is a function
// value forwarding to it (irHostForward), over a Set or a List.
func TestIRIterSort_DecimalsSortByTheirHostCompare(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 io.inspect(Iter.sort(#{2.5d, 1.5d}))
 io.inspect(Iter.sort([2.50d, 10d, 1.5d]))
}
`, "[1.5d, 2.5d]\n[1.5d, 2.50d, 10d]\n")
}
