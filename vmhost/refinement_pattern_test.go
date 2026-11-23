package vmhost_test

import "testing"

// A refinement's pattern, `Port(n)`, matches the value itself, since a
// refinement's value is its base's (spec §4 *Refinements*), in every position a
// pattern takes: a `case` arm, a payload nested in a variant pattern, a
// binding with an `else`, a pattern `if` and an `is` test, with a name, a
// `_` or a literal inside it.
func TestRefinementPattern_RunsInEveryPosition(t *testing.T) {
	got := runSourceOutput(t, `import std::io

refine Port Int when fn port -> port >= 1 and port <= 65535

fn arm(p: Port) -> Int
    case p
        Port(80) -> 0
        Port(q) -> q + 1
    end
end

fn whole(p: Port) -> Int
    case p
        Port(q) -> q
    end
end

fn nested(m: Option<Port>) -> Int
    Some(Port(n)) = m else return 0
    n
end

fn pattern_if(m: Option<Port>) -> Int
    if Some(Port(n)) = m then
        n
    else
        -1
    end
end

fn nested_arm(m: Option<Port>) -> String
    case m
        Some(Port(443)) -> "https"
        Some(Port(n)) -> "port ${n}"
        None -> "none"
    end
end

fn is_port(m: Option<Port>) -> Bool
    m is Some(Port(_))
end

fn is_http(m: Option<Port>) -> Bool
    m is Some(Port(80))
end

fn main()
    io.inspect(arm(Port(80)))
    io.inspect(arm(Port(8080)))
    io.inspect(whole(Port(22)))
    io.inspect(nested(Some(Port(81))))
    io.inspect(nested(None))
    io.inspect(pattern_if(Some(Port(82))))
    io.inspect(pattern_if(None))
    io.inspect(nested_arm(Some(Port(443))))
    io.inspect(nested_arm(Some(Port(83))))
    io.inspect(nested_arm(None))
    io.inspect(is_port(Some(Port(84))))
    io.inspect(is_port(None))
    io.inspect(is_http(Some(Port(80))))
    io.inspect(is_http(Some(Port(85))))
end
`)
	want := `0
8081
22
81
0
82
-1
"https"
"port 83"
"none"
True
False
True
False
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
