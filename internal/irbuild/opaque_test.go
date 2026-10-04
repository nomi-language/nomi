package irbuild

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestOpaqueRtTypesAreNominallyDistinct reflects over rt and asserts the
// representation's actual invariant, which is the one a lazy implementation
// loses: each spec is its own Go type, over int64, and NOT an alias.
//
// An alias (`type Duration = int64`) compiles and passes every differential
// comparison in this package, because every value flowing through the fixtures
// is produced and consumed by the same generated code. What it destroys is the
// nominal rule: `Duration` would be assignable to `Int` in every generated
// signature, so `Duration.as_nanos(5)` and `timer.sleep(3)` would type-check in
// Go, and the registry's signature projection would bind
// `rt.DurationToString(int64) string` to any `(Int) -> String` declaration.
func TestOpaqueRtTypesAreNominallyDistinct(t *testing.T) {
	seen := map[reflect.Type]string{}
	for i := range opaqueSpecs {
		s := &opaqueSpecs[i]
		if s.goType == nil {
			t.Fatalf("%s: no Go type", s.nomi)
		}
		// The UNDERLYING kind comes from the row's own `inner` rather than
		// being fixed at int64, because not every row is an Int newtype:
		// `std/toml.Toml` is `pub type Toml String`.
		// TestOpaqueGoWidthMatchesTheDeclaredInner owns the Nomi-to-Go width
		// agreement; what is asserted here is the NOMINAL half.
		underlying, named := goKindFor[s.inner.tag]
		if !named {
			t.Errorf("%s: inner %s is not a scalar this guard knows a Go kind for",
				s.nomi, s.inner.nomi())
			continue
		}
		if s.goType.Kind() != underlying {
			t.Errorf("%s: %s has kind %s, want %s — the spec declares inner %s",
				s.nomi, s.goType, s.goType.Kind(), underlying, s.inner.nomi())
		}
		if s.goType == goBuiltinFor[s.inner.tag] {
			t.Errorf("%s: %s IS the builtin %s, so it is an alias rather than a defined type; "+
				"Nomi's nominal rule would stop being Go's", s.nomi, s.goType, underlying)
		}
		if s.goType.PkgPath() != rtModulePath {
			t.Errorf("%s: %s lives in %q, not in rt — a generated package could not name it",
				s.nomi, s.goType, s.goType.PkgPath())
		}
		if prev, dup := seen[s.goType]; dup {
			t.Errorf("%s and %s share one Go type %s: two Nomi types, one Go type",
				prev, s.nomi, s.goType)
		}
		seen[s.goType] = s.nomi
	}
	// The pairing itself, spelled out: two SPECS must not collapse.
	if reflect.TypeFor[rt.Duration]() == reflect.TypeFor[rt.Instant]() {
		t.Fatal("rt.Duration and rt.Instant are one Go type")
	}
}

// TestOpaqueDistinctInstantsCompareUnequal is asserted at the representation
// level rather than through a fixture: two Instants built from different seconds must not be equal, and two
// built from the same seconds must be.
//
// It is a rt-level test on purpose. A golden record cannot distinguish a bug in
// rt from the expected output it recorded, and the fixtures above only compare Instants
// through `Instant.before?`, whose Nomi body is `a < b` on the unwrapped Ints —
// so an interning or handle-table representation that collapsed distinct values
// would show up here and nowhere else.
func TestOpaqueDistinctInstantsCompareUnequal(t *testing.T) {
	a, b := rt.Instant(10_000_000_000), rt.Instant(15_000_000_000)
	if a == b {
		t.Fatal("two Instants with different nanos compare equal")
	}
	if a != rt.Instant(10_000_000_000) {
		t.Fatal("two Instants with the same nanos compare unequal")
	}
	if rt.Duration(0) != rt.Duration(0) {
		t.Fatal("a Duration is not equal to itself")
	}
}

// TestOpaqueZeroValueIsARealValue pins the zero-value decision, which is the
// OPPOSITE of rt.Maybe's reserved-invalid tag and had to be argued rather than
// copied.
//
// int64 has no spare state — every value is a legal Duration, and
// `Duration.nanoseconds(0)` is one a program writes. So the Go zero value IS
// that value, and detecting it would mean widening the representation to carry
// a validity bit for a state no lowered program can produce. This asserts the
// consequence: a zero Duration renders as the real value it is, not as a
// sentinel.
func TestOpaqueZeroValueIsARealValue(t *testing.T) {
	var zero rt.Duration
	if got := rt.DurationToString(zero); got != "0s" {
		t.Fatalf("a zero Duration renders %q; it is Duration.nanoseconds(0) and must render 0s", got)
	}
	if zero != rt.Duration(0) {
		t.Fatal("a zero Duration is distinguishable from Duration.nanoseconds(0)")
	}
	var zeroInstant rt.Instant
	if zeroInstant != rt.Instant(0) {
		t.Fatal("a zero Instant is distinguishable from the Unix epoch")
	}
}

