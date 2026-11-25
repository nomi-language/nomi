package pkgcache

import (
	"slices"
	"testing"
)

// An entry's `effects` and `effects_in_tests` are read with the entry: nil
// for a key it leaves out, empty for `[]`, and the names in order.
func TestParseManifest_EntryEffectsKeys(t *testing.T) {
	f, err := ParseManifest([]byte(`[dependencies]
a = { dir = "../a", effects = ["std::fs", "std::http"], effects_in_tests = ["std::process"] }
b = { dir = "../b", effects = [] }
c = { dir = "../c" }
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Requirement{
		{Name: "a", Dir: "../a", Effects: []string{"std::fs", "std::http"}, EffectsInTests: []string{"std::process"}},
		{Name: "b", Dir: "../b", Effects: []string{}},
		{Name: "c", Dir: "../c"},
	}
	if !slices.EqualFunc(f.Requires, want, Requirement.Same) {
		t.Fatalf("ParseManifest answered %+v, want %+v", f.Requires, want)
	}
	if f.Requires[1].Same(Requirement{Name: "b", Dir: "../b"}) {
		t.Errorf("an entry with `effects = []` is the same as one with no effects key")
	}
}

// Through is the entries of the project a package is reached through: the
// package's own when the project names it, and each entry whose
// dependencies, at any depth, lead to it.
func TestGraph_ThroughNamesTheEntriesThatLeadToAPackage(t *testing.T) {
	g := &Graph{Deps: []Dep{
		{Name: "fetch", Direct: true, Names: map[string]string{"core": "core", "fetch": "fetch"}},
		{Name: "reports", Direct: true, Names: map[string]string{"core": "core", "fmt": "fmt"}},
		{Name: "pricing", Direct: true, Names: map[string]string{}},
		{Name: "core", Names: map[string]string{"wire": "wire"}},
		{Name: "fmt", Names: map[string]string{}},
		{Name: "wire", Names: map[string]string{}},
	}}
	for _, c := range []struct {
		name string
		want []string
	}{
		{"fetch", []string{"fetch"}},
		{"pricing", []string{"pricing"}},
		{"fmt", []string{"reports"}},
		{"core", []string{"fetch", "reports"}},
		{"wire", []string{"fetch", "reports"}},
		{"nothing", nil},
	} {
		if got := g.Through(c.name); !slices.Equal(got, c.want) {
			t.Errorf("Through(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
