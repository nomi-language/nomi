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
		{"a misspelled key", "[package]\nname = \"app\"\nnme = 1\n",
			"m.toml:3: [package] has no key `nme`; did you mean `name`?"},
		{"a misspelled table", "[package]\nname = \"app\"\n\n[i18m]\nlocales = [\"en\"]\n",
			"m.toml:4: nomi.toml has no [i18m] table; did you mean [i18n]?"},
		{"a table far from every other", "[package]\nname = \"app\"\n[bogus]\nx = 1\n",
			"m.toml:3: nomi.toml has no [bogus] table; its tables are [dependencies], [effects], [i18n], [money], [package]"},
		{"a [module] table, which is no table of nomi.toml's", "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
			"m.toml:1: nomi.toml has no [module] table; its tables are [dependencies], [effects], [i18n], [money], [package]"},
		{"an [indirect] table, which is no table of nomi.toml's", "[package]\nname = \"app\"\n\n[indirect]\n\"example.com/strutil\" = \"v1.1.0\"\n",
			"m.toml:4: nomi.toml has no [indirect] table; its tables are [dependencies], [effects], [i18n], [money], [package]"},
		{"a key outside every table", "top = 1\n[package]\nname = \"app\"\n",
			"m.toml:1: nomi.toml has no top-level key `top`; every key is in a table ([dependencies], [effects], [i18n], [money], [package])"},
		{"a key far from every other", "[package]\nname = \"app\"\nwhatever = true\n",
			"m.toml:3: [package] has no key `whatever`; its keys are `name`, `nomi`, `entry_points`"},
		{"a dotted key", "[package]\nname = \"app\"\ni18n.localez = [\"en\"]\n",
			"m.toml:3: [package] has no key `i18n`; its keys are `name`, `nomi`, `entry_points`"},
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
	// A project's nomi.toml may also hold its tools' tables, each named for
	// the package or a key of its [dependencies]: the list names those the
	// manifest names, or says what names one when it names none.
	projectCases := []struct{ name, toml, want string }{
		{"a [module] table, in a manifest that names no package", "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
			":1: nomi.toml has no [module] table; its tables are [dependencies], [effects], [i18n], [money], [package], and its tools' tables, each named for the package or a dependency"},
		{"a table far from every other, with dependencies", "[package]\nname = \"app\"\n\n[dependencies]\nsqlkit = { dir = \"../sqlkit\" }\nkit = { dir = \"../kit\" }\n\n[bogus]\nx = 1\n",
			":8: nomi.toml has no [bogus] table; its tables are [dependencies], [effects], [i18n], [money], [package], and its tools' tables [app], [kit] and [sqlkit]"},
	}
	for _, c := range projectCases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			m, err := ParseManifestIn(dir, []byte(c.toml))
			want := filepath.Join(dir, "nomi.toml") + c.want
			if err == nil {
				t.Fatalf("the manifest is accepted (%+v); want %q", m, want)
			}
			if err.Error() != want {
				t.Errorf("error:\n%s\nwant:\n%s", err, want)
			}
		})
	}
}

