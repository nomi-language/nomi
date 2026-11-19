package vmhost_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// notAllowedLines is each call p reaches from r that lists does not allow:
// its name, the list it is over ("self"; "deps", "entry" or "own" and the
// module), its site, what the list allows, and "entry denies too" and
// "deps denies too" when a module's call is over the project's other lists
// as well.
func notAllowedLines(p *vmhost.Program, r vmhost.Roots, lists *vmhost.Capabilities) []string {
	var out []string
	for _, n := range p.OutsideCallsNotAllowed(r, lists) {
		over := map[vmhost.CapabilityList]string{vmhost.OverSelf: "self", vmhost.OverDeps: "deps", vmhost.OverEntry: "entry", vmhost.OverDeclared: "own"}[n.List]
		if n.Module != "" {
			over += " " + n.Module
		}
		line := fmt.Sprintf("%s | %s | %s | [%s]", n.Name, over, n.Site, strings.Join(n.Allowed, " "))
		if n.EntryDenies {
			line += " | entry denies too"
		}
		if n.DepsDenies {
			line += " | deps denies too"
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

// selfAllows is a [capabilities.self] whose std is names.
func selfAllows(names ...string) *vmhost.SelfCapabilities {
	return &vmhost.SelfCapabilities{Std: append([]string{}, names...)}
}

// depsCeiling is a [capabilities.deps] whose std is names, none for nil,
// with entries.
func depsCeiling(names []string, entries map[string][]string) *vmhost.DepsCapabilities {
	d := &vmhost.DepsCapabilities{Modules: entries}
	if names != nil {
		d.Std = append([]string{}, names...)
	}
	return d
}

// pricingProject is an app that calls a dependency that reads a file and
// prints, hands it a callback that reads a file, and prints itself.
func pricingProject(t *testing.T) *vmhost.Program {
	return pricingProjectDeclaring(t, "")
}

// pricingProjectDeclaring is pricingProject with table added to the
// dependency's own nomi.toml.
func pricingProjectDeclaring(t *testing.T, table string) *vmhost.Program {
	return loadProject(t, "app/main.nomi", pricingFiles(table, ""))
}

// pricingFiles is pricingProjectDeclaring's files, with more added to the
// app's main.nomi.
func pricingFiles(table, more string) map[string]string {
	return map[string]string{
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
` + more,
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
	}
}

// [capabilities.self] holds the calls in the project's own files and
// [capabilities.deps] the calls in a dependency's: each alone holds only
// what it names. Deps' std is a ceiling over every dependency, an entry
// under [capabilities.deps.modules] holds that module's calls, and a call
// over both is reported once, over the entry. With no ceiling and no
// entry a dependency's calls are not held. A callback the program hands a
// module is the program's.
func TestOutsideCallsNotAllowed_TheSelfAndDepsTables(t *testing.T) {
	p := pricingProject(t)
	both := []string{"std::fs", "std::io"}
	io := []string{"std::io"}
	cases := []struct {
		name  string
		lists vmhost.Capabilities
		want  []string
	}{
		{"everything allowed", vmhost.Capabilities{Self: selfAllows(both...), Deps: depsCeiling(both, nil)}, nil},
		{"self alone holds only the project's files", vmhost.Capabilities{Self: selfAllows("std::io")}, []string{
			"std::fs | self | main.nomi:8:17 | [std::io]",
		}},
		{"deps alone holds only the dependencies' files", vmhost.Capabilities{Deps: depsCeiling(io, nil)}, []string{
			"std::fs | deps example.com/pricing | rules.nomi:7:17 | [std::io]",
		}},
		{"self allows nothing", vmhost.Capabilities{Self: selfAllows()}, []string{
			"std::fs | self | main.nomi:8:17 | []",
			"std::io | self | main.nomi:12:13 | []",
			"std::io | self | main.nomi:13:13 | []",
		}},
		{"no ceiling and no entry", vmhost.Capabilities{Deps: &vmhost.DepsCapabilities{}}, nil},
		{"an entry with no ceiling", vmhost.Capabilities{Deps: depsCeiling(nil, map[string][]string{"example.com/pricing": io})}, []string{
			"std::fs | entry example.com/pricing | rules.nomi:7:17 | [std::io]",
		}},
		{"an entry allowed nothing, the callback it is handed the program's", vmhost.Capabilities{Deps: &vmhost.DepsCapabilities{Modules: map[string][]string{"example.com/pricing": {}}}}, []string{
			"std::fs | entry example.com/pricing | rules.nomi:7:17 | []",
			"std::io | entry example.com/pricing | rules.nomi:11:13 | []",
		}},
		{"within the entry, over the ceiling", vmhost.Capabilities{Deps: depsCeiling(io, map[string][]string{"example.com/pricing": both})}, []string{
			"std::fs | deps example.com/pricing | rules.nomi:7:17 | [std::io]",
		}},
		{"within the ceiling, over the entry", vmhost.Capabilities{Deps: depsCeiling(both, map[string][]string{"example.com/pricing": io})}, []string{
			"std::fs | entry example.com/pricing | rules.nomi:7:17 | [std::io]",
		}},
		{"over both, once", vmhost.Capabilities{Deps: depsCeiling(io, map[string][]string{"example.com/pricing": io})}, []string{
			"std::fs | entry example.com/pricing | rules.nomi:7:17 | [std::io] | deps denies too",
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

// A dependency whose own nomi.toml has a [capabilities.self] table is held
// to its std when the project has a [capabilities.deps]: within it the
// program passes, over it the call is reported over the module's own list.
// With the project's entry too a call must be in both, and a call over
// every list is reported once, over the module's own list, saying the
// others deny it too; a longer entry is no error. Its std_in_tests and its
// [capabilities.deps] are not read, and a dependency with no
// [capabilities.self] declares nothing. A project with no
// [capabilities.deps] reads no dependency's table.
func TestOutsideCallsNotAllowed_ADependencysOwnSelf(t *testing.T) {
	both := []string{"std::fs", "std::io"}
	io := []string{"std::io"}
	cases := []struct {
		name, table string
		lists       vmhost.Capabilities
		want        []string
	}{
		{"within its own list", "\n[capabilities.self]\nstd = [\"std::fs\", \"std::io\"]\n", vmhost.Capabilities{Deps: depsCeiling(both, nil)}, nil},
		{"over its own list", "\n[capabilities.self]\nstd = [\"std::io\"]\nstd_in_tests = [\"std::fs\"]\n", vmhost.Capabilities{Deps: depsCeiling(both, nil)}, []string{
			"std::fs | own example.com/pricing | rules.nomi:7:17 | [std::io]",
		}},
		{"a table with no std allows nothing", "\n[capabilities.self]\n", vmhost.Capabilities{Deps: depsCeiling(both, nil)}, []string{
			"std::fs | own example.com/pricing | rules.nomi:7:17 | []",
			"std::io | own example.com/pricing | rules.nomi:11:13 | []",
		}},
		{"in its own list, not in the project's entry", "\n[capabilities.self]\nstd = [\"std::fs\", \"std::io\"]\n",
			vmhost.Capabilities{Deps: depsCeiling(both, map[string][]string{"example.com/pricing": io})}, []string{
				"std::fs | entry example.com/pricing | rules.nomi:7:17 | [std::io]",
			}},
		{"in the project's entry, not in its own list", "\n[capabilities.self]\nstd = [\"std::io\"]\n",
			vmhost.Capabilities{Deps: depsCeiling(both, map[string][]string{"example.com/pricing": both})}, []string{
				"std::fs | own example.com/pricing | rules.nomi:7:17 | [std::io]",
			}},
		{"its own [capabilities.deps] is not read", "\n[capabilities.self]\nstd = [\"std::fs\", \"std::io\"]\n\n[capabilities.deps]\nstd = []\n",
			vmhost.Capabilities{Deps: depsCeiling(both, nil)}, nil},
		{"a table of deps alone declares nothing", "\n[capabilities.deps]\nstd = []\n", vmhost.Capabilities{Deps: depsCeiling(both, nil)}, nil},
		{"over every list, once", "\n[capabilities.self]\nstd = [\"std::io\"]\n",
			vmhost.Capabilities{Deps: depsCeiling(io, map[string][]string{"example.com/pricing": io})}, []string{
				"std::fs | own example.com/pricing | rules.nomi:7:17 | [std::io] | entry denies too | deps denies too",
			}},
		{"a project with self alone", "\n[capabilities.self]\nstd = []\n", vmhost.Capabilities{Self: selfAllows(both...)}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkNotAllowed(t, pricingProjectDeclaring(t, c.table), vmhost.Roots{}, &c.lists, c.want...)
		})
	}
	p := pricingProjectDeclaring(t, "\n[capabilities.self]\nstd = []\n")
	if got := p.OutsideCallsNotAllowed(vmhost.Roots{}, nil); got != nil {
		t.Errorf("no project lists: %v, want nothing", got)
	}
}

// A test case's calls in the project's own files are held to self's std
// and std_in_tests together, and main's to std alone. Exported functions
// are roots when asked for.
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
	lists := &vmhost.Capabilities{Self: &vmhost.SelfCapabilities{Std: []string{"std::io"}, StdInTests: []string{"std::fs"}}}
	checkNotAllowed(t, p, vmhost.Roots{}, lists)
	checkNotAllowed(t, p, vmhost.Roots{Exports: true}, lists,
		"std::process | self | main.nomi:8:20 | [std::io]",
	)
	checkNotAllowed(t, p, vmhost.Roots{Tests: &vmhost.TestOptions{}}, lists)
	checkNotAllowed(t, p, vmhost.Roots{Tests: &vmhost.TestOptions{}}, &vmhost.Capabilities{Self: selfAllows("std::io")},
		"std::fs | self | main.nomi:17:21 | [std::io]",
	)
}

// A test case's calls in a dependency's files are held to deps' std and
// std_in_tests together, and main's to std alone; the dependency's entry
// holds its calls from every root.
func TestOutsideCallsNotAllowed_DepsInTests(t *testing.T) {
	p := loadProject(t, "app/main.nomi", pricingFiles("", "\ntest \"loads\"\n    assert rules.load(\"prices.txt\") == \"\"\nend\n"))
	tests := vmhost.Roots{Tests: &vmhost.TestOptions{}}
	inTests := &vmhost.Capabilities{Deps: &vmhost.DepsCapabilities{Std: []string{"std::io"}, StdInTests: []string{"std::fs"}}}
	checkNotAllowed(t, p, tests, inTests)
	checkNotAllowed(t, p, vmhost.Roots{}, inTests,
		"std::fs | deps example.com/pricing | rules.nomi:7:17 | [std::io]",
	)
	checkNotAllowed(t, p, tests, &vmhost.Capabilities{Deps: depsCeiling([]string{"std::io"}, nil)},
		"std::fs | deps example.com/pricing | rules.nomi:7:17 | [std::io]",
	)
	entry := &vmhost.Capabilities{Deps: &vmhost.DepsCapabilities{Std: []string{"std::io"}, StdInTests: []string{"std::fs"},
		Modules: map[string][]string{"example.com/pricing": {"std::io"}}}}
	checkNotAllowed(t, p, tests, entry,
		"std::fs | entry example.com/pricing | rules.nomi:7:17 | [std::io]",
	)
}
