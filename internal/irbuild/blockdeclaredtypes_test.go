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

// `==` and `!=` on a type declared in a block compare by its shape, though
// a std module declares a type of its name with an impl of Equatable of its
// own: std/calendar's DateTime and OffsetDateTime write theirs by hand,
// std/xml's Token and std/log's Level derive theirs. No impl can name a
// block's type, so none of those is its.
func TestIRBlockDeclaredTypes_EqualityIsTheirOwn(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn main()
    struct DateTime
        instant_nanos: Int
        zone:          String
    end

    struct OffsetDateTime
        name:  String
        table: String
    end

    type Token String

    enum Level
        Debug
        Info
    end

    a = DateTime{instant_nanos: 1, zone: "UTC"}
    b = DateTime{instant_nanos: 1, zone: "CET"}
    io.inspect((a == b, a != b, a == a, Equatable.equal(a, b)))
    n = OffsetDateTime{name: "book", table: "a"}
    m = OffsetDateTime{name: "book", table: "b"}
    io.inspect((n == m, n != m, n == n, Equatable.equal(n, m)))
    io.inspect((Token("a") == Token("b"), Token("a") != Token("a")))
    io.inspect((Level.Debug == Level.Info, Level.Info != Level.Info))
end
`
	const want = "(False, True, True, False)\n(False, True, True, False)\n(False, False)\n(False, False)\n"
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

// A type declared in a body under a std type's name has that type's
// structural operations and nothing of the std type's: equality, a hash, a
// Map key, a Set element and Debug, in a function, a nested fn and a
// lambda, and under one name in two functions, each its own type.
func TestIRBlockDeclaredTypes_StdNamesAreTheirOwn(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn durations() -> (Bool, Int, Int)
    struct Duration
        n: Int
    end
    a = Duration{n: 1}
    b = Duration{n: 2}
    m = {a => 1, b => 2}
    s = Set[a, b, a]
    (a == b, Map.get(m, b)!, Set.size(s))
end

fn dates() -> String
    fn inner() -> Bool
        enum Date
            Today
            Later Int
        end
        Hashable.hash(Date.Later(1)) == Hashable.hash(Date.Later(1))
    end
    f = fn x: Int ->
        type Instant Int
        Debug.inspect([Instant(x), Instant(x + 1)])
    end
    "${inner()} ${f(3)}"
end

fn other() -> String
    enum Duration
        Short
        Long
    end
    Debug.inspect(Duration.Long)
end

fn main()
    io.inspect(durations())
    io.print(dates())
    io.print(other())
end
`
	const want = "(False, 2, 2)\nTrue [Instant(3), Instant(4)]\nLong\n"
	irRunSource(t, src, want)
}

// A generic type a block declares beside a module-level one of its name is
// the block's where the checker typed a use as the block's: a struct
// literal, a struct's call form and an enum's variant call inside the
// block build the block's instance, and the module's generic function
// called there, and every use after the block, the module's.
func TestIRBlockDeclaredTypes_AGenericBesideTheModulesOfItsName(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct Box<T>
    v: T
end

enum Opt<T>
    Has T
    Empty
end

fn make<T>(x: T) -> Box<T>
    Box{v: x}
end

fn main()
    b = Box{v: 1}
    if b.v > 0 then
        struct Box<T>
            w: T
        end
        enum Opt<T>
            Got T
            Gone
        end
        io.inspect(Box{w: "inner"})
        io.inspect(Box({w: 4}))
        io.inspect(Opt.Got(2))
        io.inspect(make(3))
    end
    io.inspect(b)
    io.inspect(Opt.Has(5))
    io.inspect(make("outer"))
end
`
	const want = "Box{w: \"inner\"}\nBox{w: 4}\nGot(2)\nBox{v: 3}\nBox{v: 1}\nHas(5)\nBox{v: \"outer\"}\n"
	irRunSource(t, src, want)
}
