package vmhost_test

import (
	"fmt"
	"testing"
)

// An interface default is lowered once per implementor, so every value the
// checker types as the default's `self`, or as a type holding it, is the
// implementor there: a lambda's parameter, a binding, a list element, a pipe
// stage, a `case` arm's binding and a `List<self>` or `Maybe<self>`
// parameter's contents. A value annotated with the interface's name stays an
// existential.

// defaultSelfLambdaIface leaves a "%s" where defaultSelfLambdaPrinted goes.
const defaultSelfLambdaIface = `interface Temperature {
    fn unit(t: self) -> String
%s
    fn units(t: self) -> String {
        [t, t] |> Iter.map(fn c -> Temperature.unit(c)) |> String.join(",")
    }

    fn kept(t: self) -> Int {
        [t, t, t] |> Iter.filter(fn c -> Temperature.unit(c) != "K") |> Iter.count()
    }

    fn joined(t: self) -> String {
        [t, t, t] |> Iter.reduce(fn acc = "", c -> "${acc}${Temperature.unit(c)}")
    }

    fn looped(t: self) -> String {
        #(c, n) = Iter.loop(fn state = #(t, 0) -> {
            #(c, n) = state
            if n == 2 {
                break state
            }
            #(c, n + 1)
        })
        "${Temperature.unit(c)}${n}"
    }

    fn named(t: self) -> String {
        f = fn c = t -> "<${Temperature.unit(c)}>"
        g = fn -> [t] |> Iter.map(f) |> String.join("")
        "${f(t)}${g()}"
    }

    fn nested(t: self) -> String {
        [t, t]
            |> Iter.map(fn a -> [a, a] |> Iter.map(fn b -> Temperature.unit(b)) |> String.join("+"))
            |> String.join(",")
    }

    fn total(_t: self, ts: List<self>) -> Decimal where self: Distinct<Decimal> {
        ts
            |> Iter.map(fn c -> {
                self(n) = c
                n
            })
            |> Iter.reduce(fn acc = 0d, n -> acc + n)
    }

    fn sum(_t: self, ts: List<self>) -> Decimal where self: Distinct<Decimal> {
        ts |> Iter.map(fn self(n) -> n) |> Iter.reduce(fn acc = 0d, n -> acc + n)
    }

    fn doubled(_t: self, ts: List<self>) -> List<self> where self: Distinct<Decimal> {
        ts
            |> Iter.map(fn c -> case c {
                self(n) -> self(n * 2d)
            })
            |> Iter.to_list()
    }

    fn first(t: self) -> String {
        cs = [t, t]
        head = cs |> Iter.map(fn c -> c) |> Iter.to_list() |> List.head()
        case head {
            Some(c) -> "first ${Temperature.unit(c)}"
            None -> "none"
        }
    }

    fn pick(t: self, m: Maybe<self>) -> String {
        case m {
            Some(c) -> [c, t] |> Iter.map(fn x -> Temperature.unit(x)) |> String.join("")
            None -> "-"
        }
    }

    fn chained(t: self) -> String {
        [t] |> Iter.map(fn c -> Temperature.units(c)) |> String.join(";")
    }
}
`

// A default that names its own file's `io` import is not lowered from a
// sibling file's impl (portableDefault), so only the one-file program has it.
const defaultSelfLambdaPrinted = `
    fn printed(t: self) {
        [t, t] |> Iter.each(fn c -> io.print("each ${Temperature.unit(c)}"))
    }
`

