package analysis

import "testing"

// TestStdlibModuleForPathNamesAFlatStdlibModule is the direct witness for the
// question three sites used to answer separately.
//
// The three consumers are covered by their own tests — the LSP's stdlib branch,
// the Origin a single-file analysis stamps, and the missing-impl suppression —
// but each of those reaches the answer through a whole analysis, so a failure
// there says "46 diagnostics" and not "the path rule is wrong". This table says
// which.
//
// The rows that matter are the last two `false` ones. The stdlib is ONE Nomi
// module and every public module in it is a flat `std/<name>.nomi`, so the rule
// is "the immediate parent directory is `std`". A facade nested at
// `std/<name>/<name>.nomi` used to exist, answered NO here, and cost 46
// self-diagnostics on the open document; the two rows pin that a nested path is
// refused rather than silently renamed, so re-nesting a facade fails loudly.
func TestStdlibModuleForPathNamesAFlatStdlibModule(t *testing.T) {
	for _, c := range []struct {
		path string
		want string
		ok   bool
	}{
		// The flat layout, in all three physical shapes that reach here.
		{"/repo/std/json.nomi", "json", true},
		{"/Users/x/.cache/nomi/std/json.nomi", "json", true},
		{"/Users/x/.cache/nomi/std/0123456789abcdef/json.nomi", "json", true},
		// A version directory names std only directly under `std`, and
		// only when it is shaped like one.
		{"/Users/x/project/0123456789abcdef/json.nomi", "", false},
		{"/Users/x/.cache/nomi/std/0123456789abcdeg/json.nomi", "", false},
		{"/Users/x/.cache/nomi/std/0123456789abcdef/_fixtures/nested/deeper/module.nomi", "", false},
		{"std/json.nomi", "json", true},
		// The four adapters are flat too — their directories hold Go
		// support and no Nomi source.
		{"/repo/std/calendar.nomi", "calendar", true},
		{"/repo/std/regex.nomi", "regex", true},
		{"std/random.nomi", "random", true},
		// Not stdlib.
		{"", "", false},
		{"/repo/project/main.nomi", "", false},
		{"/repo/tests/05-calendar-and-time/dates_test.nomi", "", false},
		// A checkout living under a directory of its own called `std`
		// cannot shadow the real root.
		{"/home/std/repo/std/int.nomi", "int", true},
		// A directory handed in by mistake must not read as a module
		// called "" — the file's own name is never a candidate root.
		{"/repo/std", "", false},
		// Nested under `std/` is NOT a module this can name. The tree has
		// one such path — std/_fixtures' nested tree, which std.Load does
		// not carry either — and a re-nested adapter facade would be the
		// other, which is the regression this row exists to catch.
		{"/repo/std/_fixtures/nested/deeper/module.nomi", "", false},
		{"/repo/std/calendar/calendar.nomi", "", false},
		// A non-Nomi file under `std/` is not a module either — the
		// adapter directories are full of Go.
		{"/repo/std/std.go", "", false},
	} {
		got, ok := stdlibModuleForPath(c.path)
		if ok != c.ok || got != c.want {
			t.Errorf("stdlibModuleForPath(%q) = (%q, %v), want (%q, %v)",
				c.path, got, ok, c.want, c.ok)
		}
	}
}

// TestOriginForStandalonePathIsTheModulesOwnKey is the consequence, at the site
// where getting it wrong cost 46 diagnostics: the Origin a stdlib file's own
// declarations carry has to be the key its imports resolve to, or its `Date` is
// not the `Date` its own signatures name.
func TestOriginForStandalonePathIsTheModulesOwnKey(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{"/repo/std/json.nomi", "std/json"},
		{"/repo/std/calendar.nomi", "std/calendar"},
		{"/repo/project/main.nomi", OriginEntry},
		{"", OriginEntry},
	} {
		if got := originForStandalonePath(c.path); got != c.want {
			t.Errorf("originForStandalonePath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
