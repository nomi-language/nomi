package analysis

import (
	"strings"
	"testing"
)

// withStdSources installs sources as std's for the test.
func withStdSources(t *testing.T, sources map[string]string) {
	t.Helper()
	SetStdExports(func() map[string]StdModuleExport { return StdExportsFromSources(sources) })
	t.Cleanup(func() { SetStdExports(nil) })
}

// A type name the file does not bind is answered with the import that
// brings it: one std module's, a few modules' each, or none. An internal
// module's type, a private type and a name no std module exports get no
// import; a module with public declarations is a module a value name may
// mean.
func TestStdImportHints(t *testing.T) {
	withStdSources(t, map[string]string{
		"http":      "pub enum Method\n    Get\n    Post\nend\n\npub struct Request\n    method: Method\nend\n",
		"glossary":  "pub opaque struct Term\n    n: Int\nend\n\nstruct Argument\n    a: Int\nend\n",
		"a":         "pub struct Shared\n    x: Int\nend\n",
		"b":         "pub struct Shared\n    x: Int\nend\n\npub struct Wide\n    x: Int\nend\n",
		"c":         "pub struct Wide\n    x: Int\nend\n",
		"d":         "pub struct Wide\n    x: Int\nend\n",
		"e":         "pub struct Wide\n    x: Int\nend\n",
		"http_host": "pub struct Wire\n    x: Int\nend\n",
		"io":        "pub fn print(s: String) end\n",
		"empty":     "fn f() end\n",
	})
	types := []struct{ name, want string }{
		{"Method", "import `std::http.Method`"},
		{"Term", "import `std::glossary.Term`"},
		{"Shared", "import `std::a.Shared` or `std::b.Shared`"},
		// Four modules export it: a list that long names none.
		{"Wide", ""},
		{"Argument", ""},
		{"Wire", ""},
		{"Nowhere", ""},
	}
	for _, c := range types {
		if got := stdTypeImportHint(c.name); got != c.want {
			t.Errorf("type %s: help %q, want %q", c.name, got, c.want)
		}
		e := unknownTypeError(NewTypeRegistry(), 1, 1, c.name)
		if c.want != "" && strings.Join(e.Hints, "\n") != c.want {
			t.Errorf("unknown type %s: help %q, want %q", c.name, e.Hints, c.want)
		}
	}
	modules := []struct{ name, want string }{
		{"io", "import `std::io`"},
		{"http", "import `std::http`"},
		{"http_host", ""},
		{"empty", ""},
		{"nowhere", ""},
	}
	for _, c := range modules {
		if got := stdModuleImportHint(c.name); got != c.want {
			t.Errorf("module %s: help %q, want %q", c.name, got, c.want)
		}
	}
}

// A type an expansion's hidden import registered (`phrase$.Noun`) is never
// offered by its alias.
func TestUnknownTypeNeverNamesAHiddenAlias(t *testing.T) {
	reg := NewTypeRegistry()
	reg.Register("phrase$.Noun", &StructType{Name: "Noun", Origin: "std/phrase"})
	reg.Register("Probe.Reading", &StructType{Name: "Probe.Reading"})
	if got := strings.Join(unknownTypeError(reg, 1, 1, "Noun").Hints, "\n"); strings.Contains(got, "$") {
		t.Errorf("help names a hidden alias: %q", got)
	}
	want := `did you mean "Probe.Reading"? That is one name, not "Reading" inside "Probe"`
	if got := strings.Join(unknownTypeError(reg, 1, 1, "Reading").Hints, "\n"); got != want {
		t.Errorf("help %q, want %q", got, want)
	}
}