const defaultSelfLambdaImpls = `type Celsius Decimal
type Fahrenheit Decimal

impl Temperature for Celsius {
    fn unit(_t: Celsius) -> String {
        "C"
    }
}

impl Temperature for Fahrenheit {
    fn unit(_t: Fahrenheit) -> String {
        "F"
    }
}

fn main() {
    c = Celsius(1d)
    f = Fahrenheit(2d)
    io.print(Celsius.units(c))
    io.print(Temperature.units(f))
    io.print(Celsius.kept(c))
    io.print(Fahrenheit.kept(f))
%s    io.print(Celsius.joined(c))
    io.print(Fahrenheit.joined(f))
    io.print(Celsius.looped(c))
    io.print(Fahrenheit.looped(f))
    io.print(Celsius.named(c))
    io.print(Fahrenheit.named(f))
    io.print(Celsius.nested(c))
    io.print(Fahrenheit.nested(f))
    io.inspect(Celsius.total(c, [Celsius(1d), Celsius(2.5d)]))
    io.inspect(Fahrenheit.total(f, [Fahrenheit(3d)]))
    io.inspect(Celsius.sum(c, [Celsius(1d), Celsius(2d)]))
    io.inspect(Fahrenheit.sum(f, []))
    io.inspect(Celsius.doubled(c, [Celsius(1d), Celsius(2.5d)]))
    io.inspect(Fahrenheit.doubled(f, [Fahrenheit(3d)]))
    io.print(Celsius.first(c))
    io.print(Fahrenheit.first(f))
    io.print(Celsius.pick(c, Some(Celsius(5d))))
    io.print(Fahrenheit.pick(f, None))
    io.print(Celsius.chained(c))
    io.print(Fahrenheit.chained(f))
}
`

const defaultSelfLambdaWant = "C,C\nF,F\n3\n3\n%sCCC\nFFF\nC2\nF2\n" +
	"<C><C>\n<F><F>\nC+C,C+C\nF+F,F+F\n3.5d\n3d\n3d\n0d\n[Celsius(2d), Celsius(5.0d)]\n[Fahrenheit(6d)]\n" +
	"first C\nfirst F\nCC\n-\nC,C\nF,F\n"

func TestDefaultSelfLambda_Runs(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{
		"main.nomi": "import std/io\n\n" + fmt.Sprintf(defaultSelfLambdaIface, defaultSelfLambdaPrinted) + "\n" +
			fmt.Sprintf(defaultSelfLambdaImpls, "    Celsius.printed(c)\n    Fahrenheit.printed(f)\n"),
	})
	want := fmt.Sprintf(defaultSelfLambdaWant, "each C\neach C\neach F\neach F\n")
	if got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// The same defaults, declared in a sibling file and inherited by impls in
// the entry file.
func TestDefaultSelfLambda_InterfaceInASiblingFile(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{
		"temps.nomi": "pub " + fmt.Sprintf(defaultSelfLambdaIface, ""),
		"main.nomi":  "import {\n    std/io\n    temps.{Temperature}\n}\n\n" + fmt.Sprintf(defaultSelfLambdaImpls, ""),
	})
	want := fmt.Sprintf(defaultSelfLambdaWant, "")
	if got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// `other: Temperature` in the same default is an existential: a lambda over
// it, and a list mixing it with nothing of `self`, dispatch on the value's
// own implementor, while the `self` values beside it are the implementor
// the default is lowered for.
func TestDefaultSelfLambda_InterfaceValueStaysExistential(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std/io

interface Temperature {
    fn unit(t: self) -> String

    fn against(t: self, other: Temperature) -> String {
        mine = [t] |> Iter.map(fn c -> Temperature.unit(c)) |> String.join("")
        theirs = [other, other] |> Iter.map(fn o -> Temperature.unit(o)) |> String.join("")
        "${mine} vs ${theirs} vs ${Temperature.unit(other)}"
    }
}

struct Celsius {
    v: Int
}

struct Kelvin {
    v: Int
}

impl Temperature for Celsius {
    fn unit(_t: Celsius) -> String {
        "C"
    }
}

impl Temperature for Kelvin {
    fn unit(_t: Kelvin) -> String {
        "K"
    }
}

fn main() {
    io.print(Celsius.against(Celsius{v: 1}, Kelvin{v: 2}))
    io.print(Kelvin.against(Kelvin{v: 2}, Celsius{v: 1}))
    io.print(Temperature.against(Celsius{v: 1}, Celsius{v: 3}))
}
`})
	want := "C vs KK vs K\nK vs CC vs C\nC vs CC vs C\n"
	if got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
