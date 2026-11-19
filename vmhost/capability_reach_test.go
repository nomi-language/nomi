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
	{"a dependency that binds Go, reached from main and a test", "app/main.nomi",
		goProject("[module]\nname = \"app\"\nentry_points = [\"main\"]\n", ""),
		"std=[std::io] std_in_tests=[] go=[example.com/pg] go_in_tests=[] example.com/app=[std::io] example.com/pg=[]"},
	{"a dependency that reads a file for main", "app/main.nomi",
		dependencyFiles("[module]\nname = \"app\"\nentry_points = [\"main\"]\n", ""),
		"std=[std::fs std::io] std_in_tests=[] go=[] go_in_tests=[] example.com/app=[std::io] example.com/pricing=[std::fs]"},
	{"outside calls only in tests, and a dependency that reaches nothing", "app/main.nomi", map[string]string{
		"app/go.mod":    "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/pure v0.0.0\n\nreplace example.com/pure => ../pure\n",
		"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
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
		"pure/go.mod":     "module example.com/pure\n\ngo 1.27.0\n",
		"pure/nomi.toml":  "[module]\nname = \"pure\"\n",
		"pure/calc.nomi":  "pub fn double(n: Int) -> Int\n    n * 2\nend\n",
		"pure/other.nomi": "import std::io\n\npub fn say()\n    io.print(\"never called\")\nend\n",
	}, "std=[] std_in_tests=[std::fs] go=[] go_in_tests=[] example.com/app=[] example.com/pure=[]"},
	{"the project's own Go, and a dependency reached only from tests", "app/main.nomi", map[string]string{
		"app/go.mod":    "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/pricing v0.0.0\n\nreplace example.com/pricing => ../pricing\n",
		"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"app/main.nomi": `import
    std::io
    pricing::rules
end

gopkg "strings" as gostrings

fn upper(s: String) -> String go gostrings.ToUpper

fn main()
    io.print(upper("hi"))
end

test "loads"
    assert rules.load("x") == ""
end
`,
		"pricing/go.mod":    "module example.com/pricing\n\ngo 1.27.0\n",
		"pricing/nomi.toml": "[module]\nname = \"pricing\"\n",
		"pricing/rules.nomi": `import std::fs

pub fn load(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end
`,
	}, "std=[std::io] std_in_tests=[std::fs] go=[example.com/app] go_in_tests=[] example.com/app=[std::io] example.com/pricing=[std::fs]"},
}

// listsText is c as one line, each list and then each module's entry by
// path.
func listsText(c *Capabilities) string {
	parts := []string{
		fmt.Sprintf("std=%v", c.Std), fmt.Sprintf("std_in_tests=%v", c.StdInTests),
		fmt.Sprintf("go=%v", c.Go), fmt.Sprintf("go_in_tests=%v", c.GoInTests),
	}
	for _, m := range slices.Sorted(maps.Keys(c.Modules)) {
		parts = append(parts, fmt.Sprintf("%s=%v", m, c.Modules[m]))
	}
	return strings.Join(parts, " ")
}

// notAllowedByAll is each call r's walks reach that lists does not allow,
// by notAllowedBy, with no module's own table.
func notAllowedByAll(r *Reach, lists *Capabilities) []NotAllowed {
	var out []NotAllowed
	for _, w := range r.Walks {
		out = append(out, notAllowedBy(w.Calls, w.Tests, lists, nil, r.Project)...)
	}
	return out
}

// withoutName is c with name taken out of list: std, std_in_tests, go,
// go_in_tests, or a module's entry under Modules by its path.
func withoutName(c *Capabilities, list, name string) *Capabilities {
	out := &Capabilities{
		Std: slices.Clone(c.Std), StdInTests: slices.Clone(c.StdInTests),
		Go: slices.Clone(c.Go), GoInTests: slices.Clone(c.GoInTests),
		Modules: map[string][]string{},
	}
	for m, names := range c.Modules {
		out.Modules[m] = slices.Clone(names)
	}
	drop := func(names []string) []string {
		return slices.DeleteFunc(names, func(n string) bool { return n == name })
	}
	switch list {
	case "std":
		out.Std = drop(out.Std)
	case "std_in_tests":
		out.StdInTests = drop(out.StdInTests)
	case "go":
		out.Go = drop(out.Go)
	case "go_in_tests":
		out.GoInTests = drop(out.GoInTests)
	default:
		out.Modules[list] = drop(out.Modules[list])
	}
	return out
}

// The tightest table is the one the check's own rule (notAllowedBy) holds
// every call to and passes, and every name in it is needed: taken out,
// some call is refused. Each fixture's table is the one written out.
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
			lists := map[string][]string{"std": tight.Std, "std_in_tests": tight.StdInTests, "go": tight.Go, "go_in_tests": tight.GoInTests}
			maps.Copy(lists, tight.Modules)
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
func TestCapabilityError_GroupsCallsByNameAndList(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities]\n",
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
	refused, ok := err.(*CapabilityError)
	if !ok {
		t.Fatalf("the check answers %T, not a *CapabilityError: %v", err, err)
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
		"7:5 this program reaches std::io, and nomi.toml allows nothing outside the program | and 3 more calls reach std::io; --all-calls lists them",
		"10:14 this program reaches std::fs, and nomi.toml allows nothing outside the program | add \"std::fs\" to std under [capabilities] in nomi.toml, or remove the call",
		"14:5 this program's tests reach std::io, and nomi.toml allows them nothing outside the program | and 1 more call reaches std::io; --all-calls lists it",
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
