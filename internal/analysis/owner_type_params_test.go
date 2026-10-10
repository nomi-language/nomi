package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// A function of an inherent block on a generic owner whose signature does
// not name the block's `T` is compiled without it, so its body cannot name
// it: in an annotation, a lambda parameter, a turbofish or a `T.member`
// qualifier. Each is one error at the use, naming the owner and the
// function, with the help that fixes it.
func TestOwnerTypeParam_UseInBodyOfFunctionThatDoesNotDetermineIt_Rejected(t *testing.T) {
	errs := checkWithStdlib(`
struct Box<T> {
  v: T
}

impl Box<T> {
  fn ann(): Int {
    xs: List<T> = []
    Iter.count(xs)
  }
  fn lam(): Int {
    f = |x: T| x
    Iter.count([f])
  }
  fn turbo(): Int {
    Iter.repeat<T>(todo) |> Iter.take(0) |> Iter.count()
  }
  fn qual(): String {
    T.to_string(todo)
  }
}

pub interface Src<T> {
  fn get(s: self): T
}

impl Src<T> {
  pub fn seven(): Int {
    _f = |x: T| x
    7
  }
}
`)
	var got []string
	for _, e := range errs {
		if strings.Contains(e.Message, "not locally determined") {
			// The annotation's own cascade: the binding has no type.
			continue
		}
		got = append(got, fmt.Sprintf("%d:%d %s | %s", e.Line, e.Col, e.Message, strings.Join(e.Hints, "; ")))
	}
	help := func(fn string) string {
		return fmt.Sprintf("use `T` in the signature of `%s`, or declare the function's own type parameter (`fn %s<T>(...)`)", fn, fn)
	}
	want := []string{
		"8:14 `T` is `Box`'s type parameter, and `ann`'s signature does not determine it | " + help("ann"),
		"12:13 `T` is `Box`'s type parameter, and `lam`'s signature does not determine it | " + help("lam"),
		"16:17 `T` is `Box`'s type parameter, and `turbo`'s signature does not determine it | " + help("turbo"),
		"19:5 `T` is `Box`'s type parameter, and `qual`'s signature does not determine it | " + help("qual"),
		"29:14 `T` is `Src`'s type parameter, and `seven`'s signature does not determine it | " + help("seven"),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The shapes std's `impl Iter<T>` uses check clean on a user owner: a
// function that names the block's `T`, one that redeclares `T` (its own
// shadows the block's, and its body names its own), one with no type
// parameter at all, and one with its own differently named parameters.
func TestOwnerTypeParam_IterShapes_Accepted(t *testing.T) {
	errs := checkWithStdlib(`
pub interface Seq<T> {
  fn each_while(collection: self, yield: (T) -> Bool): Bool
}

impl Seq<T> {
  pub fn find<T>(source: List<T>, f: (T) -> Bool): Maybe<T> {
    out: List<T> = Iter.filter(source, f) |> Iter.to_list()
    List.head(out)
  }
  pub fn from(start: Int): List<Int> {
    [start, start + 1]
  }
  pub fn to_map<K, V>(pairs: List<(K, V)>): Map<K, V> {
    Iter.to_map(pairs)
  }
  pub fn flatten<U>(source: List<List<U>>): List<U> {
    Iter.flat_map(source, |inner: List<U>| inner) |> Iter.to_list()
  }
}

struct Box<T> {
  v: T
}

impl Box<T> {
  fn get(b: Box<T>): T {
    v: T = b.v
    v
  }
  fn seven(): Int { 7 }
}

fn main() {
  _ = Seq.find([1, 2], |x| x > 1)
  _ = Seq.from(3)
  _ = Seq.to_map([("a", 1)])
  _ = Seq.flatten([[1], [2]])
  _ = Box.get(Box{v: 1})
  _ = Box.seven()
  _ = Iter.from(3)
  _ = Iter.repeat(1)
}
`)
	if len(errs) != 0 {
		var got []string
		for _, e := range errs {
			got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
		}
		t.Fatalf("want no errors, got:\n%s", strings.Join(got, "\n"))
	}
}

// An impl block of a generic type names the type's parameters: `impl Box`
// for `struct Box<T>` is the arity error at the receiver, with the header to
// write instead, for an inherent block and an interface's alike.
func TestImplReceiver_GenericTypeWithoutItsParameters_Rejected(t *testing.T) {
	errs := checkWithStdlib(`
struct Box<T> {
  v: T
}

struct Pair<A, B> {
  a: A
  b: B
}

impl Box {
  fn f(x: Int): Int { x }
}

impl Display for Pair {
  fn to_string(_p: Pair<Int, Int>): String { "pair" }
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s | %s", e.Line, e.Col, e.Message, strings.Join(e.Hints, "; ")))
	}
	want := []string{
		"11:6 `Box` expects 1 type argument, got 0 | an impl block of a generic type names its type parameters: write `impl Box<T>`",
		"15:18 `Pair` expects 2 type arguments, got 0 | an impl block of a generic type names its type parameters: write `impl Display for Pair<A, B>`",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