// [effects.self], [effects.dependencies] and the effects keys of the
// [dependencies] entries read into Effects; with none of them there are
// no lists and nothing is held.
func TestManifest_Effects(t *testing.T) {
	m, err := ParseManifestData([]byte(`[package]
name = "app"

[dependencies]
pricing = { dir = "../pricing", effects = [] }
pg = { dir = "../pg", effects = ["std::fs"], effects_in_tests = ["std::process"] }
kit = { dir = "../kit" }

[effects.self]
effects = ["std::io", "std::fs"]
effects_in_tests = ["std::process"]

[effects.dependencies]
entries_required = true
effects = ["std::fs", "std::http"]
effects_in_tests = ["std::instant"]
`), "m.toml")
	if err != nil {
		t.Fatal(err)
	}
	c := m.Effects
	if c == nil || c.Self == nil || c.Dependencies == nil {
		t.Fatalf("Effects %+v; want both tables", c)
	}
	for _, l := range []struct {
		name      string
		got, want []string
	}{
		{"Self.Effects", c.Self.Effects, []string{"std::io", "std::fs"}},
		{"Self.EffectsInTests", c.Self.EffectsInTests, []string{"std::process"}},
		{"Dependencies.Effects", c.Dependencies.Effects, []string{"std::fs", "std::http"}},
		{"Dependencies.EffectsInTests", c.Dependencies.EffectsInTests, []string{"std::instant"}},
		{"pg's entry", c.Dependencies.Entries["pg"], []string{"std::fs"}},
		{"pg's entry in tests", c.Dependencies.EntriesInTests["pg"], []string{"std::process"}},
	} {
		if !slices.Equal(l.got, l.want) {
			t.Errorf("%s: %v, want %v", l.name, l.got, l.want)
		}
	}
	if !c.Dependencies.EntriesRequired {
		t.Error("EntriesRequired is false; the table sets it")
	}
	if got := c.Dependencies.Entries["pricing"]; got == nil || len(got) != 0 {
		t.Errorf("pricing's list: %#v, want an empty list", got)
	}
	if _, has := c.Dependencies.Entries["kit"]; has {
		t.Errorf("kit, whose entry has no effects key, has an entry: %v", c.Dependencies.Entries)
	}

	for _, tc := range []struct{ name, toml string }{
		{"no table", ""},
		{"an empty [effects]", "[effects]\n"},
	} {
		m, err = ParseManifestData([]byte("[package]\nname = \"app\"\n"+tc.toml), "m.toml")
		if err != nil || m.Effects != nil {
			t.Errorf("%s: %+v, %v; want no Effects", tc.name, m.Effects, err)
		}
	}
	// Each table alone holds only what it names, and an empty one holds
	// that code to nothing: no std call, and no ceiling for deps.
	m, err = ParseManifestData([]byte("[package]\nname = \"app\"\n[effects.self]\n"), "m.toml")
	if err != nil || m.Effects == nil || m.Effects.Self == nil || m.Effects.Dependencies != nil ||
		len(m.Effects.Self.Effects) != 0 {
		t.Errorf("self alone: %+v, %v; want an empty self and no deps", m.Effects, err)
	}
	m, err = ParseManifestData([]byte("[package]\nname = \"app\"\n[effects.dependencies]\n"), "m.toml")
	if err != nil || m.Effects == nil || m.Effects.Self != nil || m.Effects.Dependencies == nil ||
		m.Effects.Dependencies.Effects != nil || m.Effects.Dependencies.EntriesRequired {
		t.Errorf("deps alone: %+v, %v; want an empty deps with no ceiling and no self", m.Effects, err)
	}
	m, err = ParseManifestData([]byte("[package]\nname = \"app\"\n[effects.dependencies]\neffects = []\n"), "m.toml")
	if err != nil || m.Effects.Dependencies.Effects == nil || len(m.Effects.Dependencies.Effects) != 0 {
		t.Errorf("deps with effects = []: %+v, %v; want a ceiling that allows nothing", m.Effects, err)
	}
	m, err = ParseManifestData([]byte("[package]\nname = \"app\"\n[dependencies]\npg = { dir = \"../pg\", effects = [] }\n"), "m.toml")
	if err != nil || m.Effects == nil || m.Effects.Self != nil || m.Effects.Dependencies == nil || m.Effects.Dependencies.Entries["pg"] == nil ||
		m.Effects.Dependencies.Effects != nil {
		t.Errorf("an entry's effects key alone: %+v, %v; want dependencies held by the entry, with no ceiling", m.Effects, err)
	}
	m, err = ParseManifestData([]byte("[package]\nname = \"app\"\n[dependencies]\npg = { dir = \"../pg\" }\n"), "m.toml")
	if err != nil || m.Effects != nil {
		t.Errorf("an entry with no effects key: %+v, %v; want no Effects", m.Effects, err)
	}
	// [effects.self.modules] is under [effects.self]: alone it
	// gives a self with no std, and its entries; an empty one is a table
	// with no entries, and self without it has none at all.
	m, err = ParseManifestData([]byte("[package]\nname = \"app\"\n[effects.self.modules]\n\"pricing\" = []\n\"reports::daily\" = [\"std::fs\"]\n"), "m.toml")
	if err != nil || m.Effects == nil || m.Effects.Self == nil || m.Effects.Dependencies != nil || len(m.Effects.Self.Effects) != 0 ||
		m.Effects.Self.Modules["pricing"] == nil || len(m.Effects.Self.Modules["pricing"]) != 0 ||
		fmt.Sprint(m.Effects.Self.Modules["reports::daily"]) != "[std::fs]" {
		t.Errorf("self.modules alone: %+v, %v; want a self table with the two entries", m.Effects, err)
	}
	m, err = ParseManifestData([]byte("[package]\nname = \"app\"\n[effects.self]\nentries_required = true\n\n[effects.self.modules]\n"), "m.toml")
	if err != nil || m.Effects.Self.Modules == nil || len(m.Effects.Self.Modules) != 0 || !m.Effects.Self.EntriesRequired {
		t.Errorf("an empty self.modules with entries_required: %+v, %v; want the table with no entries, and entries_required", m.Effects.Self, err)
	}
	m, err = ParseManifestData([]byte("[package]\nname = \"app\"\n[effects.self]\neffects = []\n"), "m.toml")
	if err != nil || m.Effects.Self.Modules != nil || m.Effects.Self.EntriesRequired {
		t.Errorf("self with no table of entries: %+v, %v; want no entries and no entries_required", m.Effects.Self, err)
	}
}

