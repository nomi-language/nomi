package vmhost

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// reachFixtures are projects whose tightest tables TestTightestLists_PassAndEveryNameIsNeeded
// derives: each project's files, its entry, and the table it should get,
// as listsText writes one.
var reachFixtures = []struct {
	name, entry string
	files       map[string]string
	want        string
}{
	{"a dependency that reads a file for main", "app/main.nomi",
		dependencyFiles("[module]\nname = \"app\"\nentry_points = [\"main\"]\n", ""),
		"self.std=[std::io] self.std_in_tests=[] deps.std=[std::fs] deps.std_in_tests=[] pricing=[std::fs]"},
	{"effect calls only in tests, and a dependency that reaches nothing", "app/main.nomi", map[string]string{
		"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\npure = { dir = \"../pure\" }\n",
		"app/main.nomi": `import
    std::fs
    pure::calc
end

fn main()
    _ = calc.double(2)
end

test "reads"
    _ = fs.read_text("x")
    assert calc.double(1) == 2
end
`,
		"pure/nomi.toml":  "[module]\nname = \"pure\"\n",
		"pure/calc.nomi":  "pub fn double(n: Int) -> Int\n    n * 2\nend\n",
		"pure/other.nomi": "import std::io\n\npub fn say()\n    io.print(\"never called\")\nend\n",
	}, "self.std=[] self.std_in_tests=[std::fs] deps.std=[] deps.std_in_tests=[] pure=[]"},
	{"a dependency reached only from tests", "app/main.nomi", map[string]string{
		"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\npricing = { dir = \"../pricing\" }\n",
		"app/main.nomi": `import
    std::io
    pricing::rules
end

fn main()
    io.print("hi")
end

test "loads"
    assert rules.load("x") == ""
end
`,
		"pricing/nomi.toml": "[module]\nname = \"pricing\"\n",
		"pricing/rules.nomi": `import std::fs

pub fn load(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end
`,
	}, "self.std=[std::io] self.std_in_tests=[] deps.std=[] deps.std_in_tests=[std::fs] pricing=[std::fs]"},
	{"the modules of the project main loads, under a table of entries", "main.nomi",
		selfModulesFiles(`[]`, "\n[effects.self.modules]\n"),
		"self.std=[std::http std::io] self.std_in_tests=[] self.modules.main=[std::io] self.modules.net::fetch=[std::http] self.modules.pricing=[] deps.std=[] deps.std_in_tests=[]"},
	{"a test file's own entry", "pricing_test.nomi",
		selfModulesFiles(`[]`, "entries_required = true\n"),
		// pricing.total and fetch.rates are public, so they are roots of the
		// module's own list whichever file is the entry: std::http is the
		// code's, and only the test's file read is the tests'.
		"self.std=[std::http] self.std_in_tests=[std::fs] self.modules.net::fetch=[std::http] self.modules.pricing=[] self.modules.pricing_test=[std::fs] deps.std=[] deps.std_in_tests=[]"},
}

// listsText is c as one line: self's lists and each of its entries by key,
// deps' lists, and then each dependency's entry by path.
func listsText(c *Effects) string {
	parts := []string{
		fmt.Sprintf("self.std=%v", c.Self.Std), fmt.Sprintf("self.std_in_tests=%v", c.Self.StdInTests),
	}
	for _, m := range slices.Sorted(maps.Keys(c.Self.Modules)) {
		parts = append(parts, fmt.Sprintf("self.modules.%s=%v", m, c.Self.Modules[m]))
	}
	parts = append(parts,
		fmt.Sprintf("deps.std=%v", c.Deps.Std), fmt.Sprintf("deps.std_in_tests=%v", c.Deps.StdInTests),
	)
	for _, m := range slices.Sorted(maps.Keys(c.Deps.Modules)) {
		parts = append(parts, fmt.Sprintf("%s=%v", m, c.Deps.Modules[m]))
	}
	return strings.Join(parts, " ")
}

// notAllowedByAll is each call r's walks reach that lists does not allow,
// by notAllowedBy, with no module's own table.
func notAllowedByAll(r *Reach, lists *Effects) []NotAllowed {
	var out []NotAllowed
	for _, w := range r.Walks {
		out = append(out, notAllowedBy(w.Calls, w.Tests, lists, nil)...)
	}
	return out
}

