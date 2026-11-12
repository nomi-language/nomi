package irbuild

import "testing"

// A nested fn declared to return an interface, and a lambda whose expected
// result is one, run: the body's value, each arm of a tail `if` or `case`,
// and each early `return` enter the existential, as a module function's
// do. The lambdas stand under an annotated binding and as an argument.
func TestIRNestedFnExistentialResult_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn apply(f: (Int) -> Display, n: Int) -> String
    Display.to_string(f(n))
end

fn main()
    fn plain() -> Display
        5
    end

    fn arms(c: Bool) -> Display
        if c then "yes" else "no"
    end

    fn cased(n: Int) -> Display
        case n
            0 -> 1.5
            _ -> 2.5
        end
    end

    fn early(n: Int) -> Display
        if n > 0 then
            return n
        end
        "negative"
    end

    io.print(Display.to_string(plain()))
    io.print(Display.to_string(arms(True)))
    io.print(Display.to_string(arms(False)))
    io.print(Display.to_string(cased(0)))
    io.print(Display.to_string(cased(1)))
    io.print(Display.to_string(early(4)))
    io.print(Display.to_string(early(-4)))
    f: (Bool) -> Display = fn c -> if c then 1 else 2
    io.print(Display.to_string(f(False)))
    io.print(apply(fn n -> n * 3, 5))
    io.print(apply(fn n -> if n > 0 then "pos" else "neg", 1))
end
`
	const want = "5\n" +
		"yes\n" +
		"no\n" +
		"1.5\n" +
		"2.5\n" +
		"4\n" +
		"negative\n" +
		"2\n" +
		"15\n" +
		"pos\n"
	irRunSource(t, src, want)
}
