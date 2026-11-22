package vmhost_test

import "testing"

// `go` and `gopkg` are ordinary names: only the forms a Go binding wrote are
// the parser's error (internal/parser's TestParseRejectsGoBindings). A
// field, a function, a parameter and a local of each name parse, check and
// run, and so do statements that start with one.
func TestGoNames_GoAndGopkgNameAnything(t *testing.T) {
	got := runSourceOutput(t, `import std::io

struct Flags
    go:    Bool
    gopkg: String
end

fn go(gopkg: Int) -> Int
    gopkg + 1
end

fn gopkg(go: Int) -> Int
    go * 2
end

fn ready(n: Int) -> Flags
    go = n == 42
    gopkg = "std"
    Flags{go:, gopkg:}
end

fn main()
    flags = ready(gopkg(go(20)))
    io.print(flags.go)
    io.print(flags.gopkg)
end
`)
	if want := "True\nstd\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
