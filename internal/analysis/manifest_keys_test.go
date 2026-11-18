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

// [capabilities] reads into Capabilities; with no table there are no lists
// and nothing is enforced.
func TestManifest_Capabilities(t *testing.T) {
	m, err := ParseManifestData([]byte(`[module]
name = "app"

[capabilities]
allow = ["std::io", "std::fs"]
allow_in_tests = ["std::process"]

[capabilities.modules]
"example.com/pricing" = []
"example.com/pg" = ["go:github.com/jackc/pgx/v5"]
`), "m.toml")
	if err != nil {
		t.Fatal(err)
	}
	c := m.Capabilities
	if c == nil {
		t.Fatal("no Capabilities")
	}
	if want := []string{"std::io", "std::fs"}; !slices.Equal(c.Allow, want) {
		t.Errorf("Allow: %v, want %v", c.Allow, want)
	}
	if want := []string{"std::process"}; !slices.Equal(c.AllowInTests, want) {
		t.Errorf("AllowInTests: %v, want %v", c.AllowInTests, want)
	}
	if got := c.Modules["example.com/pricing"]; got == nil || len(got) != 0 {
		t.Errorf("pricing's list: %#v, want an empty list", got)
	}
	if want := []string{"go:github.com/jackc/pgx/v5"}; !slices.Equal(c.Modules["example.com/pg"], want) {
		t.Errorf("pg's list: %v, want %v", c.Modules["example.com/pg"], want)
	}

	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n"), "m.toml")
	if err != nil || m.Capabilities != nil {
		t.Errorf("no table: %+v, %v; want no Capabilities", m.Capabilities, err)
	}
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n[capabilities]\n"), "m.toml")
	if err != nil || m.Capabilities == nil || len(m.Capabilities.Allow) != 0 {
		t.Errorf("an empty table: %+v, %v; want lists that allow nothing", m.Capabilities, err)
	}
}

// An entry that is not a std module's or a Go package's name in form, a
// host name, and a name listed twice are errors at the entry's line.
func TestManifest_CapabilityEntryForms(t *testing.T) {
	cases := []struct{ name, table, want string }{
		{"no prefix", "[capabilities]\nallow = [\n    \"std::io\",\n    \"fs\",\n]\n",
			"m.toml:7: [capabilities] allow: \"fs\" is neither a std module (\"std::fs\") nor a Go package (\"go:<import path>\")"},
		{"a host name", "[capabilities]\nallow_in_tests = [\"host:clock\"]\n",
			"m.toml:5: [capabilities] allow_in_tests: \"host:clock\" names a function an embedding Go program supplies, which only that program can allow"},
		{"a std path", "[capabilities]\nallow = [\"std::fs/host\"]\n",
			"m.toml:5: [capabilities] allow: \"std::fs/host\" is not a std module; write one as an import does, such as \"std::fs\""},
		{"an empty Go path", "[capabilities.modules]\n\"example.com/a\" = [\"go:\"]\n",
			"m.toml:5: [capabilities] modules \"example.com/a\": \"go:\" is not a Go import path; write one as a `gopkg` declaration does, such as \"go:github.com/jackc/pgx/v5\""},
		{"twice", "[capabilities]\nallow = [\"std::io\", \"std::io\"]\n",
			"m.toml:5: [capabilities] allow lists std::io twice"},
		{"an unknown key", "[capabilities]\nallowed = [\"std::io\"]\n",
			"m.toml:5: [capabilities] has no key `allowed`; did you mean `allow`?"},
	}
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
