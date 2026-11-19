package vmhost_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// notAllowedLines is each call p reaches from r that lists does not allow:
// its name, whose list it is over ("program" for the program's, "own" after
// a module's own list), its site, the list, and "entry denies too" and
// "program denies too" when a module's call is over the project's lists
// as well.
func notAllowedLines(p *vmhost.Program, r vmhost.Roots, lists *vmhost.Capabilities) []string {
	var out []string
	for _, n := range p.OutsideCallsNotAllowed(r, lists) {
		over := n.Over
		if over == "" {
			over = "program"
		}
		if n.Declared {
			over += " own"
		}
		line := fmt.Sprintf("%s | %s | %s | [%s]", n.Name, over, n.Site, strings.Join(n.Allowed, " "))
		if n.EntryDenies {
			line += " | entry denies too"
		}
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
	return pricingProjectDeclaring(t, "")
}

// pricingProjectDeclaring is pricingProject with table added to the
// dependency's own nomi.toml.
func pricingProjectDeclaring(t *testing.T, table string) *vmhost.Program {
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
		"pricing/nomi.toml": "[module]\nname = \"pricing\"\n" + table,
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
		{"everything allowed", vmhost.Capabilities{Std: []string{"std::fs", "std::io"}}, nil},
		{"the program's list, a module with no entry", vmhost.Capabilities{Std: []string{"std::io"}}, []string{
			"std::fs | program | main.nomi:8:17 | [std::io]",
			"std::fs | program | rules.nomi:7:17 | [std::io]",
		}},
		{"nothing allowed", vmhost.Capabilities{Std: []string{}}, []string{
			"std::fs | program | main.nomi:8:17 | []",
			"std::io | program | main.nomi:12:13 | []",
			"std::io | program | main.nomi:13:13 | []",
			"std::fs | program | rules.nomi:7:17 | []",
			"std::io | program | rules.nomi:11:13 | []",
		}},
		{"a module's own list", vmhost.Capabilities{Std: []string{"std::fs", "std::io"}, Modules: map[string][]string{"example.com/pricing": {"std::io"}}}, []string{
			"std::fs | example.com/pricing | rules.nomi:7:17 | [std::io]",
		}},
		{"a module allowed nothing, the callback it is handed the program's", vmhost.Capabilities{Std: []string{"std::fs", "std::io"}, Modules: map[string][]string{"example.com/pricing": {}}}, []string{
			"std::fs | example.com/pricing | rules.nomi:7:17 | []",
			"std::io | example.com/pricing | rules.nomi:11:13 | []",
		}},
		{"over both lists, once", vmhost.Capabilities{Std: []string{"std::io"}, Modules: map[string][]string{"example.com/pricing": {"std::io"}}}, []string{
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

// A dependency whose own nomi.toml has a [capabilities] table is held to
// its std, beside the project's lists: within it the program passes,
// over it the call is reported over the module's own list. With the
// project's entry too a call must be in both, and a call over both is
// reported once, over the module's own list, saying the entry denies it
// too; a longer entry is no error. Its std_in_tests is not read. With no
// project lists, nothing is enforced, the dependency's table included.
func TestOutsideCallsNotAllowed_ADependencysOwnList(t *testing.T) {
	both := []string{"std::fs", "std::io"}
	cases := []struct {
		name, table string
		lists       vmhost.Capabilities
		want        []string
	}{
		{"within its own list", "\n[capabilities]\nstd = [\"std::fs\", \"std::io\"]\n", vmhost.Capabilities{Std: both}, nil},
		{"over its own list", "\n[capabilities]\nstd = [\"std::io\"]\nstd_in_tests = [\"std::fs\"]\n", vmhost.Capabilities{Std: both}, []string{
			"std::fs | example.com/pricing own | rules.nomi:7:17 | [std::io]",
		}},
		{"a table with no std allows nothing", "\n[capabilities]\n", vmhost.Capabilities{Std: both}, []string{
			"std::fs | example.com/pricing own | rules.nomi:7:17 | []",
			"std::io | example.com/pricing own | rules.nomi:11:13 | []",
		}},
		{"in its own list, not in the project's entry", "\n[capabilities]\nstd = [\"std::fs\", \"std::io\"]\n",
			vmhost.Capabilities{Std: both, Modules: map[string][]string{"example.com/pricing": {"std::io"}}}, []string{
				"std::fs | example.com/pricing | rules.nomi:7:17 | [std::io]",
			}},
		{"in the project's entry, not in its own list", "\n[capabilities]\nstd = [\"std::io\"]\n",
			vmhost.Capabilities{Std: both, Modules: map[string][]string{"example.com/pricing": both}}, []string{
				"std::fs | example.com/pricing own | rules.nomi:7:17 | [std::io]",
			}},
		{"its entry for itself is its list, not its std", "\n[capabilities]\nstd = [\"std::fs\", \"std::io\"]\n\n[capabilities.modules]\n\"example.com/pricing\" = [\"std::io\"]\n",
			vmhost.Capabilities{Std: both}, []string{
				"std::fs | example.com/pricing own | rules.nomi:7:17 | [std::io]",
			}},
		{"within its entry for itself", "\n[capabilities]\nstd = [\"std::fs\", \"std::io\", \"std::http\"]\n\n[capabilities.modules]\n\"example.com/pricing\" = [\"std::fs\", \"std::io\"]\n",
			vmhost.Capabilities{Std: both}, nil},
		{"over every list, once", "\n[capabilities]\nstd = [\"std::io\"]\n",
			vmhost.Capabilities{Std: []string{"std::io"}, Modules: map[string][]string{"example.com/pricing": {"std::io"}}}, []string{
				"std::fs | program | main.nomi:8:17 | [std::io]",
				"std::fs | example.com/pricing own | rules.nomi:7:17 | [std::io] | entry denies too | program denies too",
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkNotAllowed(t, pricingProjectDeclaring(t, c.table), vmhost.Roots{}, &c.lists, c.want...)
		})
	}
	p := pricingProjectDeclaring(t, "\n[capabilities]\nstd = []\n")
	if got := p.OutsideCallsNotAllowed(vmhost.Roots{}, nil); got != nil {
		t.Errorf("no project lists: %v, want nothing", got)
	}
}

// Tests are held to std and std_in_tests together; main to std
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
	lists := &vmhost.Capabilities{Std: []string{"std::io"}, StdInTests: []string{"std::fs"}}
	checkNotAllowed(t, p, vmhost.Roots{}, lists)
	checkNotAllowed(t, p, vmhost.Roots{Exports: true}, lists,
		"std::process | program | main.nomi:8:20 | [std::io]",
	)
	checkNotAllowed(t, p, vmhost.Roots{Tests: &vmhost.TestOptions{}}, lists)
	checkNotAllowed(t, p, vmhost.Roots{Tests: &vmhost.TestOptions{}}, &vmhost.Capabilities{Std: []string{"std::io"}},
		"std::fs | program | main.nomi:17:21 | [std::io]",
	)
}

// The project's own files are held by its entry under
// [capabilities.modules], keyed by its own module path, beside std: within
// it the program passes, over it the call is reported over the project's
// path, and a dependency's calls are held by its own entry, not the
// project's.
func TestOutsideCallsNotAllowed_TheProjectsOwnEntry(t *testing.T) {
	p := pricingProject(t)
	all := []string{"std::fs", "std::io"}
	cases := []struct {
		name  string
		lists vmhost.Capabilities
		want  []string
	}{
		{"within its entry", vmhost.Capabilities{Std: all, Modules: map[string][]string{"example.com/app": all}}, nil},
		{"over its entry", vmhost.Capabilities{Std: all, Modules: map[string][]string{"example.com/app": {"std::io"}}}, []string{
			"std::fs | example.com/app | main.nomi:8:17 | [std::io]",
		}},
		{"over its entry and the program's list", vmhost.Capabilities{Std: []string{"std::io"}, Modules: map[string][]string{"example.com/app": {}}}, []string{
			"std::fs | example.com/app | main.nomi:8:17 | [] | program denies too",
			"std::io | example.com/app | main.nomi:12:13 | []",
			"std::io | example.com/app | main.nomi:13:13 | []",
			"std::fs | program | rules.nomi:7:17 | [std::io]",
		}},
		{"a dependency's entry beside it", vmhost.Capabilities{Std: all, Modules: map[string][]string{"example.com/app": all, "example.com/pricing": {"std::io"}}}, []string{
			"std::fs | example.com/pricing | rules.nomi:7:17 | [std::io]",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkNotAllowed(t, p, vmhost.Roots{}, &c.lists, c.want...)
		})
	}
}

// The project's entry describes the code it ships: main is held by it, and
// the test cases by std and std_in_tests alone.
func TestOutsideCallsNotAllowed_TheProjectsEntryHoldsNoTests(t *testing.T) {
	p := loadProject(t, "main.nomi", map[string]string{
		"go.mod":    "module example.com/app\n\ngo 1.27.0\n",
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"main.nomi": `import
    std::fs
    std::io
end

fn main()
    io.print("hi")
end

test "reads"
    _ = fs.read_text("x")
    assert 1 == 1
end
`})
	lists := &vmhost.Capabilities{Std: []string{"std::io"}, StdInTests: []string{"std::fs"}, Modules: map[string][]string{"example.com/app": {}}}
	checkNotAllowed(t, p, vmhost.Roots{}, lists,
		"std::io | example.com/app | main.nomi:7:13 | []",
	)
	checkNotAllowed(t, p, vmhost.Roots{Tests: &vmhost.TestOptions{}}, lists)
}
