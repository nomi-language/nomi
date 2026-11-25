package irbuild

import (
	"strings"
	"testing"
)

// A failed typed literal's renderer in std/calendar's own `//!` cases
// calls the Debug body the checker's record of `Debug.inspect` at the
// error's type names (std/locale's `Error` for a `Locale"en"`), and every
// checked literal has one. The renderer was given no checker type for the
// error, so it rendered the error by its kind, choosing each nested Debug
// body with no checker type: 45 sites.
func TestLiteralRender_StdCalendarCasesReadTheRecord(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	stop := debugImplCensus.start()
	prog, res, err := GenerateStdlibTestIR("calendar", DeclineHooks{})
	sites := stop()
	if err != nil {
		t.Fatal(err)
	}
	if prog == nil || res == nil || len(res.IR) == 0 {
		t.Fatal("std/calendar's cases lowered to nothing, so the census saw no renderer")
	}
	checked, rendered := 0, 0
	for _, site := range res.Literals {
		if !site.Plain {
			checked++
			if site.Render != nil {
				rendered++
			}
		}
	}
	if checked == 0 || rendered != checked {
		t.Errorf("%d of std/calendar's %d checked literals have a failure renderer, want all", rendered, checked)
	}
	if got := sites[string(debugImplNoType)]; len(got) != 0 {
		t.Errorf("%d sites chose a nested Debug body with no checker type:\n  %s", len(got), strings.Join(got, "\n  "))
	}
}