// capsAbout is what the unknown-key error says each key of an effect
// table holds.
const (
	capsAbout = "it holds two tables: [effects.self] is the effects this package's own code may have, and [effects.dependencies] the effects the code of the packages it depends on may have"
	selfAbout = "`effects` lists the effects this package's own code may have, by std module, and `effects_in_tests` those its tests may also have, `entries_required` makes every module of the project have an entry, and [effects.self.modules] holds an entry for each of its modules"
	depsAbout = "`effects` and `effects_in_tests` list the most effects any dependency may have, and `entries_required` makes every [dependencies] entry have an `effects` key; a dependency's own allowance is the `effects` key of its [dependencies] entry"
)

// An entry of a std list that is not a std module's name in form, a host
// name there, an entry listed twice, and deps' effects_in_tests without its
// std are errors at the entry's line. Any other key is an error that says
// what each key holds, rather than the nearest in spelling, which for
// allow_in_tests is effects_in_tests; the keys [effects] held directly are
// among them, and so are `go` and `go_in_tests`, keys no effects table
// has, and a `go:` entry, which names no std module.
func TestManifest_EffectEntryForms(t *testing.T) {
	cases := []struct{ name, table, want string }{
		{"no prefix", "[effects.self]\neffects = [\n    \"std::io\",\n    \"fs\",\n]\n",
			"m.toml:7: [effects.self] effects: \"fs\" is not a std module; write one as an import does, such as \"std::fs\""},
		{"a host name", "[effects.self]\neffects_in_tests = [\"host:clock\"]\n",
			"m.toml:5: [effects.self] effects_in_tests: \"host:clock\" names a function an embedding Go program supplies, which only that program can allow"},
		{"a std path", "[effects.dependencies]\neffects = [\"std::fs/host\"]\n",
			"m.toml:5: [effects.dependencies] effects: \"std::fs/host\" is not a std module; write one as an import does, such as \"std::fs\""},
		{"a go: name in self's std", "[effects.self]\neffects = [\"go:github.com/jackc/pgx/v5\"]\n",
			"m.toml:5: [effects.self] effects: \"go:github.com/jackc/pgx/v5\" is not a std module; write one as an import does, such as \"std::fs\""},
		{"a go: name in a dependency's entry", "[dependencies]\na = { dir = \"../a\", effects = [\"go:strings\"] }\n",
			"m.toml:5: [dependencies] a.effects: \"go:strings\" is not a std module; write one as an import does, such as \"std::fs\""},
		{"a host name in a dependency's entry", "[dependencies]\na = { dir = \"../a\", effects = [], effects_in_tests = [\"host:clock\"] }\n",
			"m.toml:5: [dependencies] a.effects_in_tests: \"host:clock\" names a function an embedding Go program supplies, which only that program can allow"},
		{"a name twice in a dependency's entry", "[dependencies]\na = { dir = \"../a\", effects = [\"std::fs\", \"std::fs\"] }\n",
			"m.toml:5: [dependencies] a.effects lists std::fs twice"},
		{"an entry's effects_in_tests without its effects", "[dependencies]\na = { dir = \"../a\", effects_in_tests = [\"std::fs\"] }\n",
			"m.toml:5: [dependencies] a.effects_in_tests adds to its effects, which the entry leaves out; with no effects, only [effects.dependencies] holds a"},
		{"a misspelled key in a dependency's entry", "[dependencies]\na = { dir = \"../a\", efects = [] }\n",
			"m.toml:5: [dependencies.a] has no key `efects`; did you mean `effects`?"},
		{"a name twice in an entry of the project's own", "[effects.self.modules]\n\"pricing\" = [\"std::fs\", \"std::fs\"]\n",
			"m.toml:5: [effects.self.modules] \"pricing\" lists std::fs twice"},
		{"an empty key in the project's entries", "[effects.self.modules]\n\"\" = []\n",
			"m.toml:5: [effects.self.modules] has an empty key; write a module as the project's own code imports it, such as \"pricing\""},
		{"twice", "[effects.self]\neffects = [\"std::io\", \"std::io\"]\n",
			"m.toml:5: [effects.self] effects lists std::io twice"},
		{"go in self", "[effects.self]\neffects = []\ngo = true\n",
			"m.toml:6: [effects.self] has no key `go`; " + selfAbout},
		{"go_in_tests in self", "[effects.self]\ngo_in_tests = false\n",
			"m.toml:5: [effects.self] has no key `go_in_tests`; " + selfAbout},
		{"go in deps", "[effects.dependencies]\ngo = [\"pg\"]\n",
			"m.toml:5: [effects.dependencies] has no key `go`; " + depsAbout},
		{"go_in_tests in deps", "[effects.dependencies]\neffects = []\ngo_in_tests = [\"pg\"]\n",
			"m.toml:6: [effects.dependencies] has no key `go_in_tests`; " + depsAbout},
		{"the ceiling's effects_in_tests without effects", "[effects.dependencies]\neffects_in_tests = [\"std::fs\"]\n",
			"m.toml:5: [effects.dependencies] effects_in_tests adds to effects, which the table leaves out; with no effects, no ceiling holds the dependencies"},
		{"an unknown key in self", "[effects.self]\nstds = [\"std::io\"]\n",
			"m.toml:5: [effects.self] has no key `stds`; " + selfAbout},
		{"allow_in_tests in self", "[effects.self]\nallow_in_tests = [\"std::io\"]\n",
			"m.toml:5: [effects.self] has no key `allow_in_tests`; " + selfAbout},
		{"entries in self", "[effects.self]\nentries = true\n",
			"m.toml:5: [effects.self] has no key `entries`; " + selfAbout},
		{"an unknown key in deps", "[effects.dependencies]\nrequire = true\n",
			"m.toml:5: [effects.dependencies] has no key `require`; " + depsAbout},
		{"another table nested in self", "[effects.self.mods]\n\"pricing\" = []\n",
			"m.toml:4: [effects.self] has no key `mods`; " + selfAbout},
		{"a third table", "[effects.all]\neffects = []\n",
			"m.toml:4: [effects] has no key `all`; " + capsAbout},
		// Names these tables do not have are unknown keys like any other.
		{"a table named deps", "[effects.deps]\neffects = []\n",
			"m.toml:4: [effects] has no key `deps`; " + capsAbout},
		{"a table of entries under deps", "[effects.deps.packages]\n\"pg\" = []\n",
			"m.toml:4: [effects] has no key `deps`; " + capsAbout},
		{"a table of entries under dependencies", "[effects.dependencies.packages]\n\"pg\" = []\n",
			"m.toml:4: [effects.dependencies] has no key `packages`; " + depsAbout},
		{"std in self", "[effects.self]\nstd = [\"std::io\"]\n",
			"m.toml:5: [effects.self] has no key `std`; " + selfAbout},
		{"std_in_tests in dependencies", "[effects.dependencies]\neffects = []\nstd_in_tests = [\"std::io\"]\n",
			"m.toml:6: [effects.dependencies] has no key `std_in_tests`; " + depsAbout},
	}
	// Each key [effects] held directly, and its table of entries, is
	// an error that names the two tables.
	for _, key := range []string{"std", "effects_in_tests", "require_module_lists"} {
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
			m, err := ParseManifestData([]byte("[package]\nname = \"app\"\n\n"+c.table), "m.toml")
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
// dependency's own [package].name among them.
func TestManifest_ARequiredModulesTableIsItsTools(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	kit := filepath.Join(root, "kit")
	for path, text := range map[string]string{
		filepath.Join(app, "nomi.toml"): "[package]\nname = \"app\"\n\n[dependencies]\ntools = { dir = \"../kit\" }\n\n[tools.database.main]\ndialect = \"sqlite\"\n\n[app]\nanything = 1\n",
		filepath.Join(kit, "nomi.toml"): "[package]\nname = \"kit\"\n",
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
		{"[package]\nname = \"app\"\n\n[dependencies]\ntools = { dir = \"../kit\" }\n\n[kit.database.main]\ndialect = \"sqlite\"\n",
			":7: nomi.toml has no [kit] table; its tables are [dependencies], [effects], [i18n], [money], [package], and its tools' tables [app] and [tools]"},
		{"[package]\nname = \"app\"\n\n[kitt.database.main]\ndialect = \"sqlite\"\n",
			":4: nomi.toml has no [kitt] table; its tables are [dependencies], [effects], [i18n], [money], [package], and its tools' tables [app]"},
	} {
		writeManifestFile(t, app, "nomi.toml", c.manifest)
		_, err := LoadManifest(app)
		if want := filepath.Join(app, "nomi.toml") + c.want; err == nil || err.Error() != want {
			t.Errorf("error: %v\nwant: %s", err, want)
		}
	}
}
