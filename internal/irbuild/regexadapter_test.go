package irbuild

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/stdregex"
)

// std/regex's Go support has two surfaces in one package, `nomi/stdregex`:
// an FFI-shaped adapter (`FFICompile`, `FFIFind`, ...) whose signatures use
// `(T, error)`, `*T`, `[]T` and a `*stdregex.Regex` handle, and an rt-shaped
// shim that delegates to it, so there is one implementation with two
// marshalling surfaces. The builder's host registry binds only rt-shaped
// signatures, which is why the shim is not optional. The tests below hold
// both halves: the FFI-shaped signatures and Go types are refused, the
// rt-shaped spellings of the same Nomi types project, and every std/regex
// declaration lowers through the shim (adapterhandle_test.go is the positive
// side).
//
// The handle's Go type is `rt.Regex`, because a value is stored in modules
// that never call the adapter and rt is what every gen can name
// (rt/regexhandle.go). `pub opaque type Regex go go_regex.Regex` parses to an
// `*ast.ExternType`, so it is an stdHostSpecs row, as `Context`, `Dynamic` and
// `Supervisor` are, and not an opaqueSpecs row.

// TestAdapterWall_AdapterSignaturesNeedShims holds what the adapter's own Go
// signatures do in the registry, and that the shims for them bind.
//
// EXPIRES WHEN: stdlib.go's hostFnFor or kindOfGoType admits these Go shapes
// directly.
//
// The adapter is written for the FFI projection, which maps `(T, error)` onto
// `Result<T, String>`, `*T` onto `Maybe<T>` and `[]T` onto `List<T>`. The
// builder's registry does none of that, so none of the six can be bound as it
// stands. The package resolves, because the shim beside it is registered, so
// each refusal is about the SHAPE: a Go pointer to an adapter struct has no
// kind.
//
// `Compile` reports its arity first, because the arity check precedes the
// parameter walk. This order is hostFnFor's own; a program reaches these
// declarations through the stdlib index, which checks the signature first.
func TestAdapterWall_AdapterSignaturesNeedShims(t *testing.T) {
	// Each row is a REAL symbol from nomi/stdregex's FFI-shaped surface, held
	// as a func value so a rename is a Go compile error here rather than a
	// stale string. That is the same trade stdlibBinding makes.
	for _, tc := range []struct {
		name string
		fn   any
		want string
	}{
		// `(T, error)` — two returns, and the arity check precedes the package
		// lookup, so this one reports its shape.
		{"Compile", stdregex.FFICompile, "returns 2 values"},
		// The other five are one-return, so the parameter walk runs and stops
		// on the handle RECEIVER. Their result types (`*string`, `[]string`)
		// are behind it and never reached — which is why the shim needs a
		// twin for each of them and not only for the two with list results.
		{"Find", stdregex.FFIFind, "parameter 1 is *stdregex.Regex, which is outside the representable set"},
		{"FindAll", stdregex.FFIFindAll, "parameter 1 is *stdregex.Regex, which is outside the representable set"},
		{"Split", stdregex.FFISplit, "parameter 1 is *stdregex.Regex, which is outside the representable set"},
		{"Pattern", stdregex.FFIPattern, "parameter 1 is *stdregex.Regex, which is outside the representable set"},
		{"Match", stdregex.FFIMatch, "parameter 1 is *stdregex.Regex, which is outside the representable set"},
	} {
		_, why := hostFnFor(tc.fn)
		if why == "" {
			t.Errorf("hostFnFor(regex.%s) accepted it. If this binds, check "+
				"whether the lowered call and internal/stdlibbindings' row agree", tc.name)
			continue
		}
		if !strings.Contains(why, tc.want) {
			t.Errorf("hostFnFor(regex.%s) refused with %q, want a reason "+
				"containing %q; the refusal moved and this row's diagnosis is "+
				"stale", tc.name, why, tc.want)
		}
	}
	// The PAIRED POSITIVE: an rt-shaped signature over the same value types IS
	// accepted, so the rows above are refused for their SHAPE and their PACKAGE
	// and not because hostFnFor refuses everything in a test binary.
	if _, why := hostFnFor(rt.StringContainedIn); why != "" {
		t.Fatalf("hostFnFor(rt.StringContainedIn) refused with %q; this test has "+
			"no power and its negatives above mean nothing", why)
	}
	// Every one of those six declarations is bound through the shim. Read off
	// the index rather than off the registry, so this asks the question a
	// program asks.
	idx := stdlibLowering()
	for _, key := range []string{
		"regex.Regex.compile", "regex.Regex.find", "regex.Regex.find_all_in",
		"regex.Regex.split_in", "regex.Regex.pattern", "regex.Regex.contained_in?",
	} {
		f, known := idx.byKey[key]
		if !known || !f.lowerable() {
			t.Fatalf("%s does not lower, so the shims do not "+
				"bind and std/regex does not lower", key)
		}
	}
}

