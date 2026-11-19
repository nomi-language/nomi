package capability

import "testing"

// A Go binding is held by the modules list, by the module whose files hold
// it, and any other call by the std list, by its name; neither list
// allows the other's calls.
func TestAllows(t *testing.T) {
	std, goModules := []string{"std::io"}, []string{"example.com/pg"}
	cases := []struct {
		name, module string
		want         bool
	}{
		{"std::io", "", true},
		{"std::fs", "", false},
		{"go:strings", "example.com/pg", true},
		{"go:example.com/pg/goside", "example.com/pg", true},
		{"go:strings", "example.com/app", false},
		{"host:lookup", "", false},
	}
	for _, c := range cases {
		if got := Allows(std, goModules, c.name, c.module); got != c.want {
			t.Errorf("Allows(%s in %q) = %v, want %v", c.name, c.module, got, c.want)
		}
	}
	if Allows([]string{"go:strings"}, nil, "go:strings", "example.com/pg") {
		t.Error("a go: name in a std list allows a Go binding")
	}
}

// A std list's entries are std names; a module list's are module paths.
func TestEntryForms(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"std::fs", ""},
		{"std::startup", ""},
		{"go:strings", "names a Go package; FIX"},
		{"fs", `is not a std module; write one as an import does, such as "std::fs"`},
	} {
		if got := NameProblem(c.name, "FIX"); got != c.want {
			t.Errorf("NameProblem(%q) = %q, want %q", c.name, got, c.want)
		}
	}
	for _, c := range []struct{ path, want string }{
		{"example.com/pg", ""},
		{"app", ""},
		{"std::fs", `is a std module, which a list of std names holds; this list names modules, such as "example.com/pg"`},
		{"go:example.com/pg", `names a Go package; name the module that binds it, as go.mod writes it, such as "example.com/pg"`},
		{"", `is not a module path; write one as go.mod writes it, such as "example.com/pg"`},
	} {
		if got := ModuleProblem(c.path); got != c.want {
			t.Errorf("ModuleProblem(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// A hover's line names what the call reaches, or the Go it binds, and with
// lists whether they allow it, naming a module's entry that allows it too or
// the dependency whose own nomi.toml refuses it.
func TestReachedLine(t *testing.T) {
	self, deps, entry := []string{"`[capabilities.self]`"}, "`[capabilities.deps]`", "the entry for `lib.example/fetch`"
	for _, c := range []struct {
		name            string
		listed, allowed bool
		in              []string
		own             string
		want            string
	}{
		{"std::http", false, false, nil, "", "Reaches `std::http`."},
		{"go:example.com/pg/goside", false, false, nil, "", "Binds Go: `go:example.com/pg/goside`."},
		{"std::http", true, true, self, "", "Reaches `std::http`, which `nomi.toml` allows in `[capabilities.self]`."},
		{"std::http", true, true, []string{deps, entry}, "", "Reaches `std::http`, which `nomi.toml` allows in `[capabilities.deps]` and in the entry for `lib.example/fetch`."},
		{"std::http", true, true, nil, "", "Reaches `std::http`, which no table in `nomi.toml` holds."},
		{"go:example.com/pg/goside", true, true, []string{deps}, "", "Binds Go: `go:example.com/pg/goside`, which `nomi.toml` allows in `[capabilities.deps]`."},
		{"std::http", true, false, self, "", "Reaches `std::http`, which `nomi.toml` does not allow in `[capabilities.self]`."},
		{"std::http", true, false, []string{entry}, "", "Reaches `std::http`, which `nomi.toml` does not allow in the entry for `lib.example/fetch`."},
		{"std::fs", true, false, nil, "example.com/pricing", "Reaches `std::fs`, which the `nomi.toml` of `example.com/pricing` does not allow."},
	} {
		if got := ReachedLine(c.name, c.listed, c.allowed, c.in, c.own); got != c.want {
			t.Errorf("ReachedLine(%s, listed %v, allowed %v, %q, %q) = %q, want %q", c.name, c.listed, c.allowed, c.in, c.own, got, c.want)
		}
	}
}
