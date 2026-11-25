package vmhost

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// dbgReachMain has double, whose one effect is a dbg, and report, which
// prints and holds a dbg.
const dbgReachMain = `import std::io

fn double(n: Int) -> Int
    n * 2 |> dbg
end

fn report(text: String)
    io.print(text)
    dbg text
end

fn main()
    report("${double(2)}")
end
`

// A dbg is an effect, and a hover shows it, named `dbg` and with nothing
// about the lists, which do not name it; std::io beside it reads as
// before. A function whose one effect is a dbg says so alone.
func TestFunctionReach_ADbg(t *testing.T) {
	const module = "[package]\nname = \"app\"\nentry_points = [\"main\"]\n"
	dir := writeProject(t, map[string]string{
		"main.nomi": dbgReachMain,
		"nomi.toml": module + "\n[effects.self]\neffects = [\"std::io\"]\n",
	})
	p, err := Load(filepath.Join(dir, "main.nomi"))
	if err != nil {
		t.Fatalf("a dbg under a list that allows std::io is refused: %v", err)
	}
	for _, tc := range []struct {
		name, needle string
		want         []string
	}{
		{"a function whose one effect is a dbg", "double(n", []string{"Has effects: `dbg`."}},
		{"a call of it", "double(2", []string{"Has effects: `dbg`."}},
		{"a function that prints and holds a dbg", "report(text", []string{"Has effects: `dbg`, `std::io`."}},
		{"the dbg itself", "dbg text", []string{"Has effects: `dbg`."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hoverReach(t, p, dir, "main.nomi", tc.needle, 0); !slices.Equal(got, tc.want) {
				t.Errorf("hover on %q ends with\n%s\nwant\n%s", tc.needle, strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// A host's list does not name a dbg either: a program with one loads and
// runs under no names, and prints its line.
func TestWithEffects_ADbgNeedsNoName(t *testing.T) {
	p, err := LoadSource("main", "fn main()\n    dbg 1 + 1\nend\n", WithEffects())
	if err != nil {
		t.Fatalf("a dbg under a host list of no names is refused: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil || !strings.Contains(out.String(), "dbg line 2: 1 + 1 = 2") {
		t.Fatalf("run: %q, %v", out.String(), err)
	}
}
