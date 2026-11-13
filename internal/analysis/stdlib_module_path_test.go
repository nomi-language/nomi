package analysis

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStdlibModuleForPathNamesAFlatStdlibModule is the direct witness for the
// question three sites used to answer separately.
//
// The three consumers are covered by their own tests — the LSP's stdlib branch,
// the Origin a single-file analysis stamps, and the missing-impl suppression —
// but each of those reaches the answer through a whole analysis, so a failure
// there says "46 diagnostics" and not "the path rule is wrong". This table says
// which.
//
// The stdlib is ONE Nomi module and every module's name is its path: a
// `std/<name>.nomi`, or a `std/<parent>/<name>.nomi` beside its parent's file.
// A facade nested at `std/<name>/<name>.nomi` used to exist under the name
// `<name>`, answered NO here, and cost 46 self-diagnostics on the open
// document; its row pins that such a path is refused rather than silently
// renamed, so re-nesting a facade fails loudly.
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
		// A module nested under its parent, key-shaped. On disk it needs
		// its parent's file beside the directory, which /repo lacks
		// (TestStdlibModuleForPathNamesANestedModuleOnDisk has one).
		{"std/storage/testing.nomi", "storage/testing", true},
		{"/repo/std/storage/testing.nomi", "", false},
		// std/_fixtures' nested tree, which std.Load does not carry, and a
		// re-nested adapter facade, which is the regression this row
		// exists to catch, are not modules this can name.
		{"/repo/std/_fixtures/nested/deeper/module.nomi", "", false},
		{"/repo/std/_fixtures/conn.nomi", "", false},
		{"std/calendar/calendar.nomi", "", false},
		{"/repo/std/calendar/calendar.nomi", "", false},
		{"std/storage/deeper/testing.nomi", "", false},
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

// A nested module on disk is named by its path when its parent's file sits
// beside its directory, as std/storage.nomi sits beside std/storage/; a
// project under a directory called `std`, with no such file, is not std.
func TestStdlibModuleForPathNamesANestedModuleOnDisk(t *testing.T) {
	root := filepath.Join(t.TempDir(), "std")
	for _, f := range []string{"storage.nomi", "storage/testing.nomi", "project/main.nomi"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		path, want string
		ok         bool
	}{
		{filepath.Join(root, "storage", "testing.nomi"), "storage/testing", true},
		{filepath.Join(root, "project", "main.nomi"), "", false},
	} {
		got, ok := stdlibModuleForPath(c.path)
		if ok != c.ok || got != c.want {
			t.Errorf("stdlibModuleForPath(%q) = (%q, %v), want (%q, %v)", c.path, got, ok, c.want, c.ok)
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
