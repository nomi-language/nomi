package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/std"
)

// In std/calendar's gen, its own `Error`'s functions hide std/locale's under
// the spelling `Error.to_string` (stdlibIndex.underlay): both modules
// declare an `Error` that implements Display. A record that names locale's
// finds it by its identity all the same, and so does every function
// of every module calendar's lowering resolves against, a module-level one
// included.
func TestStdFuncByID_AnotherModulesFunctionIsNotHidden(t *testing.T) {
	ensureStdModule("calendar")
	lib := std.Shared()
	full := stdlibLowering()
	const module = "calendar"
	earlierMods := full.earlierMods[module]
	if !earlierMods["locale"] {
		t.Fatalf("std/calendar's lowering does not resolve against std/locale (%d earlier modules), so this test checks nothing", len(earlierMods))
	}
	earlier := full.filtered(func(m string) bool { return earlierMods[m] })
	v := stdModuleContext(module, fileuri.ToPath(lib.FileURI(module)), full.modulePkg[module], lib.Nodes[module], lib.Files[module], earlier)
	g := v.gen(earlier)

	locale := analysis.StdFunc{Module: "std/locale", Owner: "Error", Impl: "Display", Name: "to_string"}
	for _, f := range g.std.byType["Error.to_string"] {
		if f.module != module {
			t.Fatalf("calendar's gen files %s's Error.to_string under its spelling, so nothing hides it and this test checks nothing", f.module)
		}
	}
	if len(g.std.byType["Error.to_string"]) == 0 {
		t.Fatal("calendar's gen files no Error.to_string of its own, so nothing hides std/locale's and this test checks nothing")
	}
	if f := g.stdFuncByID(locale); f == nil || f.module != "locale" || f.id != locale {
		t.Fatalf("stdFuncByID(%+v) in calendar's gen = %v, want std/locale's function", locale, f)
	}

	found, moduleLevel := 0, 0
	for m := range earlierMods {
		ev := full.views[m]
		if ev == nil {
			continue
		}
		for _, c := range ev.cands {
			if c.f.id.IsZero() {
				t.Errorf("std/%s's %s has no identity, so no record finds it", m, c.f.key)
				continue
			}
			if got := g.stdFuncByID(c.f.id); got != c.f {
				t.Errorf("stdFuncByID(%+v) in calendar's gen = %v, want std/%s's %s", c.f.id, got, m, c.f.key)
				continue
			}
			found++
			if c.f.id.Owner == "" {
				moduleLevel++
			}
		}
	}
	for c := range v.settled {
		got := g.stdFuncByID(c.f.id)
		if got == nil || got.canon != c.f {
			t.Errorf("stdFuncByID(%+v) in calendar's gen = %v, want calendar's own %s", c.f.id, got, c.f.key)
		}
	}
	t.Logf("found %d functions of %d earlier modules by identity, %d of them module-level, and %d of calendar's own", found, len(earlierMods), moduleLevel, len(v.settled))
	if found < 500 || moduleLevel < 50 {
		t.Fatalf("found %d earlier modules' functions by identity, %d of them module-level, which is too few to check anything", found, moduleLevel)
	}
}
