package analysis

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A table or key nomi.toml does not have is an error at its line, with the
// nearest known name when one is close.
func TestManifest_UnknownKeysAreErrors(t *testing.T) {
	cases := []struct{ name, toml, want string }{
		{"a misspelled key", "[module]\nname = \"app\"\nnme = 1\n",
			"m.toml:3: [module] has no key `nme`; did you mean `name`?"},
		{"a misspelled table", "[module]\nname = \"app\"\n\n[i18m]\nlocales = [\"en\"]\n",
			"m.toml:4: nomi.toml has no [i18m] table; did you mean [i18n]?"},
		{"a table far from every other", "[module]\nname = \"app\"\n[bogus]\nx = 1\n",
			"m.toml:3: nomi.toml has no [bogus] table; its tables are [capabilities], [i18n], [module], [money]"},
		{"a key outside every table", "top = 1\n[module]\nname = \"app\"\n",
			"m.toml:1: nomi.toml has no top-level key `top`; every key is in a table ([capabilities], [i18n], [module], [money])"},
		{"a key far from every other", "[module]\nname = \"app\"\nwhatever = true\n",
			"m.toml:3: [module] has no key `whatever`; its keys are `name`, `entry_points`"},
		{"a dotted key", "[module]\nname = \"app\"\ni18n.localez = [\"en\"]\n",
			"m.toml:3: [module] has no key `i18n`; its keys are `name`, `entry_points`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := ParseManifestData([]byte(c.toml), "m.toml")
			if err == nil {
				t.Fatalf("the manifest is accepted (%+v); want %q", m, c.want)
			}
			if err.Error() != c.want {
				t.Errorf("error:\n%s\nwant:\n%s", err, c.want)
			}
		})
	}
}

// [capabilities.self] and [capabilities.deps] read into Capabilities; with
// neither there are no lists and nothing is held.
func TestManifest_Capabilities(t *testing.T) {
	m, err := ParseManifestData([]byte(`[module]
name = "app"

[capabilities.self]
std = ["std::io", "std::fs"]
std_in_tests = ["std::process"]
go = false
go_in_tests = true

[capabilities.deps]
entries_required = true
std = ["std::fs", "std::http"]
std_in_tests = ["std::instant"]
go = ["example.com/pg"]
go_in_tests = ["example.com/minio"]

[capabilities.deps.modules]
"example.com/pricing" = []
"example.com/pg" = ["std::fs"]
`), "m.toml")
	if err != nil {
		t.Fatal(err)
	}
	c := m.Capabilities
	if c == nil || c.Self == nil || c.Deps == nil {
		t.Fatalf("Capabilities %+v; want both tables", c)
	}
	for _, l := range []struct {
		name      string
		got, want []string
	}{
		{"Self.Std", c.Self.Std, []string{"std::io", "std::fs"}},
		{"Self.StdInTests", c.Self.StdInTests, []string{"std::process"}},
		{"Deps.Std", c.Deps.Std, []string{"std::fs", "std::http"}},
		{"Deps.StdInTests", c.Deps.StdInTests, []string{"std::instant"}},
		{"Deps.Go", c.Deps.Go, []string{"example.com/pg"}},
		{"Deps.GoInTests", c.Deps.GoInTests, []string{"example.com/minio"}},
		{"pg's entry", c.Deps.Modules["example.com/pg"], []string{"std::fs"}},
	} {
		if !slices.Equal(l.got, l.want) {
			t.Errorf("%s: %v, want %v", l.name, l.got, l.want)
		}
	}
	if c.Self.Go || !c.Self.GoInTests {
		t.Errorf("Self.Go %v, GoInTests %v; want false and true", c.Self.Go, c.Self.GoInTests)
	}
	if !c.Deps.EntriesRequired {
		t.Error("EntriesRequired is false; the table sets it")
	}
	if got := c.Deps.Modules["example.com/pricing"]; got == nil || len(got) != 0 {
		t.Errorf("pricing's list: %#v, want an empty list", got)
	}

	for _, tc := range []struct{ name, toml string }{
		{"no table", ""},
		{"an empty [capabilities]", "[capabilities]\n"},
	} {
		m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n"+tc.toml), "m.toml")
		if err != nil || m.Capabilities != nil {
			t.Errorf("%s: %+v, %v; want no Capabilities", tc.name, m.Capabilities, err)
		}
	}
	// Each table alone holds only what it names, and an empty one holds
	// that code to nothing: no std call, no Go, and no ceiling for deps.
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n[capabilities.self]\n"), "m.toml")
	if err != nil || m.Capabilities == nil || m.Capabilities.Self == nil || m.Capabilities.Deps != nil ||
		len(m.Capabilities.Self.Std) != 0 || m.Capabilities.Self.Go {
		t.Errorf("self alone: %+v, %v; want an empty self and no deps", m.Capabilities, err)
	}
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n[capabilities.deps]\n"), "m.toml")
	if err != nil || m.Capabilities == nil || m.Capabilities.Self != nil || m.Capabilities.Deps == nil ||
		m.Capabilities.Deps.Std != nil || len(m.Capabilities.Deps.Go) != 0 || m.Capabilities.Deps.EntriesRequired {
		t.Errorf("deps alone: %+v, %v; want an empty deps with no ceiling and no self", m.Capabilities, err)
	}
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n[capabilities.deps]\nstd = []\n"), "m.toml")
	if err != nil || m.Capabilities.Deps.Std == nil || len(m.Capabilities.Deps.Std) != 0 {
		t.Errorf("deps with std = []: %+v, %v; want a ceiling that allows nothing", m.Capabilities, err)
	}
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n[capabilities.deps.modules]\n\"example.com/pg\" = []\n"), "m.toml")
	if err != nil || m.Capabilities == nil || m.Capabilities.Deps == nil || m.Capabilities.Deps.Modules["example.com/pg"] == nil {
		t.Errorf("deps.modules alone: %+v, %v; want a deps table with the entry", m.Capabilities, err)
	}
}

