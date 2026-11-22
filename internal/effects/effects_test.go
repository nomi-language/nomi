package effects

import "testing"

// A list holds a call by its name, and every list allows an unlisted one.
func TestAllows(t *testing.T) {
	std := []string{"std::io"}
	cases := []struct {
		name string
		want bool
	}{
		{"std::io", true},
		{"std::fs", false},
		{"host:lookup", false},
		{"dbg", true},
	}
	for _, c := range cases {
		if got := Allows(std, c.name); got != c.want {
			t.Errorf("Allows(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

// A std list's entries are std names.
func TestEntryForms(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"std::fs", ""},
		{"std::startup", ""},
		{"host:clock", "names a function an embedding Go program supplies, which only that program can allow"},
		{"go:strings", `is not a std module; write one as an import does, such as "std::fs"`},
		{"fs", `is not a std module; write one as an import does, such as "std::fs"`},
	} {
		if got := NameProblem(c.name); got != c.want {
			t.Errorf("NameProblem(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// A hover's line names the effect the call has, and with
// lists whether they allow it, naming a module's entry that allows it too or
// the dependency whose own nomi.toml refuses it.
func TestReachedLine(t *testing.T) {
	self, deps, entry := []string{"`[effects.self]`"}, "`[effects.deps]`", "the entry for `lib.example/fetch`"
	for _, c := range []struct {
		name            string
		listed, allowed bool
		in              []string
		own             string
		want            string
	}{
		{"std::http", false, false, nil, "", "Has effects: `std::http`."},
		{"std::http", true, true, self, "", "Has effects: `std::http`, which `nomi.toml` allows in `[effects.self]`."},
		{"std::http", true, true, []string{deps, entry}, "", "Has effects: `std::http`, which `nomi.toml` allows in `[effects.deps]` and in the entry for `lib.example/fetch`."},
		{"std::http", true, true, nil, "", "Has effects: `std::http`, which no table in `nomi.toml` holds."},
		{"std::http", true, false, self, "", "Has effects: `std::http`, which `nomi.toml` does not allow in `[effects.self]`."},
		{"std::http", true, false, []string{entry}, "", "Has effects: `std::http`, which `nomi.toml` does not allow in the entry for `lib.example/fetch`."},
		{"std::fs", true, false, nil, "example.com/pricing", "Has effects: `std::fs`, which the `nomi.toml` of `example.com/pricing` does not allow."},
	} {
		if got := ReachedLine(Reached{Name: c.name, Allowed: c.allowed, In: c.in, Own: c.own}, c.listed); got != c.want {
			t.Errorf("ReachedLine(%s, listed %v, allowed %v, %q, %q) = %q, want %q", c.name, c.listed, c.allowed, c.in, c.own, got, c.want)
		}
	}
}

// A hover on a function says each effect it can have, through the functions
// on the way, the refused first and then in name order, four at most and a
// line naming the rest; nothing for a function that has no effects.
func TestFunctionReach(t *testing.T) {
	self := []string{"`[effects.self]`"}
	if got := FunctionReach(nil, true); got != "" {
		t.Errorf("FunctionReach of nothing = %q, want none", got)
	}
	one := []Reached{{Name: "std::fs", Through: []string{"read_settings"}, Allowed: true, In: self}}
	if got, want := FunctionReach(one, true), "Has effects: `std::fs` through `read_settings`, which `nomi.toml` allows in `[effects.self]`."; got != want {
		t.Errorf("FunctionReach = %q, want %q", got, want)
	}
	several := []Reached{
		{Name: "std::time", Allowed: true},
		{Name: "std::env", Allowed: true},
		{Name: "std::random", Allowed: true},
		{Name: "std::fs", Through: []string{"load", "read"}, In: self},
		{Name: "std::io", Allowed: true},
		{Name: "std::net", Allowed: true},
	}
	want := "Has effects: `std::fs` through `load`, `read`, which `nomi.toml` does not allow in `[effects.self]`.\n\n" +
		"Also has effects: `std::env`, `std::io`, `std::net`, `std::random`, `std::time`."
	if got := FunctionReach(several, true); got != want {
		t.Errorf("FunctionReach of six names =\n%s\nwant\n%s", got, want)
	}
	allowed := []Reached{
		{Name: "std::io", Allowed: true, In: self},
		{Name: "std::fs", Through: []string{"footer"}, Allowed: true, In: self},
	}
	if got, want := FunctionReach(allowed, true), "Has effects: `std::fs`, `std::io`."; got != want {
		t.Errorf("FunctionReach of two allowed names = %q, want %q", got, want)
	}
	if got, want := FunctionReach(several, false), "Has effects: `std::env`, `std::fs`, `std::io`, `std::net`, `std::random`, `std::time`."; got != want {
		t.Errorf("FunctionReach with no list = %q, want %q", got, want)
	}
}

// UsesLine names the app fields a function uses, five at most, then
// counts the rest.
func TestUsesLine(t *testing.T) {
	for _, tc := range []struct {
		fields []string
		want   string
	}{
		{[]string{"App.limit"}, "Uses `App.limit`."},
		{[]string{"App.label", "App.limit"}, "Uses `App.label`, `App.limit`."},
		{[]string{"App.a", "App.b", "App.c", "App.d", "App.e", "App.f", "App.g"},
			"Uses `App.a`, `App.b`, `App.c`, `App.d`, `App.e` and 2 more."},
	} {
		if got := UsesLine(tc.fields); got != tc.want {
			t.Errorf("UsesLine(%q) = %q, want %q", tc.fields, got, tc.want)
		}
	}
}

// A refusal's headline says the effect its subject has and what the list
// allows, no effects when it allows none.
func TestMessage(t *testing.T) {
	for _, c := range []struct {
		who, holder, them string
		allowed           []string
		want              string
	}{
		{"this program has", "the host", "", nil, "this program has the effect std::fs, and the host allows no effects"},
		{"this module's own code has", "[effects.self]", " it", nil,
			"this module's own code has the effect std::fs, and [effects.self] allows it no effects"},
		{"this module's tests have", "[effects.self]", " them", []string{"std::io", "std::time"},
			"this module's tests have the effect std::fs, and [effects.self] allows them only std::io, std::time"},
	} {
		if got := Message(c.who, "std::fs", c.holder, c.them, c.allowed); got != c.want {
			t.Errorf("Message(%q) = %q, want %q", c.who, got, c.want)
		}
	}
}

// EffectList names one effect, several joined with "and", or none.
func TestEffectList(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  string
	}{
		{nil, "no effects"},
		{[]string{"std::fs"}, "the effect std::fs"},
		{[]string{"std::fs", "std::io"}, "the effects std::fs and std::io"},
		{[]string{"std::env", "std::fs", "std::io"}, "the effects std::env, std::fs and std::io"},
	} {
		if got := EffectList(c.names); got != c.want {
			t.Errorf("EffectList(%q) = %q, want %q", c.names, got, c.want)
		}
	}
}