// TestOpaqueDefsArePackageNeutral guards the precondition that makes ONE
// process-wide *typeDef per spec sound, where foreign.go rejects shared defs in
// general.
//
// foreign.go's reason is that a shared def's components are interned in the
// OWNER's table, so they render correctly in one package and nowhere else. An
// opaque spec escapes that only because it has no such component: its inner is a
// scalar and its Go name is resolved through rt. A spec added over a struct
// inner would silently reintroduce exactly the bug foreign.go describes, so the
// precondition is asserted rather than commented.
func TestOpaqueDefsArePackageNeutral(t *testing.T) {
	for i, d := range opaqueDefs() {
		spec := &opaqueSpecs[i]
		if !d.rtDeclared {
			t.Errorf("%s: not marked rtDeclared, so typeDecl would emit a second declaration", spec.nomi)
		}
		if !d.isDistinct || d.isEnum {
			t.Errorf("%s: not a distinct def", spec.nomi)
		}
		if d.inner.def != nil || d.inner.comp != nil {
			t.Errorf("%s: inner %s carries a declaration, so its rendering is package-relative",
				spec.nomi, d.inner.nomi())
		}
		if len(d.components) > 0 || len(d.mentions) > 0 || len(d.fields) > 0 || len(d.variants) > 0 {
			t.Errorf("%s: has components/mentions/fields/variants; a shared def must have none", spec.nomi)
		}
		if len(d.refusals) > 0 || !d.lowerable {
			t.Errorf("%s: not lowerable", spec.nomi)
		}
	}
}

// TestOpaqueSpecsMatchStdSource asserts the anchors are really built from the
// std source in this build.
//
// Without it, every guard in this file is vacuous in the failing direction: a
// spec whose shape check does not match std produces NO anchor, every mention
// refuses, and the coverage number quietly drops while nothing fails. Same
// reason TestPreludeShapeMatchesStdSource exists.
func TestOpaqueSpecsMatchStdSource(t *testing.T) {
	lib := std.Load()
	for i := range opaqueSpecs {
		s := &opaqueSpecs[i]
		module := strings.TrimPrefix(s.origin, "std/")
		fa := lib.Files[module]
		if fa == nil {
			t.Fatalf("%s: std has no module %q", s.nomi, module)
		}
		_, byName := opaqueAnchors(fa)
		if _, anchored := byName[s.nomi]; !anchored {
			t.Fatalf("%s: no anchor built from %s — the declaration's shape does not match the spec",
				s.nomi, s.origin)
		}
	}
}