// withoutName is c with name taken out of list, a list's path below
// [effects] (`self.std`, `deps.std_in_tests`), an entry of a module of
// the project (`self.modules.pricing`) or a dependency's entry by its
// path.
func withoutName(c *Effects, list, name string) *Effects {
	out := c.Clone()
	drop := func(names *[]string) {
		*names = slices.DeleteFunc(*names, func(n string) bool { return n == name })
	}
	if key, ok := strings.CutPrefix(list, "self.modules."); ok {
		entry := slices.Clone(out.Self.Modules[key])
		drop(&entry)
		out.Self.Modules[key] = entry
		return out
	}
	switch list {
	case "self.std":
		drop(&out.Self.Std)
	case "self.std_in_tests":
		drop(&out.Self.StdInTests)
	case "deps.std":
		drop(&out.Deps.Std)
	case "deps.std_in_tests":
		drop(&out.Deps.StdInTests)
	default:
		entry := out.Deps.Modules[list]
		drop(&entry)
		out.Deps.Modules[list] = entry
	}
	return out
}

// The tightest tables are the ones the check's own rule (notAllowedBy)
// holds every call to and passes, and every name in them is needed: taken
// out, some call is refused. Each fixture's tables are the ones written
// out.
func TestTightestLists_PassAndEveryNameIsNeeded(t *testing.T) {
	for _, fx := range reachFixtures {
		t.Run(fx.name, func(t *testing.T) {
			dir := writeProject(t, fx.files)
			r, err := Reached(filepath.Join(dir, filepath.FromSlash(fx.entry)))
			if err != nil {
				t.Fatalf("the program does not check: %v", err)
			}
			tight := r.Tightest()
			if got := listsText(tight); got != fx.want {
				t.Errorf("tightest table:\n%s\nwant:\n%s", got, fx.want)
			}
			if refused := notAllowedByAll(r, tight); len(refused) > 0 {
				t.Errorf("the tightest table refuses %d calls, the first %s at %s", len(refused), refused[0].Name, refused[0].Site)
			}
			lists := map[string][]string{
				"self.std": tight.Self.Std, "self.std_in_tests": tight.Self.StdInTests,
				"deps.std": tight.Deps.Std, "deps.std_in_tests": tight.Deps.StdInTests,
			}
			maps.Copy(lists, tight.Deps.Modules)
			for key, entry := range tight.Self.Modules {
				lists["self.modules."+key] = entry
			}
			for list, names := range lists {
				for _, name := range names {
					if len(notAllowedByAll(r, withoutName(tight, list, name))) == 0 {
						t.Errorf("without %s in %s the table still passes; it is not needed", name, list)
					}
				}
			}
		})
	}
}

// A call refused over the same list as an earlier call of the same name
// is counted on the first one's diagnostic, not shown: the text gives one
// diagnostic per name and list, a test's call of a name apart from main's,
// while Calls and Diagnostics keep every call. With AllCallsEnv set the
// text gives every call.
func TestEffectError_GroupsCallsByNameAndList(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\n",
		"app/main.nomi": `import
    std::fs
    std::io
end

fn main()
    io.print("a")
    io.print("b")
    io.print("c")
    io.print(fs.read_text("x"))
end

test "prints"
    io.print("d")
    io.print("e")
    assert 1 == 1
end
`,
	})
	t.Setenv(AllCallsEnv, "")
	err := Check(filepath.Join(dir, "app", "main.nomi"))
	refused, ok := err.(*EffectError)
	if !ok {
		t.Fatalf("the check answers %T, not a *EffectError: %v", err, err)
	}
	if len(refused.Calls) != 7 || len(refused.Diagnostics) != 7 {
		t.Errorf("%d calls and %d diagnostics; want every call, 7", len(refused.Calls), len(refused.Diagnostics))
	}
	shown := refused.Shown()
	var got []string
	for _, d := range shown {
		got = append(got, fmt.Sprintf("%d:%d %s | %s", d.Line, d.Col, d.Message, d.Hints[len(d.Hints)-1]))
	}
	want := []string{
		"7:5 this module's own code has the effect std::io, and [effects.self] allows it no effects | and 3 more calls have the effect std::io; --all-calls lists them",
		"10:14 this module's own code has the effect std::fs, and [effects.self] allows it no effects | add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call",
		"14:5 this module's tests have the effect std::io, and [effects.self] allows them no effects | and 1 more call has the effect std::io; --all-calls lists it",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("shown:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if refused.Error() != shown.Error() {
		t.Errorf("the text is not the shown diagnostics:\n%s", refused.Error())
	}
	t.Setenv(AllCallsEnv, "1")
	if all := refused.Shown(); len(all) != 7 || refused.Error() != refused.Diagnostics.Error() {
		t.Errorf("with %s set, %d diagnostics shown; want all 7", AllCallsEnv, len(all))
	}
}