// TestAdapterWall_AdapterGoTypesAreNotRepresentable is the KIND-level half,
// read where hostFnFor's ordering cannot mask it.
//
// EXPIRES WHEN: stdlib.go's kindOfGoType admits a Go handle pointer, a `*T`, or
// a `[]T`.
//
// This is the half that requires a shim per extern, and it is independent of
// whether the adapter's package is a registered host package.
func TestAdapterWall_AdapterGoTypesAreNotRepresentable(t *testing.T) {
	for _, tc := range []struct {
		what string
		typ  reflect.Type
	}{
		{"the handle itself (*regex.Regex)", reflect.TypeFor[*stdregex.Regex]()},
		{"Find's result (*string), the FFI spelling of Maybe<String>", reflect.TypeFor[*string]()},
		{"FindAll's result ([]string), the FFI spelling of List<String>", reflect.TypeFor[[]string]()},
	} {
		if k := kindOfGoType(tc.typ); k != kindInvalid {
			t.Errorf("kindOfGoType(%s) = %s for %s, want kindInvalid. If this "+
				"answers, the shim per extern is not needed", tc.typ, k.nomi(), tc.what)
		}
	}
	// The PAIRED POSITIVES: the rt spellings of the SAME two Nomi types DO
	// project, so the refusals above are about the Go shape rather than about
	// Maybe and List being unrepresentable. This is what makes "the adapter
	// needs shims" the right conclusion instead of "Maybe cannot be returned".
	for _, tc := range []struct {
		what string
		typ  reflect.Type
	}{
		{"rt.Maybe[string]", reflect.TypeFor[rt.Maybe[string]]()},
		{"*rt.List[string]", reflect.TypeFor[*rt.List[string]]()},
	} {
		if k := kindOfGoType(tc.typ); k == kindInvalid {
			t.Fatalf("kindOfGoType(%s) is invalid, so this test's negatives carry "+
				"no information: the shape is not the discriminator it claims",
				tc.what)
		}
	}
	// The SHIM's signatures are rt-shaped, so they project.
	for _, tc := range []struct {
		what string
		typ  reflect.Type
	}{
		{"the shim's handle (rt.Regex)", reflect.TypeFor[rt.Regex]()},
		{"the shim's Find result", reflect.TypeFor[rt.Maybe[string]]()},
		{"the shim's FindAll result", reflect.TypeFor[*rt.List[string]]()},
	} {
		if kindOfGoType(tc.typ) == kindInvalid {
			t.Fatalf("%s does not project, so the shim route is broken", tc.what)
		}
	}
}

// TestAdapterWall_HandleHasNoInnerScalar is a TABLE INVARIANT: opaqueSpecs
// holds only scalar-inner rows, and handles live in stdHostSpecs.
//
// EXPIRES WHEN: opaque.go grows a declForm for a Go-backed handle, or its
// `inner` stops being required to be a scalar.
//
// The parser gives `type X go pkg.Y` an `*ast.ExternType`, which is
// stdHostSpecs', and the analyzer gives it a `*PrimitiveType`, so a handle
// never needs an opaqueSpecs row. opaqueSpecs' package-neutrality argument
// rests on every row having a SCALAR inner.
func TestAdapterWall_HandleHasNoInnerScalar(t *testing.T) {
	// The FIRING: no spec row anywhere claims a Go handle, so nothing in
	// opaqueSpecs can be the representation of one.
	for i := range opaqueSpecs {
		s := &opaqueSpecs[i]
		if s.form != formOpaque && s.form != formDistinct {
			t.Fatalf("opaqueSpecs[%d] (%s.%s) has form %d, which is neither of "+
				"the two scalar clauses. If a handle form is added here, "+
				"this invariant does not apply to it", i, s.origin, s.nomi, s.form)
		}
		switch s.inner {
		case kindInt, kindFloat, kindString, kindBool, kindUnit:
		default:
			t.Fatalf("opaqueSpecs[%d] (%s.%s) has inner %s, which is not a "+
				"scalar, so the scalar-inner restriction does not hold",
				i, s.origin, s.nomi, s.inner.nomi())
		}
	}
	// The PAIRED POSITIVE: the table is non-empty, so the loop above is not
	// passing because there is nothing to check.
	if len(opaqueSpecs) == 0 {
		t.Fatal("opaqueSpecs is empty, so this test asserts nothing")
	}
	// The handle is represented in the OTHER table, with no inner at all.
	handles := 0
	for i := range stdHostSpecs {
		if stdHostSpecs[i].handle {
			handles++
			if d := stdHostDefs()[i]; d.inner != kindInvalid || !d.rtOpaque {
				t.Errorf("the handle row %s.%s has inner=%s rtOpaque=%v; a handle is "+
					"a LEAF with contents and both of those decide whether `a == b` "+
					"answers or refuses", stdHostSpecs[i].origin, stdHostSpecs[i].nomi,
					d.inner.nomi(), d.rtOpaque)
			}
		}
	}
	// One: std/regex's Regex.
	if handles != 1 {
		t.Fatalf("stdHostSpecs has %d handle rows, want 1 (regex.Regex). Another one is "+
			"fine — update this count and say what it is", handles)
	}
}
