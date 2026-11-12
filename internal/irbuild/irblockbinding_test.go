package irbuild

import "testing"

func TestIRBlockBinding_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
 total = begin
     a = 10
     b = 20
     a + b
 end
 dbg total
 first = begin
     item = 3
     io.print(item)
     item + 1
 end
 second = begin
     item = 8
     nested = begin
         value = 2
         value * 3
     end
     item + nested
 end
 item = 100
 io.print(first)
 io.print(second)
 io.print(item)
 base = 5
 calculate = fn n: Int ->
     answer = begin
         offset = base + n
         offset * 2
     end
     answer + 1
 end
	io.print(calculate(3))
end`, "dbg line 8: total = 30\n3\n4\n14\n100\n17\n")
}

func TestIRBlockBinding_EscapingCallable(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
 f = begin
     base = 4
     fn n: Int -> base + n
 end
 g = begin
     base = 10
     fn n: Int -> base * n
 end
 io.print(f(3))
 io.print(g(3))
 io.print(f(5))
end`, "7\n30\n9\n")
}

// A block bound to a name whose value branches: its tail is an `if`, an
// `else if` chain or a `case` whose arms write the binding, at any depth of
// nested blocks, with defers, an arm that returns from the function, an
// arm that is itself a block, and an empty list given its type by the
// binding.
func TestIRBlockBinding_BranchingTail(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

enum Shape
    Circle Int
    Square Int
end

fn note(s: String) -> Unit
    io.print(s)
end

fn classify(n: Int) -> String
    label = begin
        small = n < 10
        if small then
            "small"
        else if n < 100 then
            "medium"
        else
            "large"
        end
    end
    "${n} is ${label}"
end

fn area(s: Shape) -> Int
    a: Int = begin
        scale = 2
        case s
            Shape.Circle(r) -> 3 * r * r * scale
            Shape.Square(w) -> w * w * scale
        end
    end
    a + 1
end

fn first_even(xs: List<Int>) -> Maybe<Int>
    found = begin
        evens = Iter.filter(xs, fn x -> x % 2 == 0) |> Iter.to_list()
        case List.head(evens)
            Some(e) -> Some(e * 10)
            None -> None
        end
    end
    found
end

fn early(n: Int) -> Int
    v = begin
        m = n * 2
        if m > 10 then
            return 0
        else
            m
        end
    end
    v + 1
end

fn nested(n: Int) -> Int
    v = begin
        a = n + 1
        begin
            b = a * 2
            w = begin
                c = b + 1
                if c > 5 then c else 0 - c
            end
            case w
                0 -> 100
                _ -> w
            end
        end
    end
    v
end

fn deferred(n: Int) -> String
    v = begin
        defer note("block done")
        case n
            1 ->
                defer note("arm done")
                "one"
            _ -> "other"
        end
    end
    v
end

fn main()
    io.print(classify(3))
    io.print(classify(42))
    io.print(classify(500))
    io.inspect(area(Shape.Circle(2)))
    io.inspect(area(Shape.Square(3)))
    io.inspect(first_even([1, 3, 4, 6]))
    io.inspect(first_even([1, 3]))
    io.inspect(early(3))
    io.inspect(early(30))
    io.inspect(nested(1))
    io.inspect(nested(5))
    io.print(deferred(1))
    io.print(deferred(2))
    f = fn x: Int ->
        y = begin
            z = x + 1
            if z > 2 then "big" else "small"
        end
        y
    end
    io.print(f(1))
    io.print(f(5))
    xs: List<Int> = begin
        base = [1, 2]
        if Iter.count(base) > 5 then [] else List.concat(base, [3])
    end
    io.inspect(xs)
    empty: List<Int> = begin
        n = 0
        if n == 0 then [] else [n]
    end
    io.inspect(empty)
end
`, "3 is small\n42 is medium\n500 is large\n25\n19\nSome(40)\nNone\n7\n0\n-5\n13\n"+
		"arm done\nblock done\none\nblock done\nother\nsmall\nbig\n[1, 2, 3]\n[]\n")
}

// A block bound to a name written as std's Display: the block's Int, String
// or branching tail is boxed as the interface the binding names.
func TestIRBlockBinding_AnInterfaceTypedValue(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn show(n: Int) -> String
    d: Display = begin
        doubled = n * 2
        if doubled > 10 then
            "big"
        else
            doubled
        end
    end
    "${d}"
end

fn main()
    d: Display = begin
        x = 2
        x + 3
    end
    io.print("${d}")
    io.print(show(2))
    io.print(show(20))
end
`, "5\n4\nbig\n")
}
