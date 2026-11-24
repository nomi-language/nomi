package analysis

import (
	"fmt"
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
			"m.toml:3: nomi.toml has no [bogus] table; its tables are [dependencies], [effects], [i18n], [indirect], [module], [money]"},
		{"a key outside every table", "top = 1\n[module]\nname = \"app\"\n",
			"m.toml:1: nomi.toml has no top-level key `top`; every key is in a table ([dependencies], [effects], [i18n], [indirect], [module], [money])"},
		{"a key far from every other", "[module]\nname = \"app\"\nwhatever = true\n",
			"m.toml:3: [module] has no key `whatever`; its keys are `name`, `nomi`, `entry_points`"},
		{"a dotted key", "[module]\nname = \"app\"\ni18n.localez = [\"en\"]\n",
			"m.toml:3: [module] has no key `i18n`; its keys are `name`, `nomi`, `entry_points`"},
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

// [effects.self] and [effects.deps] read into Effects; with
// neither there are no lists and nothing is held.
func TestManifest_Effects(t *testing.T) {
	m, err := ParseManifestData([]byte(`[module]
name = "app"

[effects.self]
std = ["std::io", "std::fs"]
std_in_tests = ["std::process"]

[effects.deps]
entries_required = true
std = ["std::fs", "std::http"]
std_in_tests = ["std::instant"]

[effects.deps.modules]
"pricing" = []
"pg" = ["std::fs"]
`), "m.toml")
	if err != nil {
		t.Fatal(err)
	}
	c := m.Effects
	if c == nil || c.Self == nil || c.Deps == nil {
		t.Fatalf("Effects %+v; want both tables", c)
	}
	for _, l := range []struct {
		name      string
		got, want []string
	}{
		{"Self.Std", c.Self.Std, []string{"std::io", "std::fs"}},
		{"Self.StdInTests", c.Self.StdInTests, []string{"std::process"}},
		{"Deps.Std", c.Deps.Std, []string{"std::fs", "std::http"}},
		{"Deps.StdInTests", c.Deps.StdInTests, []string{"std::instant"}},
		{"pg's entry", c.Deps.Modules["pg"], []string{"std::fs"}},
	} {
		if !slices.Equal(l.got, l.want) {
			t.Errorf("%s: %v, want %v", l.name, l.got, l.want)
		}
	}
	if !c.Deps.EntriesRequired {
		t.Error("EntriesRequired is false; the table sets it")
	}
	if got := c.Deps.Modules["pricing"]; got == nil || len(got) != 0 {
		t.Errorf("pricing's list: %#v, want an empty list", got)
	}

	for _, tc := range []struct{ name, toml string }{
		{"no table", ""},
		{"an empty [effects]", "[effects]\n"},
	} {
		m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n"+tc.toml), "m.toml")
		if err != nil || m.Effects != nil {
			t.Errorf("%s: %+v, %v; want no Effects", tc.name, m.Effects, err)
		}
	}
	// Each table alone holds only what it names, and an empty one holds
	// that code to nothing: no std call, and no ceiling for deps.
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n[effects.self]\n"), "m.toml")
	if err != nil || m.Effects == nil || m.Effects.Self == nil || m.Effects.Deps != nil ||
		len(m.Effects.Self.Std) != 0 {
		t.Errorf("self alone: %+v, %v; want an empty self and no deps", m.Effects, err)
	}
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n[effects.deps]\n"), "m.toml")
	if err != nil || m.Effects == nil || m.Effects.Self != nil || m.Effects.Deps == nil ||
		m.Effects.Deps.Std != nil || m.Effects.Deps.EntriesRequired {
		t.Errorf("deps alone: %+v, %v; want an empty deps with no ceiling and no self", m.Effects, err)
	}
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n[effects.deps]\nstd = []\n"), "m.toml")
	if err != nil || m.Effects.Deps.Std == nil || len(m.Effects.Deps.Std) != 0 {
		t.Errorf("deps with std = []: %+v, %v; want a ceiling that allows nothing", m.Effects, err)
	}
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n[effects.deps.modules]\n\"pg\" = []\n"), "m.toml")
	if err != nil || m.Effects == nil || m.Effects.Deps == nil || m.Effects.Deps.Modules["pg"] == nil {
		t.Errorf("deps.modules alone: %+v, %v; want a deps table with the entry", m.Effects, err)
	}
	// [effects.self.modules] is under [effects.self]: alone it
	// gives a self with no std, and its entries; an empty one is a table
	// with no entries, and self without it has none at all.
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n[effects.self.modules]\n\"pricing\" = []\n\"reports::daily\" = [\"std::fs\"]\n"), "m.toml")
	if err != nil || m.Effects == nil || m.Effects.Self == nil || m.Effects.Deps != nil || len(m.Effects.Self.Std) != 0 ||
		m.Effects.Self.Modules["pricing"] == nil || len(m.Effects.Self.Modules["pricing"]) != 0 ||
		fmt.Sprint(m.Effects.Self.Modules["reports::daily"]) != "[std::fs]" {
		t.Errorf("self.modules alone: %+v, %v; want a self table with the two entries", m.Effects, err)
	}
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n[effects.self]\nentries_required = true\n\n[effects.self.modules]\n"), "m.toml")
	if err != nil || m.Effects.Self.Modules == nil || len(m.Effects.Self.Modules) != 0 || !m.Effects.Self.EntriesRequired {
		t.Errorf("an empty self.modules with entries_required: %+v, %v; want the table with no entries, and entries_required", m.Effects.Self, err)
	}
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n[effects.self]\nstd = []\n"), "m.toml")
	if err != nil || m.Effects.Self.Modules != nil || m.Effects.Self.EntriesRequired {
		t.Errorf("self with no table of entries: %+v, %v; want no entries and no entries_required", m.Effects.Self, err)
	}
}

