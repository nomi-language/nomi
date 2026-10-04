package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// The guards over stdlib record-struct FIELD DEFAULTS. Each catches a mutation
// the other does not:
//
//  1. Make the string default's `shape` ignore the VALUE (accept any
//     StringLit). -> ShapeCheckRejectsADriftedDefault/string_default_changed.
//     The pinned text passes, because a vacuous shape check still produces the
//     right answer for std as it is.
//
//  2. Change std/assertions' declared default AND the spec's together, so they
//     still agree. -> PinnedText. The drift table passes because the spec
//     matches the declaration it was drifted to match.
//
// So the drift table guards the spec against std, and the pinned text guards
// the answer itself against both moving at once.

// TestStdStructDefaults_PinnedText spells the transcript out absolutely.
//
// Every row is an answer a plausible wrong lowering gets wrong, and only one of
// the three defaults is Go's zero value:
//
//   - `reason` defaults to a non-empty string, so a dropped default reads "".
//   - `actual` defaults to None, whose Tag is TagNone and NOT 0. A dropped
//     default leaves a zero Maybe, which is neither Some nor None and matches
//     no arm of `shown`.
//   - `details` defaults to `[]`, and an empty *List IS nil, so a dropped
//     default here is INVISIBLE. It is pinned anyway: that is what makes the
//     other two rows readable as "defaults are applied" rather than "this field
//     happened to be zero".
//
// It runs the fixture on the VM and pins the ANSWER, which is what catches std
// and the spec drifting together (mutation 2 in the header) and what notices
// the fixture going silent.
func TestStdStructDefaults_PinnedText(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a fixture")
	}
	path := fixture("stdstruct_defaults.nomi")
	want := strings.Join([]string{
		"all.reason = assertion failed",
		"all.actual = None",
		"all.details = []",
		"only_reason.reason = values were not equal",
		"only_reason.actual = None",
		"only_reason.details = []",
		"with_details.reason = values were not equal",
		"with_details.actual = None",
		"with_details.details = actual+more",
		"explicit.reason = explicit",
		"explicit.actual = Some(41)",
		"explicit.details = []",
		"",
	}, "\n")
	if got := vmReference(path); got.stdout != want || got.exit != 0 {
		t.Fatalf("reference output is not what this fixture pins: %s\nwant:\n%s", got, want)
	}
}

// TestStdStructDefaults_ShapeCheckRejectsADriftedDefault is the half an output
// comparison cannot see.
//
// The existing drift table covers a DECLARED default with no spec row for it
// (`field default` on the DateTime row). These are the reverse and the
// mismatched cases, which only exist now that a row may legitimately carry one:
// a spec default the declaration stopped carrying would invent a value std never
// wrote, and a spec default whose VALUE no longer matches std's would emit a
// different one at every construction site that omits the field. Neither is a Go
// compile error.
//
// The declaration is std's real one, mutated. Hand-building it would let a case
// pass by mis-modelling the node, and `AssertionDetails` cannot be analysed from
// a synthetic module the way `DateTime` can -- `details: List<AssertionDetail>`
// has to resolve to the SPEC's def, and in a synthetic module the same spelling
// is a user struct and would not match even for the control.
func TestStdStructDefaults_ShapeCheckRejectsADriftedDefault(t *testing.T) {
	lib := std.Load()
	fa := lib.Files["assertions"]
	if fa == nil {
		t.Fatal("no std/assertions FileAnalysis")
	}
	var spec *stdStructSpec
	for i := range stdStructSpecs {
		if stdStructSpecs[i].nomi == "AssertionDetails" {
			spec = &stdStructSpecs[i]
		}
	}
	if spec == nil {
		t.Fatal("no AssertionDetails spec; this test is about that row")
	}
	decl := stdStructDeclIn(fa, spec)
	if decl == nil {
		t.Fatal("std/assertions does not declare AssertionDetails")
	}
	anchors := stdAnchorsOf(fa)

	// The control first, so a `matches` that rejected everything would fail
	// here rather than making every case below pass vacuously.
	if !spec.matches(decl, anchors) {
		t.Fatal("the spec's own declaration does not anchor, so every case below would pass vacuously")
	}

	cases := []struct {
		name   string
		mutate func([]ast.StructField)
	}{{
		// std dropped the default. The field becomes required, and a spec that
		// still supplied one would fill an omitted field with a value the
		// declaration no longer gives it.
		"string default removed",
		func(f []ast.StructField) { f[0].Default = nil },
	}, {
		// std changed the default's VALUE. Same type, different answer, and
		// nothing about the layout is wrong -- this is the case a type check
		// cannot see.
		"string default changed",
		func(f []ast.StructField) { f[0].Default = &ast.StringLit{Value: "boom"} },
	}, {
		"None default removed",
		func(f []ast.StructField) { f[1].Default = nil },
	}, {
		// `Some` where the spec implements `None`. Both are Maybe<String>, so
		// the field's KIND still matches and only the default differs.
		"None default became Some",
		func(f []ast.StructField) { f[1].Default = &ast.TypeIdent{Name: "Some"} },
	}, {
		"empty-list default removed",
		func(f []ast.StructField) { f[2].Default = nil },
	}, {
		// A non-empty default list. The spec's value is the empty one, so this
		// would silently drop elements std declares.
		"empty-list default became non-empty",
		func(f []ast.StructField) {
			f[2].Default = &ast.ListLit{Items: []ast.Node{&ast.StringLit{Value: "x"}}}
		},
	}, {
		// A default of an unrelated NODE KIND. The shape matcher must answer on
		// the node it actually got rather than assuming the family.
		"string default became a list",
		func(f []ast.StructField) { f[0].Default = &ast.ListLit{} },
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			drifted := *decl
			drifted.Fields = append([]ast.StructField(nil), decl.Fields...)
			c.mutate(drifted.Fields)
			if spec.matches(&drifted, anchors) {
				t.Fatalf("the drifted declaration still anchors; the builder would fill an "+
					"omitted field with a value std does not declare (case %q)", c.name)
			}
		})
	}
}