// capsAbout is what the unknown-key error says each key of a capability
// table holds.
const (
	capsAbout = "it holds two tables: [capabilities.self] is what this module's own code may reach, and [capabilities.deps] what the code of the modules it depends on may reach"
	selfAbout = "`std` lists the std modules this module's own code may reach and `std_in_tests` those its tests may also reach, and `go` and `go_in_tests` are true or false: whether its own code, or its tests, may bind Go"
	depsAbout = "`std` and `std_in_tests` list the most std modules any dependency may reach, `go` and `go_in_tests` the dependencies whose Go may run, `entries_required` makes every dependency have an entry, and [capabilities.deps.modules] holds an entry for each dependency"
)

// An entry of a std list that is not a std module's name in form, a Go
// package or a host name there, an entry of a list of dependencies that is
// not a module path, an entry listed twice, a go of the wrong kind, and
// deps' std_in_tests without its std are errors at the entry's line. Any
// other key is an error that says what each key holds, rather than the
// nearest in spelling, which for allow_in_tests is go_in_tests; the keys
// [capabilities] held directly are among them.
func TestManifest_CapabilityEntryForms(t *testing.T) {
	cases := []struct{ name, table, want string }{
		{"no prefix", "[capabilities.self]\nstd = [\n    \"std::io\",\n    \"fs\",\n]\n",
			"m.toml:7: [capabilities.self] std: \"fs\" is not a std module; write one as an import does, such as \"std::fs\""},
		{"a host name", "[capabilities.self]\nstd_in_tests = [\"host:clock\"]\n",
			"m.toml:5: [capabilities.self] std_in_tests: \"host:clock\" names a function an embedding Go program supplies, which only that program can allow"},
		{"a std path", "[capabilities.deps]\nstd = [\"std::fs/host\"]\n",
			"m.toml:5: [capabilities.deps] std: \"std::fs/host\" is not a std module; write one as an import does, such as \"std::fs\""},
		{"a Go package in self's std", "[capabilities.self]\nstd = [\"go:github.com/jackc/pgx/v5\"]\n",
			"m.toml:5: [capabilities.self] std: \"go:github.com/jackc/pgx/v5\" names a Go package; Go is allowed by module, not by package: set go = true under [capabilities.self] to let this module's own code bind Go"},
		{"a Go package in deps' std", "[capabilities.deps]\nstd = [\"go:strings\"]\n",
			"m.toml:5: [capabilities.deps] std: \"go:strings\" names a Go package; Go is allowed by module, not by package: list the dependency that binds it in go under [capabilities.deps]"},
		{"a Go package in a module's entry", "[capabilities.deps.modules]\n\"example.com/a\" = [\"go:strings\"]\n",
			"m.toml:5: [capabilities.deps.modules] \"example.com/a\": \"go:strings\" names a Go package; Go is allowed by module, not by package: list the dependency that binds it in go under [capabilities.deps]"},
		{"a Go package in go", "[capabilities.deps]\ngo = [\"go:example.com/pg\"]\n",
			"m.toml:5: [capabilities.deps] go: \"go:example.com/pg\" names a Go package; name the module that binds it, as go.mod writes it, such as \"example.com/pg\""},
		{"a std module in go_in_tests", "[capabilities.deps]\ngo_in_tests = [\"std::fs\"]\n",
			"m.toml:5: [capabilities.deps] go_in_tests: \"std::fs\" is a std module, which a list of std names holds; this list names modules, such as \"example.com/pg\""},
		{"an empty module path", "[capabilities.deps]\ngo = [\"\"]\n",
			"m.toml:5: [capabilities.deps] go: \"\" is not a module path; write one as go.mod writes it, such as \"example.com/pg\""},
		{"twice", "[capabilities.self]\nstd = [\"std::io\", \"std::io\"]\n",
			"m.toml:5: [capabilities.self] std lists std::io twice"},
		{"a module twice", "[capabilities.deps]\ngo = [\"example.com/pg\", \"example.com/pg\"]\n",
			"m.toml:5: [capabilities.deps] go lists example.com/pg twice"},
		{"a list as self's go", "[capabilities.self]\ngo = [\"example.com/app\"]\n",
			"m.toml:5: [capabilities.self] go is true or false: whether this module's own code may bind Go; the dependencies whose Go may run are listed in go under [capabilities.deps]"},
		{"a flag as deps' go", "[capabilities.deps]\ngo = true\n",
			"m.toml:5: [capabilities.deps] go lists the dependencies whose Go may run, by module path; whether this module's own code may bind Go is go under [capabilities.self]"},
		{"deps' std_in_tests without std", "[capabilities.deps]\nstd_in_tests = [\"std::fs\"]\n",
			"m.toml:5: [capabilities.deps] std_in_tests adds to std, which the table leaves out; with no std, no ceiling holds the dependencies"},
		{"an unknown key in self", "[capabilities.self]\nstds = [\"std::io\"]\n",
			"m.toml:5: [capabilities.self] has no key `stds`; " + selfAbout},
		{"allow_in_tests in self", "[capabilities.self]\nallow_in_tests = [\"std::io\"]\n",
			"m.toml:5: [capabilities.self] has no key `allow_in_tests`; " + selfAbout},
		{"entries_required in self", "[capabilities.self]\nentries_required = true\n",
			"m.toml:5: [capabilities.self] has no key `entries_required`; " + selfAbout},
		{"an unknown key in deps", "[capabilities.deps]\nrequire = true\n",
			"m.toml:5: [capabilities.deps] has no key `require`; " + depsAbout},
		{"a table nested in self", "[capabilities.self.modules]\n\"example.com/a\" = []\n",
			"m.toml:4: [capabilities.self] has no key `modules`; " + selfAbout},
		{"a third table", "[capabilities.all]\nstd = []\n",
			"m.toml:4: [capabilities] has no key `all`; " + capsAbout},
	}
	// Each key [capabilities] held directly, and its table of entries, is
	// an error that names the two tables.
	for _, key := range []string{"std", "std_in_tests", "go", "go_in_tests", "require_module_lists"} {
		value := "[\"std::io\"]"
		if key == "require_module_lists" {
			value = "true"
		}
		cases = append(cases, struct{ name, table, want string }{"the key " + key + " under [capabilities]",
			"[capabilities]\n" + key + " = " + value + "\n", "m.toml:5: [capabilities] has no key `" + key + "`; " + capsAbout})
	}
	cases = append(cases, struct{ name, table, want string }{"[capabilities.modules]",
		"[capabilities.modules]\n\"example.com/a\" = []\n", "m.toml:4: [capabilities] has no key `modules`; " + capsAbout})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := ParseManifestData([]byte("[module]\nname = \"app\"\n\n"+c.table), "m.toml")
			if err == nil {
				t.Fatalf("the manifest is accepted (%+v); want %q", m.Capabilities, c.want)
			}
			if err.Error() != c.want {
				t.Errorf("error:\n%s\nwant:\n%s", err, c.want)
			}
		})
	}
}

