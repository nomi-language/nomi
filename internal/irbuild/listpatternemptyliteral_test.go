package irbuild

import "testing"

// A list pattern over the untyped literal `[]`, whose element type nothing
// determines, runs: heads, a suffix, and a pattern that is only a suffix
// (`[..rest]`), which matches every list and so builds no test, over `[]`
// and over a typed list.
func TestIRListPatternOverEmptyLiteral_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn main()
    n = case []
        [_] -> 1
        _ -> 0
    end
    io.inspect(n)
    m = case []
        [] -> "empty"
        [_, ..] -> "more"
    end
    io.inspect(m)
    io.inspect(case []
        [_, _] -> 2
        [..] -> 3
    end)
    io.inspect(case []
        [_, ..rest] -> Iter.count(rest)
        _ -> -1
    end)
    io.inspect(case [1, 2]
        [_, _, _] -> []
        [..rest] -> rest
    end)
end
`
	irRunSource(t, src, "0\n\"empty\"\n3\n-1\n[1, 2]\n")
}
