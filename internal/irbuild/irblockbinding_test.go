package irbuild

import "testing"

func TestIRBlockBinding_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 total = { a = 10 b = 20 a + b }
 dbg total
 first = { item = 3 io.print(item) item + 1 }
 second = { item = 8 nested = { value = 2 value * 3 } item + nested }
 item = 100
 io.print(first)
 io.print(second)
 io.print(item)
 base = 5
 calculate = |n: Int| { answer = { offset = base + n offset * 2 } answer + 1 }
	io.print(calculate(3))
}`, "dbg line 4: total = 30\n3\n4\n14\n100\n17\n")
}

func TestIRBlockBinding_EscapingCallable(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 f = { base = 4 |n: Int| base + n }
 g = { base = 10 |n: Int| base * n }
 io.print(f(3))
 io.print(g(3))
 io.print(f(5))
}`, "7\n30\n9\n")
}

// A block bound to a name whose value branches: its tail is an `if`, an
// `else if` chain or a `case` whose arms write the binding, at any depth of
// nested blocks, with defers, an arm that returns from the function, an
// arm that is itself a block, and an empty list given its type by the
// binding.
func TestIRBlockBinding_BranchingTail(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

enum Shape {
    Circle Int
    Square Int
}

fn note(s: String): Unit {
    io.print(s)
}

fn classify(n: Int): String {
    label = {
        small = n < 10
        if small {
            "small"
        } else if n < 100 {
            "medium"
        } else {
            "large"
        }
    }
    "${n} is ${label}"
}

fn area(s: Shape): Int {
    a: Int = {
        scale = 2
        case s {
            Shape.Circle(r) -> 3 * r * r * scale
            Shape.Square(w) -> w * w * scale
        }
    }
    a + 1
}

fn first_even(xs: List<Int>): Maybe<Int> {
    found = {
        evens = Iter.filter(xs, |x| x % 2 == 0) |> Iter.to_list()
        case List.head(evens) {
            Some(e) -> Some(e * 10)
            None -> None
        }
    }
    found
}

fn early(n: Int): Int {
    v = {
        m = n * 2
        if m > 10 {
            return 0
        } else {
            m
        }
    }
    v + 1
}

fn nested(n: Int): Int {
    v = {
        a = n + 1
        {
            b = a * 2
            w = {
                c = b + 1
                if c > 5 { c } else { 0 - c }
            }
            case w {
                0 -> 100
                _ -> w
            }
        }
    }
    v
}

fn deferred(n: Int): String {
    v = {
        defer note("block done")
        case n {
            1 -> {
                defer note("arm done")
                "one"
            }
            _ -> "other"
        }
    }
    v
}

fn main() {
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
    f = |x: Int| {
        y = {
            z = x + 1
            if z > 2 { "big" } else { "small" }
        }
        y
    }
    io.print(f(1))
    io.print(f(5))
    xs: List<Int> = {
        base = [1, 2]
        if Iter.count(base) > 5 { [] } else { List.concat(base, [3]) }
    }
    io.inspect(xs)
    empty: List<Int> = {
        n = 0
        if n == 0 { [] } else { [n] }
    }
    io.inspect(empty)
}
`, "3 is small\n42 is medium\n500 is large\n25\n19\nSome(40)\nNone\n7\n0\n-5\n13\n"+
		"arm done\nblock done\none\nblock done\nother\nsmall\nbig\n[1, 2, 3]\n[]\n")
}