// A top-level table named for the module itself or a module it requires
// belongs to that module's tools, which read it; any other is an error.
func TestManifest_ARequiredModulesTableIsItsTools(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	kit := filepath.Join(root, "kit")
	for path, text := range map[string]string{
		filepath.Join(app, "go.mod"):    "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/kit v0.0.0\n\nreplace example.com/kit => ../kit\n",
		filepath.Join(app, "nomi.toml"): "[module]\nname = \"app\"\n\n[kit.database.main]\ndialect = \"sqlite\"\n\n[app]\nanything = 1\n",
		filepath.Join(kit, "go.mod"):    "module example.com/kit\n\ngo 1.27.0\n",
		filepath.Join(kit, "nomi.toml"): "[module]\nname = \"kit\"\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadManifest(app); err != nil {
		t.Fatalf("a required module's table is refused: %v", err)
	}
	writeManifestFile(t, app, "nomi.toml", "[module]\nname = \"app\"\n\n[kitt.database.main]\ndialect = \"sqlite\"\n")
	_, err := LoadManifest(app)
	want := filepath.Join(app, "nomi.toml") + ":4: nomi.toml has no [kitt] table; its tables are [capabilities], [i18n], [module], [money]"
	if err == nil || err.Error() != want {
		t.Errorf("error: %v\nwant: %s", err, want)
	}
}
