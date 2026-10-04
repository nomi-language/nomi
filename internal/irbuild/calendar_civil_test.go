package irbuild

import (
	"github.com/nomi-language/nomi/internal/stdcalendar"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// TestCalendarUnitsAreNominallyDistinctAtTheGoLevel is the check the width guard
// cannot make and the fixture cannot reach.
//
// Ten units all wrap `Int`, so every one of them is `int64` underneath and
// TestOpaqueGoWidthMatchesTheDeclaredInner is satisfied by any of them being any
// of the others. What must ALSO hold is that they are ten DIFFERENT Go types:
// the builder selects a std overload by comparing parameter kinds, and
// `impl Add<Hours, DateTime>` and `impl Add<Minutes, DateTime>` differ in
// nothing else. If two collapsed to one Go type their impl signatures would be
// identical and stdPick would route `+ Minutes(3)` to whichever arrived first —
// a wrong answer off by 59 minutes, not a refusal.
//
// TestOpaqueRtTypesAreNominallyDistinct states this over `opaqueSpecs`; this
// states it over the registry rows and the bound Go signatures.
//
// The adapter spells its boundary in Int, String and structs, so each rung is
// an `impl Add<Minutes, DateTime>` Nomi body that destructures its period and
// calls a distinctly-named free function, `calendar.zoned_add_minutes`, bound
// to `func(rt.DateTime, int64) rt.DateTime`. No signature names its unit, so
// the hazard sits in the key: every rung key must reach a different Go
// function, and a copy-pasted row binding `zoned_add_minutes` to the hours
// symbol is an off-by-59-minutes wrong answer.
//
// The operand half is checked behaviourally, in std/calendar's own
// TestCalendarUnitRungsMoveTheirOwnComponent, which drives every rung against
// a unique expected delta. This test checks the two halves a signature can
// answer: the receiver the key names is the receiver the symbol takes, and no
// two rungs share a symbol.
func TestCalendarUnitsAreNominallyDistinctAtTheGoLevel(t *testing.T) {
	operand := map[string]reflect.Type{
		"Years":        reflect.TypeFor[rt.Years](),
		"Months":       reflect.TypeFor[rt.Months](),
		"Weeks":        reflect.TypeFor[rt.Weeks](),
		"Days":         reflect.TypeFor[rt.Days](),
		"Hours":        reflect.TypeFor[rt.Hours](),
		"Minutes":      reflect.TypeFor[rt.Minutes](),
		"Seconds":      reflect.TypeFor[rt.Seconds](),
		"Milliseconds": reflect.TypeFor[rt.Milliseconds](),
		"Microseconds": reflect.TypeFor[rt.Microseconds](),
		"Nanoseconds":  reflect.TypeFor[rt.Nanoseconds](),
	}
	// The receiver each key prefix names, which is the half of the signature
	// that carries information across an Int boundary.
	receiver := map[string]reflect.Type{
		"date":   reflect.TypeFor[stdcalendar.Date](),
		"time":   reflect.TypeFor[stdcalendar.Time](),
		"naive":  reflect.TypeFor[stdcalendar.NaiveDateTime](),
		"offset": reflect.TypeFor[stdcalendar.OffsetDateTime](),
		"zoned":  reflect.TypeFor[stdcalendar.DateTime](),
	}
	// The vacuity gate is not a total, so adding a rung needs no edit here: the
	// filter must match something, and every row it matched must survive the
	// shape check.
	symbolOf := map[string]string{}
	rows := 0
	shaped := 0
	for _, b := range stdlibbindings.Funcs() {
		local, isCalendarRung := strings.CutPrefix(b.Name, "calendar.")
		if !isCalendarRung || !strings.Contains(local, "_add_") {
			continue
		}
		rows++
		fn := reflect.TypeOf(b.Fn)
		if fn.NumIn() != 2 {
			t.Errorf("%s: rt symbol takes %d parameter(s), want 2", b.Name, fn.NumIn())
			continue
		}
		// The adapter's own boundary rule, asserted rather than assumed: a rung
		// that still declared `rt.Minutes` would not survive the FFI preflight,
		// so a row that reads that way means the registry and the facade have
		// come apart.
		if fn.In(1).Kind() != reflect.Int64 || fn.In(1) != reflect.TypeFor[int64]() {
			t.Errorf("%s: rt symbol's operand is %s, want a plain int64 — "+
				"the adapter boundary is spelled in Int", b.Name, fn.In(1))
		}
		prefix, _, _ := strings.Cut(local, "_add_")
		want, known := receiver[prefix]
		if !known {
			t.Errorf("%s: the key names receiver %q, which is not a calendar type this test knows; "+
				"add it here rather than leaving the row unverified", b.Name, prefix)
			continue
		}
		if fn.In(0) != want {
			t.Errorf("%s: rt symbol's receiver is %s, but the key names %s — "+
				"the registry row and the Go signature disagree about which type this rung adds to",
				b.Name, fn.In(0), want)
		}
		shaped++
		// One symbol per rung. Two keys reaching one function is invisible to
		// every other guard: both rows type-check, both register, and
		// `+ Minutes(1)` quietly adds an hour.
		symbol := reflect.ValueOf(b.Fn).Pointer()
		name := goruntime.FuncForPC(symbol).Name()
		if prior, clash := symbolOf[name]; clash {
			t.Errorf("%s and %s are both bound to %s, so one of the two rungs adds the wrong unit",
				prior, b.Name, name)
		}
		symbolOf[name] = b.Name
	}
	if rows == 0 {
		t.Fatal("no `calendar.*_add_*` binding matched, so every assertion below is vacuous; " +
			"the registry key spelling this filter reads must have changed")
	}
	if shaped != rows {
		t.Fatalf("%d of %d calendar Add rungs were dropped by the shape check, so they go unverified",
			rows-shaped, rows)
	}
	// And the ten units are ten types. Stated over the DECLARED set rather than
	// over what happened to be bound, because no rung's signature names a unit
	// and `Weeks` has no rt symbol at all.
	distinct := map[reflect.Type]bool{}
	for name, typ := range operand {
		if typ.Kind() != reflect.Int64 {
			t.Errorf("%s's operand %s is a Go %s, want int64 — every unit wraps Nomi `Int`",
				name, typ, typ.Kind())
		}
		distinct[typ] = true
	}
	if len(distinct) != len(operand) {
		t.Errorf("the %d civil units resolve to %d distinct Go type(s)", len(operand), len(distinct))
	}
}
