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
std = ["std::io", "std::fs"]
std_in_tests = ["std::process"]
go = ["example.com/pg"]
go_in_tests = ["example.com/minio"]
require_module_lists = true

[capabilities.modules]
"example.com/pricing" = []
"example.com/pg" = ["std::fs"]
`), "m.toml")
	if err != nil {
		t.Fatal(err)
	}
	c := m.Capabilities
	if c == nil {
		t.Fatal("no Capabilities")
	}
	for _, l := range []struct {
		name      string
		got, want []string
	}{
		{"Std", c.Std, []string{"std::io", "std::fs"}},
		{"StdInTests", c.StdInTests, []string{"std::process"}},
		{"Go", c.Go, []string{"example.com/pg"}},
		{"GoInTests", c.GoInTests, []string{"example.com/minio"}},
		{"pg's entry", c.Modules["example.com/pg"], []string{"std::fs"}},
	} {
		if !slices.Equal(l.got, l.want) {
			t.Errorf("%s: %v, want %v", l.name, l.got, l.want)
		}
	}
	if !c.RequireModuleLists {
		t.Error("RequireModuleLists is false; the table sets it")
	}
	if got := c.Modules["example.com/pricing"]; got == nil || len(got) != 0 {
		t.Errorf("pricing's list: %#v, want an empty list", got)
	}

	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n"), "m.toml")
	if err != nil || m.Capabilities != nil {
		t.Errorf("no table: %+v, %v; want no Capabilities", m.Capabilities, err)
	}
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n[capabilities]\n"), "m.toml")
	if err != nil || m.Capabilities == nil || len(m.Capabilities.Std) != 0 || len(m.Capabilities.Go) != 0 || m.Capabilities.RequireModuleLists {
		t.Errorf("an empty table: %+v, %v; want lists that allow nothing", m.Capabilities, err)
	}
}

// An entry of a std list that is not a std module's name in form, a Go
// package or a host name there, an entry of a Go list that is not a
// module path, and an entry listed twice are errors at the entry's line.
// Any other key is an error that says what each key holds, rather than
// the nearest in spelling, which for allow_in_tests is go_in_tests.
func TestManifest_CapabilityEntryForms(t *testing.T) {
	cases := []struct{ name, table, want string }{
		{"no prefix", "[capabilities]\nstd = [\n    \"std::io\",\n    \"fs\",\n]\n",
			"m.toml:7: [capabilities] std: \"fs\" is not a std module; write one as an import does, such as \"std::fs\""},
		{"a host name", "[capabilities]\nstd_in_tests = [\"host:clock\"]\n",
			"m.toml:5: [capabilities] std_in_tests: \"host:clock\" names a function an embedding Go program supplies, which only that program can allow"},
		{"a std path", "[capabilities]\nstd = [\"std::fs/host\"]\n",
			"m.toml:5: [capabilities] std: \"std::fs/host\" is not a std module; write one as an import does, such as \"std::fs\""},
		{"a Go package in std", "[capabilities]\nstd = [\"go:github.com/jackc/pgx/v5\"]\n",
			"m.toml:5: [capabilities] std: \"go:github.com/jackc/pgx/v5\" names a Go package; Go is allowed by module, not by package: list the module that binds it under `go`"},
		{"a Go package in a module's entry", "[capabilities.modules]\n\"example.com/a\" = [\"go:strings\"]\n",
			"m.toml:5: [capabilities] modules \"example.com/a\": \"go:strings\" names a Go package; Go is allowed by module, not by package: list the module that binds it under `go`"},
		{"a Go package in go", "[capabilities]\ngo = [\"go:example.com/pg\"]\n",
			"m.toml:5: [capabilities] go: \"go:example.com/pg\" names a Go package; name the module that binds it, as go.mod writes it, such as \"example.com/pg\""},
		{"a std module in go_in_tests", "[capabilities]\ngo_in_tests = [\"std::fs\"]\n",
			"m.toml:5: [capabilities] go_in_tests: \"std::fs\" is a std module, which a list of std names holds; this list names modules, such as \"example.com/pg\""},
		{"an empty module path", "[capabilities]\ngo = [\"\"]\n",
			"m.toml:5: [capabilities] go: \"\" is not a module path; write one as go.mod writes it, such as \"example.com/pg\""},
		{"twice", "[capabilities]\nstd = [\"std::io\", \"std::io\"]\n",
			"m.toml:5: [capabilities] std lists std::io twice"},
		{"a module twice", "[capabilities]\ngo = [\"example.com/pg\", \"example.com/pg\"]\n",
			"m.toml:5: [capabilities] go lists example.com/pg twice"},
		{"an unknown key", "[capabilities]\nstds = [\"std::io\"]\n",
			"m.toml:5: [capabilities] has no key `stds`; `std` and `std_in_tests` list std modules, `go` and `go_in_tests` the modules whose code may bind Go, and `modules` and `require_module_lists` are about the modules the project requires"},
		{"allow", "[capabilities]\nallow = [\"std::io\"]\n",
			"m.toml:5: [capabilities] has no key `allow`; `std` and `std_in_tests` list std modules, `go` and `go_in_tests` the modules whose code may bind Go, and `modules` and `require_module_lists` are about the modules the project requires"},
		{"allow_in_tests", "[capabilities]\nallow_in_tests = [\"std::io\"]\n",
			"m.toml:5: [capabilities] has no key `allow_in_tests`; `std` and `std_in_tests` list std modules, `go` and `go_in_tests` the modules whose code may bind Go, and `modules` and `require_module_lists` are about the modules the project requires"},
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
