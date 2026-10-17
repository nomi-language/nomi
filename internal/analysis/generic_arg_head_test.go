package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// An argument whose outermost type constructor differs from a generic
// parameter's is a mismatch even while the parameter's type variables are
// unsolved: no substitution makes a `Set<T>` of Unit. Before, the generic
// argument check let these through and the program could not run.
func TestGenericArgHead_ADifferentConstructorIsAMismatch(t *testing.T) {
	errs := checkWithStdlib(`
fn first_or<T>(xs: List<T>, d: T) -> T {
  Maybe.with_default(List.head(xs), d)
}

fn main() {
  u = {}
  _ = Set.size(u)
  _ = List.head({})
  _ = Map.size(1.5)
  _ = first_or(3, 1)
  _ = Maybe.some?(4)
  _ = Set.size(#{1})
  _ = first_or([1], 2)
  _ = List.head(1..3 |> Iter.to_list())
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{
		"8:16 argument 1: expected Set<T>, got Unit",
		"9:17 argument 1: expected List<T>, got Unit",
		"10:16 argument 1: expected Map<K, V>, got Float",
		"11:16 argument 1: expected List<T>, got Int",
		"12:19 argument 1: expected Maybe<T>, got Int",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A distinct type's head is as fixed as a struct's: no substitution makes a
// `Vector<T>` of a String or a `Task<T>` of an Int. The generic argument
// check treated a distinct parameter as open and let these through, and the
// IR builder declined `Vector.get("", 0)` (a fuzz find). The last four calls
// are shapes that stay accepted.
func TestGenericArgHead_ADistinctTypeIsAFixedHead(t *testing.T) {
	errs := checkWithStdlib(`
import std/tasks.Task

type UserId Int

fn first<T>(xs: Vector<T>) -> Maybe<T> {
  Vector.get(xs, 0)
}

fn main() {
  _ = Vector.get("", 0)
  _ = Vector.at(1, 0)
  _ = Task.await(1)
  _ = Vector.length([1])
  _ = List.head(#[1])
  _ = first(UserId(1))
  _ = "" |> Vector.length()
  _ = Vector.get(#[1], 0)
  _ = Vector.length(#[])
  _ = Vector.concat(#[1], #[])
  _ = first(#["a"])
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{
		"11:18 argument 1: expected Vector<T>, got String",
		"12:17 argument 1: expected Vector<T>, got Int",
		"13:18 argument 1: expected Task<T>, got Int",
		"14:21 argument 1: expected Vector<T>, got List<Int>",
		"15:17 argument 1: expected List<T>, got Vector<Int>",
		"16:13 argument 1: expected Vector<T>, got UserId",
		"17:13 `Vector.length` takes a Vector<T> as its first argument, but the pipe passes a String",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
