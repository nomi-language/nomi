package irbuild

import "testing"

// A Range, a String, Bytes and a type's own `impl Iter` entering an
// `Iter<Display>` are viewed at their own element, each element already
// the interface's value: a parameter, an annotated binding.
func TestIRViewEntersAnInterfaceIter_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct Countdown
    from: Int
end

impl Iter for Countdown
    fn each_while(c: Countdown, yield: (Int) -> Bool) -> Bool
        if c.from == 0 then
            True
        else if yield(c.from) then
            each_while(Countdown{from: c.from - 1}, yield)
        else
            False
        end
    end
end

fn show(xs: Iter<Display>) -> List<String>
    xs |> Iter.map(&Display.to_string(&1)) |> Iter.to_list()
end

fn main()
    io.inspect(show(1..3))
    xs: Iter<Display> = 1..=2
    io.inspect(Iter.count(xs))
    io.inspect(show("ab"))
    io.inspect(show(Bytes.from_list([])))
    io.inspect(show(Countdown{from: 2}))
end
`
	irRunSource(t, src, "[\"1\", \"2\"]\n2\n[\"a\", \"b\"]\n[]\n[\"2\", \"1\"]\n")
}

// An Iter whose elements are already an interface's values is that
// interface's Iter unchanged (relabels): a stage's result bound at
// `Iter<Display>`, and a function's `Iter<Int>` passed to one.
func TestIRIterRelabelsIntoAnInterfaceIter_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn nums() -> Iter<Int>
    [1, 2] |> Iter.map(&(&1 * 3))
end

fn show(xs: Iter<Display>) -> List<String>
    xs |> Iter.map(&Display.to_string(&1)) |> Iter.to_list()
end

fn main()
    s: Iter<Display> = Iter.repeat(1) |> Iter.take(2)
    io.inspect(show(s))
    io.inspect(show(nums()))
end
`
	irRunSource(t, src, "[\"1\", \"1\"]\n[\"3\", \"6\"]\n")
}
