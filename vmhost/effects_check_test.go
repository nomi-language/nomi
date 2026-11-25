package vmhost_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// notAllowedLines is each call p reaches from r that lists does not allow:
// its name, the list it is over ("self" or "self entry" and the key;
// "deps", "entry" or "own" and the module, with the key of its own entry),
// its site, what the list allows, and "entry denies too", "ceiling denies
// too" and "own std denies too" when the call is over other lists as well.
func notAllowedLines(p *vmhost.Program, r vmhost.Roots, lists *vmhost.Effects) []string {
	var out []string
	for _, n := range p.EffectCallsNotAllowed(r, lists) {
		over := map[vmhost.EffectList]string{vmhost.OverSelf: "self", vmhost.OverSelfEntry: "self entry", vmhost.OverDependencies: "deps", vmhost.OverEntry: "entry", vmhost.OverDeclared: "own"}[n.List]
		if n.Package != "" {
			over += " " + n.Package
		}
		if n.Entry != "" {
			over += " " + n.Entry
		}
		line := fmt.Sprintf("%s | %s | %s | [%s]", n.Name, over, n.Site, strings.Join(n.Allowed, " "))
		if n.EntryDenies {
			line += " | entry denies too"
		}
		if n.CeilingDenies {
			line += " | ceiling denies too"
		}
		if n.OwnStdDenies {
			line += " | own std denies too"
		}
		out = append(out, line)
	}
	return out
}

