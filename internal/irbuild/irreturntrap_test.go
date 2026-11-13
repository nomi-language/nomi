package irbuild

import "testing"

// A `return todo` or `break todo` settles no result: the lambda's other
// exits do, a bare `return` and a value among them, and a function's
// `return todo` arm sits beside a value arm.
func TestIRReturnOfATrapSettlesNothing_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn g(x: Int) -> Int
    if x > 5 then
        return x
    else
        return todo
    end
end

fn main()
    f = fn x: Int ->
        if x > 5 then
            return todo
        end
        if x > 3 then
            return
        end
        io.print("bare")
    end
    f(1)
    k = fn x: Int ->
        if x > 5 then
            return todo
        end
        if x > 3 then
            return 2
        end
        x
    end
    io.inspect(k(4))
    io.inspect(g(6))
    xs = [1, 2, 3]
        |> Iter.map(fn x ->
            if x > 5 then
                break todo
            end
            if x > 2 then
                break x
            end
            x * 2
        end)
        |> Iter.to_list()
    io.inspect(xs)
end
`
	irRunSource(t, src, "bare\n2\n6\n[2, 4, 3]\n")
}