// capsAbout is what the unknown-key error says each key of an effect
// table holds.
const (
	capsAbout = "it holds two tables: [effects.self] is the effects this module's own code may have, and [effects.deps] the effects the code of the modules it depends on may have"
	selfAbout = "`std` lists the effects this module's own code may have, by std module, and `std_in_tests` those its tests may also have, `entries_required` makes every module of the project have an entry, and [effects.self.modules] holds an entry for each of its modules"
	depsAbout = "`std` and `std_in_tests` list the most effects any dependency may have, `entries_required` makes every dependency have an entry, and [effects.deps.modules] holds an entry for each dependency"
)

// An entry of a std list that is not a std module's name in form, a host
// name there, an entry listed twice, and deps' std_in_tests without its
// std are errors at the entry's line. Any other key is an error that says
// what each key holds, rather than the nearest in spelling, which for
// allow_in_tests is std_in_tests; the keys [effects] held directly are
// among them, and so are `go` and `go_in_tests`, keys no effects table
// has, and a `go:` entry, which names no std module.
func TestManifest_EffectEntryForms(t *testing.T) {
	cases := []struct{ name, table, want string }{
		{"no prefix", "[effects.self]\nstd = [\n    \"std::io\",\n    \"fs\",\n]\n",
			"m.toml:7: [effects.self] std: \"fs\" is not a std module; write one as an import does, such as \"std::fs\""},
		{"a host name", "[effects.self]\nstd_in_tests = [\"host:clock\"]\n",
			"m.toml:5: [effects.self] std_in_tests: \"host:clock\" names a function an embedding Go program supplies, which only that program can allow"},
		{"a std path", "[effects.deps]\nstd = [\"std::fs/host\"]\n",
			"m.toml:5: [effects.deps] std: \"std::fs/host\" is not a std module; write one as an import does, such as \"std::fs\""},
		{"a go: name in self's std", "[effects.self]\nstd = [\"go:github.com/jackc/pgx/v5\"]\n",
			"m.toml:5: [effects.self] std: \"go:github.com/jackc/pgx/v5\" is not a std module; write one as an import does, such as \"std::fs\""},
		{"a go: name in a module's entry", "[effects.deps.modules]\n\"a\" = [\"go:strings\"]\n",
			"m.toml:5: [effects.deps.modules] \"a\": \"go:strings\" is not a std module; write one as an import does, such as \"std::fs\""},
		{"a name twice in an entry of the project's own", "[effects.self.modules]\n\"pricing\" = [\"std::fs\", \"std::fs\"]\n",
			"m.toml:5: [effects.self.modules] \"pricing\" lists std::fs twice"},
		{"an empty key in the project's entries", "[effects.self.modules]\n\"\" = []\n",
			"m.toml:5: [effects.self.modules] has an empty key; write a module as the project's own code imports it, such as \"pricing\""},
		{"an empty key in the dependencies' entries", "[effects.deps.modules]\n\"\" = []\n",
			"m.toml:5: [effects.deps.modules] has an empty key; write a dependency's name as [dependencies] names it"},
		{"twice", "[effects.self]\nstd = [\"std::io\", \"std::io\"]\n",
			"m.toml:5: [effects.self] std lists std::io twice"},
		{"go in self", "[effects.self]\nstd = []\ngo = true\n",
			"m.toml:6: [effects.self] has no key `go`; " + selfAbout},
		{"go_in_tests in self", "[effects.self]\ngo_in_tests = false\n",
			"m.toml:5: [effects.self] has no key `go_in_tests`; " + selfAbout},
		{"go in deps", "[effects.deps]\ngo = [\"pg\"]\n",
			"m.toml:5: [effects.deps] has no key `go`; " + depsAbout},
		{"go_in_tests in deps", "[effects.deps]\nstd = []\ngo_in_tests = [\"pg\"]\n",
			"m.toml:6: [effects.deps] has no key `go_in_tests`; " + depsAbout},
		{"deps' std_in_tests without std", "[effects.deps]\nstd_in_tests = [\"std::fs\"]\n",
			"m.toml:5: [effects.deps] std_in_tests adds to std, which the table leaves out; with no std, no ceiling holds the dependencies"},
		{"an unknown key in self", "[effects.self]\nstds = [\"std::io\"]\n",
			"m.toml:5: [effects.self] has no key `stds`; " + selfAbout},
		{"allow_in_tests in self", "[effects.self]\nallow_in_tests = [\"std::io\"]\n",
			"m.toml:5: [effects.self] has no key `allow_in_tests`; " + selfAbout},
		{"entries in self", "[effects.self]\nentries = true\n",
			"m.toml:5: [effects.self] has no key `entries`; " + selfAbout},
		{"an unknown key in deps", "[effects.deps]\nrequire = true\n",
			"m.toml:5: [effects.deps] has no key `require`; " + depsAbout},
		{"another table nested in self", "[effects.self.mods]\n\"pricing\" = []\n",
			"m.toml:4: [effects.self] has no key `mods`; " + selfAbout},
		{"a third table", "[effects.all]\nstd = []\n",
			"m.toml:4: [effects] has no key `all`; " + capsAbout},
	}
	// Each key [effects] held directly, and its table of entries, is
	// an error that names the two tables.
	for _, key := range []string{"std", "std_in_tests", "require_module_lists"} {
		value := "[\"std::io\"]"
		if key == "require_module_lists" {
			value = "true"
		}
		cases = append(cases, struct{ name, table, want string }{"the key " + key + " under [effects]",
			"[effects]\n" + key + " = " + value + "\n", "m.toml:5: [effects] has no key `" + key + "`; " + capsAbout})
	}
	cases = append(cases, struct{ name, table, want string }{"[effects.modules]",
		"[effects.modules]\n\"a\" = []\n", "m.toml:4: [effects] has no key `modules`; " + capsAbout})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := ParseManifestData([]byte("[module]\nname = \"app\"\n\n"+c.table), "m.toml")
			if err == nil {
				t.Fatalf("the manifest is accepted (%+v); want %q", m.Effects, c.want)
			}
			if err.Error() != c.want {
				t.Errorf("error:\n%s\nwant:\n%s", err, c.want)
			}
		})
	}
}