// TestOpaqueDebugRenderingIsSynthesized pins that the `<opaque T>` rendering is produced in the ANALYZER, as
// ordinary Nomi source, so the builder has nothing to reproduce.
//
// If that synthesis ever moves into the builder this fails, rather than the
// rendering silently reverting to whatever the impl path happens to do.
func TestOpaqueDebugRenderingIsSynthesized(t *testing.T) {
	p, err := AnalyzeSource("main", "pub opaque type Meters Int\n")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(p.Modules) == 0 {
		t.Fatal("no modules")
	}
	found := false
	for _, n := range p.Modules[0].Nodes {
		impl, isImpl := n.(*ast.ImplBlock)
		if !isImpl || simpleTypeName(impl.Interface) != "Debug" || simpleTypeName(impl.Receiver) != "Meters" {
			continue
		}
		for _, item := range impl.Items {
			fd, isFn := item.(*ast.FuncDef)
			if !isFn || fd.Name != "inspect" || fd.Body == nil {
				continue
			}
			if strings.Contains(renderNode(fd.Body), "<opaque Meters>") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("no synthesized `impl Debug for Meters` with an `<opaque Meters>` body reached the builder; " +
			"declModifiers lifted the `opaque type` refusal on the grounds that the analyzer produces this")
	}
}

// TestOpaqueHostFnFrameIsReadOffTheSignature pins the one binding that takes a
// frame, and both directions of the rule.
//
// `timer.sleep` waits on the frame's cancellation context, so it needs one; every
// other binding is a function of its arguments alone and must not be handed one.
// The flag is derived from the Go signature rather than declared, so the table
// cannot claim a frame the implementation does not take.
func TestOpaqueHostFnFrameIsReadOffTheSignature(t *testing.T) {
	sleep, err := hostFnFor(rt.TimerSleep)
	if err != "" {
		t.Fatalf("rt.TimerSleep did not project: %s", err)
	}
	if !sleep.takesFrame {
		t.Error("rt.TimerSleep takes a *rt.Frame and takesFrame is false")
	}
	if len(sleep.params) != 1 || sleep.params[0] != opaqueKind(0) || sleep.result != kindUnit {
		t.Errorf("rt.TimerSleep projected as %d param(s) result %s; want (Duration) -> Unit",
			len(sleep.params), sleep.result.nomi())
	}
	fmt, err := hostFnFor(rt.DurationToString)
	if err != "" {
		t.Fatalf("rt.DurationToString did not project: %s", err)
	}
	if fmt.takesFrame {
		t.Error("rt.DurationToString takes no frame and takesFrame is true")
	}
	if len(fmt.params) != 1 || fmt.params[0] != opaqueKind(0) || fmt.result != kindString {
		t.Errorf("rt.DurationToString projected wrongly: %d param(s) result %s",
			len(fmt.params), fmt.result.nomi())
	}
}

// TestOpaqueGoTypeDoesNotProjectAsInt is the drift guard the design turns on: an
// rt extern over rt.Duration must NOT be interchangeable with one over int64.
//
// The rejected alternative was typing these externs over plain int64 and
// inserting a Go conversion at the call. That loses the registry's central
// property — a signature nobody typed cannot drift — because
// `rt.DurationToString(int64) string` would then match, and silently bind to,
// any `(Int) -> String` declaration in std.
func TestOpaqueGoTypeDoesNotProjectAsInt(t *testing.T) {
	if got := kindOfGoType(reflect.TypeFor[rt.Duration]()); got == kindInt {
		t.Fatal("rt.Duration projects onto Int, so a Duration extern would bind to an Int declaration")
	}
	if got := kindOfGoType(reflect.TypeFor[rt.Duration]()); got != opaqueKind(0) {
		t.Fatalf("rt.Duration projects onto %s, want the Duration kind", got.nomi())
	}
	if got := kindOfGoType(reflect.TypeFor[int64]()); got == opaqueKind(0) {
		t.Fatal("int64 projects onto the Duration kind")
	}
}

// TestOpaqueStdlibIndexResolvesTheOpaqueSurface asserts the index really holds
// the entries the call sites resolve through, so a regression that stops
// anchoring shows up as a named failure rather than as a coverage number.
func TestOpaqueStdlibIndexResolvesTheOpaqueSurface(t *testing.T) {
	if testing.Short() {
		t.Skip("loads std; -short")
	}
	idx := stdlibLowering()
	lowerable := []string{
		"Duration.nanoseconds", "Duration.microseconds", "Duration.milliseconds",
		"Duration.seconds", "Duration.minutes", "Duration.hours",
		"Duration.as_nanos", "Duration.as_micros", "Duration.as_millis",
		"Duration.as_seconds", "Duration.as_minutes", "Duration.as_hours",
		"Duration.to_string", "Duration.add", "Duration.subtract",
		"Duration.multiply", "Duration.divide", "Duration.equal?",
		"Instant.now", "Instant.from_seconds", "Instant.to_seconds", "Instant.before?",
	}
	for _, name := range lowerable {
		f := stdSole(idx.byType[name])
		if f == nil {
			t.Errorf("%s: not in the stdlib index at all", name)
			continue
		}
		if !f.lowerable() {
			t.Errorf("%s: refused as %q", name, f.why)
		}
	}
	// The file-qualified half.
	if f := idx.byFile["timer.sleep"]; f == nil || !f.lowerable() {
		t.Errorf("timer.sleep is not resolvable as a file-qualified call: %v", f)
	}
	// A module-private helper IS reachable through the file API object, and that
	// is the point rather than a leak. `byFile` keys carry the module, so
	// `strings.string_compare` is a spelling only std/strings itself can write —
	// the front end rejects it everywhere else before this index is consulted —
	// and a module naming its own private free functions is exactly what the
	// implicit file alias is for.
	//
	// A `pub` gate here would make every module-private `go`-bound extern
	// unreachable however carefully it is bound: std/random's `below_state`,
	// `unit_float_state` and `os_state` and most of std/calendar's boundary are
	// of that shape.
	if f := idx.byFile["strings.string_compare"]; f == nil {
		t.Errorf("the private `fn string_compare` is not reachable as " +
			"strings.string_compare, so a std module cannot call its own private " +
			"free functions through the implicit file alias")
	}
}

// goKindFor and goBuiltinFor are the scalar-inner tables the two width guards
// share, so a future row over a Float or a Bool inner is covered by both
// without an edit in either.
//
// One table rather than a copy per guard: `TestOpaqueGoWidthMatchesTheDeclaredInner`
// exists because the same fact lived in two places with nothing making them
// agree, and a second copy of ITS table would be the same mistake one level up.
var goKindFor = map[tag]reflect.Kind{
	tagInt:    reflect.Int64,
	tagFloat:  reflect.Float64,
	tagString: reflect.String,
	tagBool:   reflect.Bool,
}

var goBuiltinFor = map[tag]reflect.Type{
	tagInt:    reflect.TypeFor[int64](),
	tagFloat:  reflect.TypeFor[float64](),
	tagString: reflect.TypeFor[string](),
	tagBool:   reflect.TypeFor[bool](),
}
