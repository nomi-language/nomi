package vmhost_test

import "testing"

// A generic type named before its declaration is built carries the
// declaration's variants, fields and methods. An instance (`Result<Int,
// String>`) copies them from the declaration when the annotation is
// resolved, and each case below resolved one before the declaration was
// built, so the copy had none: "variant Ok not found in enum Result" and the
// like, at a `case` arm the program spells correctly.

// std/maybe.nomi and std/results.nomi import each other, and
// `Maybe.to_result`'s signature was resolved before Result had Ok and Err.
func TestDeclarationOrder_MaybeToResultHasResultsVariants(t *testing.T) {
	got := runSourceOutput(t, `import std/io

fn main() {
    m = Some(3)
    case Maybe.to_result(m, "e") {
        Ok(z) -> io.print("ok ${z}")
        Err(e) -> io.print("err ${e}")
    }
    none: Maybe<Int> = None
    if Ok(z) = Maybe.to_result(none, "none") {
        io.print("ok ${z}")
    } else {
        io.print("not ok")
    }
    if Err(e) = Maybe.to_result(none, "none") {
        io.print("err ${e}")
    }
}
`)
	if want := "ok 3\nnot ok\nerr none\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// A struct's fields name a generic struct and a generic enum declared below
// it in the same file.
func TestDeclarationOrder_FieldNamesATypeDeclaredBelow(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct A {
    b: Box<Int>
    e: Shape<Int>
}

struct Box<T> {
    v: T
}

enum Shape<T> {
    Dot(T)
    Nothing
}

fn get(a: A): Int {
    case a.e {
        .Dot(x) -> x + a.b.v
        .Nothing -> a.b.v
    }
}

fn main() {
    io.print("${get(A { b: Box { v: 2 }, e: Shape.Dot(3) })}")
}
`)
	if got != "5\n" {
		t.Fatalf("output %q, want %q", got, "5\n")
	}
}

// A variant's payload names its own enum, which is resolved while the enum
// is being built. No order of building declarations fixes this one.
func TestDeclarationOrder_RecursiveVariantPayloadHasVariants(t *testing.T) {
	got := runSourceOutput(t, `import std/io

enum Tree<T> {
    Leaf(T)
    Node(Tree<T>)
}

fn depth(t: Tree<Int>): Int {
    case t {
        .Leaf(x) -> x
        .Node(inner) -> case inner {
            .Leaf(y) -> y + 100
            .Node(_) -> 0
        }
    }
}

fn main() {
    io.print("${depth(Tree.Node(Tree.Leaf(3)))}")
}
`)
	if got != "103\n" {
		t.Fatalf("output %q, want %q", got, "103\n")
	}
}

// A bound names a generic interface declared below the function. Its
// instance had no methods: "no interface bound of type parameter 'B' (Bag)
// declares function 'size'".
func TestDeclarationOrder_BoundNamesAnInterfaceDeclaredBelow(t *testing.T) {
	got := runSourceOutput(t, `import std/io

fn sum<B>(b: B): Int where B: Bag<Int> {
    B.size(b) + B.first(b)
}

interface Bag<T> {
    fn size(b: self): Int
    fn first(b: self): T
}

struct Three {
    n: Int
}

impl Bag<Int> for Three {
    fn size(_b: Three): Int {
        3
    }

    fn first(b: Three): Int {
        b.n
    }
}

fn main() {
    io.print("${sum(Three { n: 20 })}")
}
`)
	if got != "23\n" {
		t.Fatalf("output %q, want %q", got, "23\n")
	}
}

// An enum embeds a wrapping distinct type declared below it. The enum read
// the distinct type before its inner type was set, took the variant for a
// zero-sized one, and the program was "UserId wraps Int, so UserId(...)
// takes an Int; got UserId" at the enum.
func TestDeclarationOrder_EmbedsADistinctTypeDeclaredBelow(t *testing.T) {
	got := runSourceOutput(t, `import std/io

enum Shape {
    embeds UserId
    Dot
}

type UserId Int

fn main() {
    s: Shape = UserId(5)
    n = case s {
        Shape.UserId(k) -> k + 1
        Shape.Dot -> 0
    }
    io.print("${n}")
    io.print(Debug.inspect(Shape.UserId(8)))
}
`)
	if want := "6\nUserId(8)\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