// A top-level table named for the module itself, or by the key its
// [dependencies] gives a module it needs, belongs to that module's tools,
// which `nomi tool` runs under that name; any other is an error, the
// dependency's own [module].name among them.
func TestManifest_ARequiredModulesTableIsItsTools(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	kit := filepath.Join(root, "kit")
	for path, text := range map[string]string{
		filepath.Join(app, "nomi.toml"): "[module]\nname = \"app\"\n\n[dependencies]\ntools = { dir = \"../kit\" }\n\n[tools.database.main]\ndialect = \"sqlite\"\n\n[app]\nanything = 1\n",
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
	for _, c := range []struct{ manifest, want string }{
		{"[module]\nname = \"app\"\n\n[dependencies]\ntools = { dir = \"../kit\" }\n\n[kit.database.main]\ndialect = \"sqlite\"\n",
			":7: nomi.toml has no [kit] table; its tables are [dependencies], [effects], [i18n], [indirect], [module], [money]"},
		{"[module]\nname = \"app\"\n\n[kitt.database.main]\ndialect = \"sqlite\"\n",
			":4: nomi.toml has no [kitt] table; its tables are [dependencies], [effects], [i18n], [indirect], [module], [money]"},
	} {
		writeManifestFile(t, app, "nomi.toml", c.manifest)
		_, err := LoadManifest(app)
		if want := filepath.Join(app, "nomi.toml") + c.want; err == nil || err.Error() != want {
			t.Errorf("error: %v\nwant: %s", err, want)
		}
	}
}
