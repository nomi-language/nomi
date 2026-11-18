package irbuild

import "testing"

// irDisplayRecordSource renders a value of each kind a hole's record can
// name an impl for: a program type's own impl, a derived one, a std type's,
// a generic type's at an instance, a tuple with a part that has an impl,
// and in a generic function, the same types at each instance.
const irDisplayRecordSource = `import std::io

struct Bad
    why: String
end

impl Display for Bad
    fn to_string(b: Bad) -> String
        "bad: ${b.why}"
    end
end

struct Odd
    n: Int
end

derive Display for Odd

struct Box<T>
    item: T
end

impl Display for Box<T> where T: Display
    fn to_string(b: Box<T>) -> String
        "box(${b.item})"
    end
end

fn shown<T>(x: T) -> String where T: Display
    "<${x}>"
end

fn pair<T>(x: T) -> String where T: Display
    "${(1, x)}"
end

fn main()
    io.print("${Bad{why: "no"}} ${Odd{n: 3}} ${'q'} ${(1, Bad{why: "t"})} ${Box{item: Odd{n: 4}}}")
    io.print(shown(Bad{why: "g"}))
    io.print(shown(Odd{n: 5}))
    io.print(shown('z'))
    io.print(shown(Box{item: 7}))
    io.print(shown(2))
    io.print(pair(Bad{why: "p"}))
end
`

// A hole renders through the impl function the checker recorded for it
// (analysis.DisplayRender.Method), reached from its declaration, and a hole
// of a type parameter through the one the checker answers at each
// instance's types (analysis.FileAnalysis.DisplayAt).
func TestIRDisplayRecord_AHoleRendersByItsRecord(t *testing.T) {
	verifyLambdaProgram(t, irDisplayRecordSource,
		"bad: no Odd{n: 3} q (1, bad: t) box(Odd{n: 4})\n"+
			"<bad: g>\n<Odd{n: 5}>\n<z>\n<box(7)>\n<2>\n(1, bad: p)\n")
}
