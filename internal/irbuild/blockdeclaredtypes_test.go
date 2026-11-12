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
	const src = `import std::io

fn pick(c: Bool) -> Int
    if c then
        struct A
            n: Int
        end
        A{n: 1}.n
    else
        enum B
            One
            Two Int
        end
        case B.Two(5)
            .One -> 0
            .Two(n) -> n
        end
    end
end

fn main()
    begin
        struct P<T>
            a: T
        end
        type Meters Int
        io.inspect(P{a: Meters(3)})
        begin
            struct Inner
                p: P<Int>
            end
            fn get(i: Inner) -> Int
                i.p.a
            end
            io.inspect(get(Inner{p: P{a: 4}}))
        end
    end
    x = begin
        struct C
            c: Int
        end
        C{c: 6}.c
    end
    io.inspect(x)
    io.inspect(begin
        struct D
            d: Int
        end
        D{d: 7}
    end)
    io.inspect(pick(True))
    io.inspect(pick(False))
    f = fn x: Int ->
        struct W
            w: Int
        end
        W{w: x}.w * 2
    end
    io.inspect(f(21))
    xs = [1, 2]
    |> Iter.map(fn x ->
        struct V
            v: Int
        end
        V{v: x + 1}.v
    end)
    |> Iter.to_list()
    io.inspect(xs)
    io.inspect(case Some(8)
        Some(n) ->
            struct S
                s: Int
            end
            S{s: n}.s
        None -> 0
    end)
end
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
	const src = `test "statement blocks declare types"
    begin
        struct P
            a: Int
        end
        assert P{a: 1}.a == 1
        begin
            enum E
                X Int
            end
            assert case E.X(P{a: 2}.a)
                .X(n) -> n
            end == 2
            Unit
        end
    end
    if True then
        struct Q
            q: Int
        end
        assert Q{q: 3}.q == 3
        Unit
    end
    case Some(4)
        Some(n) ->
            struct R
                r: Int
            end
            assert R{r: n}.r == 4
            Unit
        None -> Unit
    end
end
`
	irTestBodyVM(t, src, 1)
}
