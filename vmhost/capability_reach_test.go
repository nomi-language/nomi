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
		"self.std=[std::io] self.std_in_tests=[] self.go=false self.go_in_tests=false deps.std=[] deps.std_in_tests=[] deps.go=[example.com/pg] deps.go_in_tests=[] example.com/pg=[]"},
	{"a dependency that reads a file for main", "app/main.nomi",
		dependencyFiles("[module]\nname = \"app\"\nentry_points = [\"main\"]\n", ""),
		"self.std=[std::io] self.std_in_tests=[] self.go=false self.go_in_tests=false deps.std=[std::fs] deps.std_in_tests=[] deps.go=[] deps.go_in_tests=[] example.com/pricing=[std::fs]"},
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
	}, "self.std=[] self.std_in_tests=[std::fs] self.go=false self.go_in_tests=false deps.std=[] deps.std_in_tests=[] deps.go=[] deps.go_in_tests=[] example.com/pure=[]"},
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
	}, "self.std=[std::io] self.std_in_tests=[] self.go=true self.go_in_tests=false deps.std=[] deps.std_in_tests=[std::fs] deps.go=[] deps.go_in_tests=[] example.com/pricing=[std::fs]"},
	{"the project's own Go only in tests", "main.nomi", map[string]string{
		"go.mod":    "module example.com/app\n\ngo 1.27.0\n",
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"main.nomi": `gopkg "strings" as gostrings

fn upper(s: String) -> String go gostrings.ToUpper

fn main()
    _ = 1
end

test "upper"
    assert upper("hi") == "HI"
end
`,
	}, "self.std=[] self.std_in_tests=[] self.go=false self.go_in_tests=true deps.std=[] deps.std_in_tests=[] deps.go=[] deps.go_in_tests=[]"},
}

// listsText is c as one line: self's lists, deps' lists, and then each
// dependency's entry by path.
func listsText(c *Capabilities) string {
	parts := []string{
		fmt.Sprintf("self.std=%v", c.Self.Std), fmt.Sprintf("self.std_in_tests=%v", c.Self.StdInTests),
		fmt.Sprintf("self.go=%v", c.Self.Go), fmt.Sprintf("self.go_in_tests=%v", c.Self.GoInTests),
		fmt.Sprintf("deps.std=%v", c.Deps.Std), fmt.Sprintf("deps.std_in_tests=%v", c.Deps.StdInTests),
		fmt.Sprintf("deps.go=%v", c.Deps.Go), fmt.Sprintf("deps.go_in_tests=%v", c.Deps.GoInTests),
	}
	for _, m := range slices.Sorted(maps.Keys(c.Deps.Modules)) {
		parts = append(parts, fmt.Sprintf("%s=%v", m, c.Deps.Modules[m]))
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

// withoutName is c with name taken out of list, a list's path below
// [capabilities] (`self.std`, `deps.go`) or a dependency's entry by its
// path, or with a flag (`self.go`) turned false.
func withoutName(c *Capabilities, list, name string) *Capabilities {
	out := c.Clone()
	drop := func(names *[]string) {
		*names = slices.DeleteFunc(*names, func(n string) bool { return n == name })
	}
	switch list {
	case "self.std":
		drop(&out.Self.Std)
	case "self.std_in_tests":
		drop(&out.Self.StdInTests)
	case "self.go":
		out.Self.Go = false
	case "self.go_in_tests":
		out.Self.GoInTests = false
	case "deps.std":
		drop(&out.Deps.Std)
	case "deps.std_in_tests":
		drop(&out.Deps.StdInTests)
	case "deps.go":
		drop(&out.Deps.Go)
	case "deps.go_in_tests":
		drop(&out.Deps.GoInTests)
	default:
		entry := out.Deps.Modules[list]
		drop(&entry)
		out.Deps.Modules[list] = entry
	}
	return out
}

// The tightest tables are the ones the check's own rule (notAllowedBy)
// holds every call to and passes, and every name in them is needed: taken
// out, some call is refused, and so is a flag that is true, turned false.
// Each fixture's tables are the ones written out.
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
			flag := func(on bool) []string {
				if on {
					return []string{"true"}
				}
				return nil
			}
			lists := map[string][]string{
				"self.std": tight.Self.Std, "self.std_in_tests": tight.Self.StdInTests,
				"self.go": flag(tight.Self.Go), "self.go_in_tests": flag(tight.Self.GoInTests),
				"deps.std": tight.Deps.Std, "deps.std_in_tests": tight.Deps.StdInTests,
				"deps.go": tight.Deps.Go, "deps.go_in_tests": tight.Deps.GoInTests,
			}
			maps.Copy(lists, tight.Deps.Modules)
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
		"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.self]\n",
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
		"7:5 this module's own code reaches std::io, and [capabilities.self] allows it nothing outside the program | and 3 more calls reach std::io; --all-calls lists them",
		"10:14 this module's own code reaches std::fs, and [capabilities.self] allows it nothing outside the program | add \"std::fs\" to std under [capabilities.self] in nomi.toml, or remove the call",
		"14:5 this module's tests reach std::io, and [capabilities.self] allows them nothing outside the program | and 1 more call reaches std::io; --all-calls lists it",
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
