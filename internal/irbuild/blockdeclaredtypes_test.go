package irbuild

import "testing"

// A type declared in a block runs wherever the block stands: a statement
// block, nested blocks whose inner declarations name the outer ones, a
// call's argument, a binding's value, an `if` or `case` arm, a lambda body
// and an Iter.map callback.
func TestIRBlockDeclaredTypes_RunAnywhere(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn pick(c: Bool): Int {
    if c {
        struct A {
            n: Int
        }
        A{n: 1}.n
    } else {
        enum B {
            One
            Two Int
        }
        case B.Two(5) {
            .One -> 0
            .Two(n) -> n
        }
    }
}

fn main() {
    {
        struct P<T> {
            a: T
        }
        type Meters Int
        io.inspect(P{a: Meters(3)})
        {
            struct Inner {
                p: P<Int>
            }
            fn get(i: Inner): Int {
                i.p.a
            }
            io.inspect(get(Inner{p: P{a: 4}}))
        }
    }
    x = {
        struct C {
            c: Int
        }
        C{c: 6}.c
    }
    io.inspect(x)
    io.inspect({
        struct D {
            d: Int
        }
        D{d: 7}
    })
    io.inspect(pick(True))
    io.inspect(pick(False))
    f = |x: Int| {
        struct W {
            w: Int
        }
        W{w: x}.w * 2
    }
    io.inspect(f(21))
    xs = [1, 2]
    |> Iter.map(|x| {
        struct V {
            v: Int
        }
        V{v: x + 1}.v
    })
    |> Iter.to_list()
    io.inspect(xs)
    io.inspect(case Some(8) {
        Some(n) -> {
            struct S {
                s: Int
            }
            S{s: n}.s
        }
        None -> 0
    })
}
`
	const want = "P{a: Meters(3)}\n4\n6\nD{d: 7}\n1\n5\n42\n[2, 3]\n8\n"
	irRunSource(t, src, want)
}

// The same in a test body: a statement block, a nested one, an `if` arm and
// a `case` arm, each declaring a type its assertions build.
func TestIRBlockDeclaredTypes_InATestBody(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `test "statement blocks declare types" {
    {
        struct P {
            a: Int
        }
        assert P{a: 1}.a == 1
        {
            enum E {
                X Int
            }
            assert case E.X(P{a: 2}.a) {
                .X(n) -> n
            } == 2
            Unit
        }
    }
    if True {
        struct Q {
            q: Int
        }
        assert Q{q: 3}.q == 3
        Unit
    }
    case Some(4) {
        Some(n) -> {
            struct R {
                r: Int
            }
            assert R{r: n}.r == 4
            Unit
        }
        None -> Unit
    }
}
`
	irTestBodyVM(t, src, 1)
}
