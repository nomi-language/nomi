package vmhost_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// notAllowedLines is each call p reaches from r that lists does not allow:
// its name, whose list it is over ("program" for the program's), its site,
// the list, and "program denies too" when a module's call is over both.
func notAllowedLines(p *vmhost.Program, r vmhost.Roots, lists *vmhost.Capabilities) []string {
	var out []string
	for _, n := range p.OutsideCallsNotAllowed(r, lists) {
		over := n.Over
		if over == "" {
			over = "program"
		}
		line := fmt.Sprintf("%s | %s | %s | [%s]", n.Name, over, n.Site, strings.Join(n.Allowed, " "))
		if n.ProgramDenies {
			line += " | program denies too"
		}
		out = append(out, line)
	}
	return out
}

func checkNotAllowed(t *testing.T, p *vmhost.Program, r vmhost.Roots, lists *vmhost.Capabilities, want ...string) {
	t.Helper()
	got := notAllowedLines(p, r, lists)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("not allowed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// pricingProject is an app that calls a dependency that reads a file and
// prints, hands it a callback that reads a file, and prints itself.
func pricingProject(t *testing.T) *vmhost.Program {
	return loadProject(t, "app/main.nomi", map[string]string{
		"app/go.mod":    "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/pricing v0.0.0\n\nreplace example.com/pricing => ../pricing\n",
		"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"app/main.nomi": `import
    std::fs
    std::io
    pricing::rules
end

fn read(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end

fn main()
    io.print(rules.load("prices.txt"))
    io.print(rules.twice("x", read))
end
`,
		"pricing/go.mod":    "module example.com/pricing\n\ngo 1.27.0\n",
		"pricing/nomi.toml": "[module]\nname = \"pricing\"\n",
		"pricing/rules.nomi": `import
    std::fs
    std::io
end

pub fn load(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end

pub fn twice<T>(x: T, f: (T) -> T) -> T
    io.print("twice")
    f(f(x))
end
`,
	})
}

// The program's list holds every call, whichever module writes it, and a
// list with nothing reaches nothing; a module with an entry is held by
// its own list too, and a call over both is reported once, over the
// module's. A module with no entry is held by the program's list alone,
// and a callback the program hands a module is the program's.
func TestOutsideCallsNotAllowed_TheProgramAndModuleRules(t *testing.T) {
	p := pricingProject(t)
	cases := []struct {
		name  string
		lists vmhost.Capabilities
		want  []string
	}{
		{"everything allowed", vmhost.Capabilities{Allow: []string{"std::fs", "std::io"}}, nil},
		{"the program's list, a module with no entry", vmhost.Capabilities{Allow: []string{"std::io"}}, []string{
			"std::fs | program | main.nomi:8:17 | [std::io]",
			"std::fs | program | rules.nomi:7:17 | [std::io]",
		}},
		{"nothing allowed", vmhost.Capabilities{Allow: []string{}}, []string{
			"std::fs | program | main.nomi:8:17 | []",
			"std::io | program | main.nomi:12:13 | []",
			"std::io | program | main.nomi:13:13 | []",
			"std::fs | program | rules.nomi:7:17 | []",
			"std::io | program | rules.nomi:11:13 | []",
		}},
		{"a module's own list", vmhost.Capabilities{Allow: []string{"std::fs", "std::io"}, Modules: map[string][]string{"example.com/pricing": {"std::io"}}}, []string{
			"std::fs | example.com/pricing | rules.nomi:7:17 | [std::io]",
		}},
		{"a module allowed nothing, the callback it is handed the program's", vmhost.Capabilities{Allow: []string{"std::fs", "std::io"}, Modules: map[string][]string{"example.com/pricing": {}}}, []string{
			"std::fs | example.com/pricing | rules.nomi:7:17 | []",
			"std::io | example.com/pricing | rules.nomi:11:13 | []",
		}},
		{"over both lists, once", vmhost.Capabilities{Allow: []string{"std::io"}, Modules: map[string][]string{"example.com/pricing": {"std::io"}}}, []string{
			"std::fs | program | main.nomi:8:17 | [std::io]",
			"std::fs | example.com/pricing | rules.nomi:7:17 | [std::io] | program denies too",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkNotAllowed(t, p, vmhost.Roots{}, &c.lists, c.want...)
		})
	}
	if got := p.OutsideCallsNotAllowed(vmhost.Roots{}, nil); got != nil {
		t.Errorf("no lists: %v, want nothing", got)
	}
}

// Tests are held to allow and allow_in_tests together; main to allow
// alone. Exported functions are roots when asked for.
func TestOutsideCallsNotAllowed_TestsAndExports(t *testing.T) {
	p := loadProject(t, "main.nomi", map[string]string{"main.nomi": `import
    std::fs
    std::io
    std::process.{self, Command}
end

export fn shout(s: String) -> String
    _ = process.run(Command{program: "say"})
    s
end

fn main()
    io.print("hi")
end

test "reads"
    _ = fs.read_text("x")
    assert 1 == 1
end
`})
	lists := &vmhost.Capabilities{Allow: []string{"std::io"}, AllowInTests: []string{"std::fs"}}
	checkNotAllowed(t, p, vmhost.Roots{}, lists)
	checkNotAllowed(t, p, vmhost.Roots{Exports: true}, lists,
		"std::process | program | main.nomi:8:20 | [std::io]",
	)
	checkNotAllowed(t, p, vmhost.Roots{Tests: &vmhost.TestOptions{}}, lists)
	checkNotAllowed(t, p, vmhost.Roots{Tests: &vmhost.TestOptions{}}, &vmhost.Capabilities{Allow: []string{"std::io"}},
		"std::fs | program | main.nomi:17:21 | [std::io]",
	)
}