func checkNotAllowed(t *testing.T, p *vmhost.Program, r vmhost.Roots, lists *vmhost.Effects, want ...string) {
	t.Helper()
	got := notAllowedLines(p, r, lists)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("not allowed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// selfAllows is an [effects.self] whose std is names.
func selfAllows(names ...string) *vmhost.SelfEffects {
	return &vmhost.SelfEffects{Effects: append([]string{}, names...)}
}

// depsCeiling is an [effects.dependencies] whose std is names, none for nil,
// with entries.
func depsCeiling(names []string, entries map[string][]string) *vmhost.DependenciesEffects {
	d := &vmhost.DependenciesEffects{Entries: entries}
	if names != nil {
		d.Effects = append([]string{}, names...)
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
		"app/nomi.toml": "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\npricing = { dir = \"../pricing\" }\n",
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
		"pricing/nomi.toml": "[package]\nname = \"pricing\"\n" + table,
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

// [effects.self] holds the calls in the project's own files and
// [effects.dependencies] the calls in a dependency's: each alone holds only
// what it names. Its effects are a ceiling over every dependency, a
// [dependencies] entry's effects hold that package's calls, and a call
// over both is reported once, over the entry. With no ceiling and no
// entry a dependency's calls are not held. A callback the program hands a
// module is the program's.
func TestEffectCallsNotAllowed_TheSelfAndDepsTables(t *testing.T) {
	p := pricingProject(t)
	both := []string{"std::fs", "std::io"}
	io := []string{"std::io"}
	cases := []struct {
		name  string
		lists vmhost.Effects
		want  []string
	}{
		{"everything allowed", vmhost.Effects{Self: selfAllows(both...), Dependencies: depsCeiling(both, nil)}, nil},
		{"self alone holds only the project's files", vmhost.Effects{Self: selfAllows("std::io")}, []string{
			"std::fs | self | main.nomi:8:17 | [std::io]",
		}},
		{"deps alone holds only the dependencies' files", vmhost.Effects{Dependencies: depsCeiling(io, nil)}, []string{
			"std::fs | deps pricing | rules.nomi:7:17 | [std::io]",
		}},
		{"self allows nothing", vmhost.Effects{Self: selfAllows()}, []string{
			"std::fs | self | main.nomi:8:17 | []",
			"std::io | self | main.nomi:12:13 | []",
			"std::io | self | main.nomi:13:13 | []",
		}},
		{"no ceiling and no entry", vmhost.Effects{Dependencies: &vmhost.DependenciesEffects{}}, nil},
		{"an entry with no ceiling", vmhost.Effects{Dependencies: depsCeiling(nil, map[string][]string{"pricing": io})}, []string{
			"std::fs | entry pricing | rules.nomi:7:17 | [std::io]",
		}},
		{"an entry allowed nothing, the callback it is handed the program's", vmhost.Effects{Dependencies: &vmhost.DependenciesEffects{Entries: map[string][]string{"pricing": {}}}}, []string{
			"std::fs | entry pricing | rules.nomi:7:17 | []",
			"std::io | entry pricing | rules.nomi:11:13 | []",
		}},
		{"within the entry, over the ceiling", vmhost.Effects{Dependencies: depsCeiling(io, map[string][]string{"pricing": both})}, []string{
			"std::fs | deps pricing | rules.nomi:7:17 | [std::io]",
		}},
		{"within the ceiling, over the entry", vmhost.Effects{Dependencies: depsCeiling(both, map[string][]string{"pricing": io})}, []string{
			"std::fs | entry pricing | rules.nomi:7:17 | [std::io]",
		}},
		{"over both, once", vmhost.Effects{Dependencies: depsCeiling(io, map[string][]string{"pricing": io})}, []string{
			"std::fs | entry pricing | rules.nomi:7:17 | [std::io] | ceiling denies too",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkNotAllowed(t, p, vmhost.Roots{}, &c.lists, c.want...)
		})
	}
	if got := p.EffectCallsNotAllowed(vmhost.Roots{}, nil); got != nil {
		t.Errorf("no lists: %v, want nothing", got)
	}
}

// A dependency whose own nomi.toml has an [effects.self] table is held
// to its std when the project has an [effects.dependencies]: within it the
// program passes, over it the call is reported over the module's own list.
// With the project's entry too a call must be in both, and a call over
// every list is reported once, over the module's own list, saying the
// others deny it too; a longer entry is no error. Its effects_in_tests and its
// [effects.dependencies] are not read, and a dependency with no
// [effects.self] declares nothing. A project with no
// [effects.dependencies] reads no dependency's table.
func TestEffectCallsNotAllowed_ADependencysOwnSelf(t *testing.T) {
	both := []string{"std::fs", "std::io"}
	io := []string{"std::io"}
	cases := []struct {
		name, table string
		lists       vmhost.Effects
		want        []string
	}{
		{"within its own list", "\n[effects.self]\neffects = [\"std::fs\", \"std::io\"]\n", vmhost.Effects{Dependencies: depsCeiling(both, nil)}, nil},
		{"over its own list", "\n[effects.self]\neffects = [\"std::io\"]\neffects_in_tests = [\"std::fs\"]\n", vmhost.Effects{Dependencies: depsCeiling(both, nil)}, []string{
			"std::fs | own pricing | rules.nomi:7:17 | [std::io]",
		}},
		{"a table with no std allows nothing", "\n[effects.self]\n", vmhost.Effects{Dependencies: depsCeiling(both, nil)}, []string{
			"std::fs | own pricing | rules.nomi:7:17 | []",
			"std::io | own pricing | rules.nomi:11:13 | []",
		}},
		{"in its own list, not in the project's entry", "\n[effects.self]\neffects = [\"std::fs\", \"std::io\"]\n",
			vmhost.Effects{Dependencies: depsCeiling(both, map[string][]string{"pricing": io})}, []string{
				"std::fs | entry pricing | rules.nomi:7:17 | [std::io]",
			}},
		{"in the project's entry, not in its own list", "\n[effects.self]\neffects = [\"std::io\"]\n",
			vmhost.Effects{Dependencies: depsCeiling(both, map[string][]string{"pricing": both})}, []string{
				"std::fs | own pricing | rules.nomi:7:17 | [std::io]",
			}},
		{"its own [effects.dependencies] is not read", "\n[effects.self]\neffects = [\"std::fs\", \"std::io\"]\n\n[effects.dependencies]\neffects = []\n",
			vmhost.Effects{Dependencies: depsCeiling(both, nil)}, nil},
		{"a table of deps alone declares nothing", "\n[effects.dependencies]\neffects = []\n", vmhost.Effects{Dependencies: depsCeiling(both, nil)}, nil},
		{"over every list, once", "\n[effects.self]\neffects = [\"std::io\"]\n",
			vmhost.Effects{Dependencies: depsCeiling(io, map[string][]string{"pricing": io})}, []string{
				"std::fs | own pricing | rules.nomi:7:17 | [std::io] | entry denies too | ceiling denies too",
			}},
		{"a project with self alone", "\n[effects.self]\neffects = []\n", vmhost.Effects{Self: selfAllows(both...)}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkNotAllowed(t, pricingProjectDeclaring(t, c.table), vmhost.Roots{}, &c.lists, c.want...)
		})
	}
	p := pricingProjectDeclaring(t, "\n[effects.self]\neffects = []\n")
	if got := p.EffectCallsNotAllowed(vmhost.Roots{}, nil); got != nil {
		t.Errorf("no project lists: %v, want nothing", got)
	}
}

// A test case's calls in the project's own files are held to self's std
// and effects_in_tests together, and main's to std alone. Exported functions
// are roots when asked for.
func TestEffectCallsNotAllowed_TestsAndExports(t *testing.T) {
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
	lists := &vmhost.Effects{Self: &vmhost.SelfEffects{Effects: []string{"std::io"}, EffectsInTests: []string{"std::fs"}}}
	checkNotAllowed(t, p, vmhost.Roots{}, lists)
	checkNotAllowed(t, p, vmhost.Roots{Exports: true}, lists,
		"std::process | self | main.nomi:8:20 | [std::io]",
	)
	checkNotAllowed(t, p, vmhost.Roots{Tests: &vmhost.TestOptions{}}, lists)
	checkNotAllowed(t, p, vmhost.Roots{Tests: &vmhost.TestOptions{}}, &vmhost.Effects{Self: selfAllows("std::io")},
		"std::fs | self | main.nomi:17:21 | [std::io]",
	)
}

// storeProject is a project of two modules: main reads a file and prints
// what store loads, and hands store a callback that reads a file; store
// reads a file and prints. A test loads too.
func storeProject(t *testing.T) *vmhost.Program {
	return loadProject(t, "main.nomi", map[string]string{
		"nomi.toml": "[package]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"main.nomi": `import
    std::fs
    std::io
    store
end

fn read(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end

fn main()
    io.print(store.load("prices.txt"))
    io.print(store.twice("x", read))
end

test "loads"
    assert store.load("prices.txt") == ""
end
`,
		"store.nomi": `import
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

// An entry under [effects.self.modules] holds the calls in its
// module's files within [effects.self]: a module with no entry is
// held by self's std alone, a call over both is reported once, over the
// entry, and the callback a module is handed is the module's that wrote
// it. The entry holds its module's calls from every root, tests included.
func TestEffectCallsNotAllowed_TheSelfModulesTable(t *testing.T) {
	p := storeProject(t)
	both := []string{"std::fs", "std::io"}
	self := func(std []string, entries map[string][]string) *vmhost.Effects {
		return &vmhost.Effects{Self: &vmhost.SelfEffects{Effects: std, Modules: entries}}
	}
	cases := []struct {
		name  string
		lists *vmhost.Effects
		want  []string
	}{
		{"no entries, self alone", self([]string{"std::io"}, nil), []string{
			"std::fs | self | main.nomi:8:17 | [std::io]",
			"std::fs | self | store.nomi:7:17 | [std::io]",
		}},
		{"a module with no entry is held by self alone", self(both, map[string][]string{"main": both}), nil},
		{"an entry within self", self(both, map[string][]string{"store": {"std::fs"}}), []string{
			"std::io | self entry store | store.nomi:11:13 | [std::fs]",
		}},
		{"an empty entry, the callback it is handed main's", self(both, map[string][]string{"store": {}}), []string{
			"std::fs | self entry store | store.nomi:7:17 | []",
			"std::io | self entry store | store.nomi:11:13 | []",
		}},
		{"within the entry, over self", self([]string{"std::io"}, map[string][]string{"store": both}), []string{
			"std::fs | self | main.nomi:8:17 | [std::io]",
			"std::fs | self | store.nomi:7:17 | [std::io]",
		}},
		{"over both, once", self([]string{"std::io"}, map[string][]string{"store": {"std::io"}}), []string{
			"std::fs | self | main.nomi:8:17 | [std::io]",
			"std::fs | self entry store | store.nomi:7:17 | [std::io] | ceiling denies too",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkNotAllowed(t, p, vmhost.Roots{}, c.lists, c.want...)
		})
	}
	tests := vmhost.Roots{Tests: &vmhost.TestOptions{}}
	inTests := &vmhost.Effects{Self: &vmhost.SelfEffects{Effects: []string{"std::io"}, EffectsInTests: []string{"std::fs"}}}
	checkNotAllowed(t, p, tests, inTests)
	inTests.Self.Modules = map[string][]string{"store": {"std::io"}}
	checkNotAllowed(t, p, tests, inTests,
		"std::fs | self entry store | store.nomi:7:17 | [std::io]",
	)
}

// A dependency whose own nomi.toml declares [effects.self.modules] is
// held to the entry for each file's module within its own std, when the
// project has an [effects.dependencies]: over the entry the call is reported
// over the module's own list, naming the entry, and saying when its own
// std denies it too. A module it gives no entry is held by its own std
// alone, and its own entries_required is for its own builds.
func TestEffectCallsNotAllowed_ADependencysOwnModules(t *testing.T) {
	both := []string{"std::fs", "std::io"}
	lists := vmhost.Effects{Dependencies: depsCeiling(both, nil)}
	cases := []struct {
		name, table string
		want        []string
	}{
		{"within its own entry", "\n[effects.self]\neffects = [\"std::fs\", \"std::io\"]\n\n[effects.self.modules]\n\"rules\" = [\"std::fs\", \"std::io\"]\n", nil},
		{"over its own entry", "\n[effects.self]\neffects = [\"std::fs\", \"std::io\"]\n\n[effects.self.modules]\n\"rules\" = [\"std::io\"]\n", []string{
			"std::fs | own pricing rules | rules.nomi:7:17 | [std::io]",
		}},
		{"over its own entry and its own std", "\n[effects.self]\neffects = [\"std::io\"]\n\n[effects.self.modules]\n\"rules\" = [\"std::io\"]\n", []string{
			"std::fs | own pricing rules | rules.nomi:7:17 | [std::io] | own std denies too",
		}},
		{"a module it gives no entry", "\n[effects.self]\neffects = [\"std::fs\", \"std::io\"]\n\n[effects.self.modules]\n\"other\" = []\n", nil},
		{"its own entries_required is not read", "\n[effects.self]\nentries_required = true\neffects = [\"std::fs\", \"std::io\"]\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkNotAllowed(t, pricingProjectDeclaring(t, c.table), vmhost.Roots{}, &lists, c.want...)
		})
	}
}

// A test case's calls in a dependency's files are held to the ceiling's
// effects and effects_in_tests together, and main's to effects alone; so
// are the dependency's entry's.
func TestEffectCallsNotAllowed_DepsInTests(t *testing.T) {
	p := loadProject(t, "app/main.nomi", pricingFiles("", "\ntest \"loads\"\n    assert rules.load(\"prices.txt\") == \"\"\nend\n"))
	tests := vmhost.Roots{Tests: &vmhost.TestOptions{}}
	inTests := &vmhost.Effects{Dependencies: &vmhost.DependenciesEffects{Effects: []string{"std::io"}, EffectsInTests: []string{"std::fs"}}}
	checkNotAllowed(t, p, tests, inTests)
	checkNotAllowed(t, p, vmhost.Roots{}, inTests,
		"std::fs | deps pricing | rules.nomi:7:17 | [std::io]",
	)
	checkNotAllowed(t, p, tests, &vmhost.Effects{Dependencies: depsCeiling([]string{"std::io"}, nil)},
		"std::fs | deps pricing | rules.nomi:7:17 | [std::io]",
	)
	entry := &vmhost.Effects{Dependencies: &vmhost.DependenciesEffects{Effects: []string{"std::io"}, EffectsInTests: []string{"std::fs"},
		Entries: map[string][]string{"pricing": {"std::io"}}}}
	checkNotAllowed(t, p, tests, entry,
		"std::fs | entry pricing | rules.nomi:7:17 | [std::io]",
	)
	entry.Dependencies.EntriesInTests = map[string][]string{"pricing": {"std::fs"}}
	checkNotAllowed(t, p, tests, entry)
	checkNotAllowed(t, p, vmhost.Roots{}, entry,
		"std::fs | entry pricing | rules.nomi:7:17 | [std::io] | ceiling denies too",
	)
}
