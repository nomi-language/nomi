package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// THE SPREAD PATCHES DEEPLY, so `{..base, field: value}` and
// `Struct.update(base, {field: value})` are the same operation.
//
// `Struct.update` remains the form that takes a COMPUTED patch — a variable
// holding one, which the spread's syntax cannot express — and the form
// reachable by name.
//
// # THE TWO RECURSIONS, AND WHY THEY ARE TWO
//
// `Partial<T>`'s admission walk is `deepPartialMatches` (analysis/partial.go).
// It is NOT what the spread calls, and that is a choice rather than a
// duplication. It takes TYPES and returns a bool; its own header says a nested
// field "has no node of its own here (only types reach this walk)". So it
// cannot name the nested type in a diagnostic or point at the offending nested
// field, which is the whole of what a user sees. The spread has an
// `*ast.StructLit` per field, so its recursion is the AST-level validator
// `checkStructLitAgainstStruct` calling itself with `patch` set.
//
// What the two share is the field-shape resolution — `partialTargetFields`,
// which substitutes generic arguments and resolves a thin name-only
// `StructType` through the registry — and the opacity guard. What they cannot
// share is the verdict-reporting, so TestSpreadDeep_AgreesWithStructUpdate is
// the pin that keeps them from drifting. It is the assertion that stands in
// for a shared function.

const spreadDeepDecls = `struct Inner {
  a: Int
  b: Int
}
struct Outer {
  inner: Inner
  k: Int
}
struct AnonOuter {
  rec: {a: Int, b: Int}
  k: Int
}
struct L3 {
  v: Int
  w: Int
}
struct L2 {
  l3: L3
  m: Int
}
struct L1 {
  l2: L2
  n: Int
}
interface Speaker {
  fn say(s: self): String
}
struct Loud {
  n: Int
}
impl Speaker for Loud {
  fn say(_s: Loud): String {
    "loud"
  }
}
struct Stage {
  who: Speaker
  seats: Int
}
struct Box<T> {
  item: T
  tag: String
}
`

// spreadDeepPatches are patch shapes written once and checked in BOTH
// spellings. `head` is the receiver expression's binding, `patch` is the
// literal's field list without its braces.
//
// The table is the agreement contract: for every row, the spread and
// `Struct.update` must reach the same verdict. A row that is legal in one
// spelling and refused in the other is the drift this file exists to catch.
var spreadDeepPatches = []struct {
	name  string
	decl  string
	head  string
	patch string
	ok    bool
}{
	// --- the deep case, in every shape that reaches it ---
	{
		name:  "a bare brace at a nominal struct field",
		decl:  "  o = Outer{inner: Inner{a: 1, b: 2}, k: 3}\n",
		head:  "o",
		patch: "inner: {a: 9}",
		ok:    true,
	},
	{
		name:  "a bare brace at an anonymous-typed field",
		decl:  "  o = AnonOuter{rec: {a: 1, b: 2}, k: 3}\n",
		head:  "o",
		patch: "rec: {a: 9}",
		ok:    true,
	},
	{
		// A COMPLETE brace at an anonymous-typed field is both a complete
		// VALUE and a complete PATCH. It takes the patch route, and
		// the two routes have to agree or the strict-extension claim is
		// false for this one shape: the value, its `Debug.inspect`
		// rendering and its type are the same by either route.
		name:  "a COMPLETE brace at an anonymous-typed field",
		decl:  "  o = AnonOuter{rec: {a: 1, b: 2}, k: 3}\n",
		head:  "o",
		patch: "rec: {a: 9, b: 8}",
		ok:    true,
	},
	{
		name:  "three levels deep",
		decl:  "  o = L1{l2: L2{l3: L3{v: 1, w: 2}, m: 3}, n: 4}\n",
		head:  "o",
		patch: "l2: {l3: {v: 9}}",
		ok:    true,
	},
	{
		name:  "a deep patch beside a shallow one",
		decl:  "  o = Outer{inner: Inner{a: 1, b: 2}, k: 3}\n",
		head:  "o",
		patch: "inner: {a: 9}, k: 4",
		ok:    true,
	},
	{
		// A SOLVED type parameter over a struct. `partialTargetFields`
		// substitutes `T := Inner` before the position is judged, so the
		// brace reaches a real field set. This is the generic half of the
		// question and it comes out YES.
		name:  "a bare brace at a field generic over a struct, solved",
		decl:  "  b: Box<Inner> = Box{item: Inner{a: 1, b: 2}, tag: \"t\"}\n",
		head:  "b",
		patch: "item: {a: 9}",
		ok:    true,
	},

	// --- values, unchanged: each is a position the patch rule must not take ---
	{
		name:  "a nominal literal replaces wholesale",
		decl:  "  o = Outer{inner: Inner{a: 1, b: 2}, k: 3}\n",
		head:  "o",
		patch: "inner: Inner{a: 9, b: 8}",
		ok:    true,
	},
	{
		name:  "a binding is a value",
		decl:  "  o = Outer{inner: Inner{a: 1, b: 2}, k: 3}\n  x = Inner{a: 9, b: 8}\n",
		head:  "o",
		patch: "inner: x",
		ok:    true,
	},
	{
		name:  "a nested spread is a value",
		decl:  "  o = Outer{inner: Inner{a: 1, b: 2}, k: 3}\n",
		head:  "o",
		patch: "inner: {..o.inner, a: 9}",
		ok:    true,
	},
	{
		// AN INTERFACE-TYPED FIELD TAKES VALUES ONLY. `Partial<T>` stops
		// at an interface because an interface does not name its fields,
		// and the spread stops in the same place.
		name:  "an interface-typed field admits an implementer",
		decl:  "  s = Stage{who: Loud{n: 1}, seats: 3}\n",
		head:  "s",
		patch: "who: Loud{n: 2}",
		ok:    true,
	},
	{
		name:  "an interface-typed field REFUSES a brace",
		decl:  "  s = Stage{who: Loud{n: 1}, seats: 3}\n",
		head:  "s",
		patch: "who: {n: 2}",
		ok:    false,
	},

	// --- refusals the depth must not swallow ---
	{
		name:  "an unknown field one level down",
		decl:  "  o = Outer{inner: Inner{a: 1, b: 2}, k: 3}\n",
		head:  "o",
		patch: "inner: {z: 1}",
		ok:    false,
	},
	{
		name:  "a wrong value type one level down",
		decl:  "  o = Outer{inner: Inner{a: 1, b: 2}, k: 3}\n",
		head:  "o",
		patch: "inner: {a: \"s\"}",
		ok:    false,
	},
	{
		name:  "an unknown field three levels down",
		decl:  "  o = L1{l2: L2{l3: L3{v: 1, w: 2}, m: 3}, n: 4}\n",
		head:  "o",
		patch: "l2: {l3: {zz: 1}}",
		ok:    false,
	},
	{
		name:  "a brace at a field that is not struct-shaped",
		decl:  "  o = Outer{inner: Inner{a: 1, b: 2}, k: 3}\n",
		head:  "o",
		patch: "k: {a: 1}",
		ok:    false,
	},
}

// TestSpreadDeep_AgreesWithStructUpdate is the shared-rule pin. Every row is
// checked in both spellings and the verdicts must match, because the two forms
// are one operation and their recursions are two functions.
//
// It fails in both directions by construction: the table carries accepting and
// refusing rows, so a change that made the spread accept everything, or refuse
// everything, breaks it.
func TestSpreadDeep_AgreesWithStructUpdate(t *testing.T) {
	for _, tc := range spreadDeepPatches {
		t.Run(tc.name, func(t *testing.T) {
			spread := spreadDeepDecls + "fn main() {\n" + tc.decl +
				"  _a = {.." + tc.head + ", " + tc.patch + "}\n}\n"
			update := spreadDeepDecls + "fn main() {\n" + tc.decl +
				"  _a = Struct.update(" + tc.head + ", {" + tc.patch + "})\n}\n"

			_, spreadErrs := checkSourceWithStdlib(spread)
			_, updateErrs := checkSourceWithStdlib(update)

			spreadOK := len(spreadErrs) == 0
			updateOK := len(updateErrs) == 0
			if spreadOK != updateOK {
				t.Fatalf("the two spellings disagree: spread accepted=%v %v; Struct.update accepted=%v %v",
					spreadOK, spreadErrs, updateOK, updateErrs)
			}
			if spreadOK != tc.ok {
				t.Fatalf("want accepted=%v, got accepted=%v; spread %v; update %v",
					tc.ok, spreadOK, spreadErrs, updateErrs)
			}
		})
	}
}

// --- the two diagnostics, which are what a user actually reads -------------

// An unknown NESTED field must name the NESTED type. Before the depth existed
// this read `field 'inner' of Outer: expected Inner, got {z: Int}` — a message
// about the outer field, in which nothing says `z` is the problem.
//
// The text is asserted exactly, because "some diagnostic fired" is satisfied
// by the old message too.
func TestSpreadDeep_UnknownNestedFieldNamesTheNestedType(t *testing.T) {
	const src = spreadDeepDecls + `fn main() {
  o = Outer{inner: Inner{a: 1, b: 2}, k: 3}
  _a = {..o, inner: {z: 1}}
}
`
	_, errs := checkSourceWithStdlib(src)
	expectExactlyOne(t, errs, "Inner has no field 'z'")
}

// A wrong nested VALUE type must name the nested FIELD. Before the depth this
// read `field 'inner' of Outer: expected Inner, got {a: String}`, which names
// `Outer.inner` and not `Inner.a`.
func TestSpreadDeep_WrongNestedValueNamesTheNestedField(t *testing.T) {
	const src = spreadDeepDecls + `fn main() {
  o = Outer{inner: Inner{a: 1, b: 2}, k: 3}
  _a = {..o, inner: {a: "s"}}
}
`
	_, errs := checkSourceWithStdlib(src)
	expectExactlyOne(t, errs, "field 'a' of Inner: expected Int, got String")
}

// Three levels down, so the message is not merely "one level better" — it
// names the type at the depth the mistake is at.
func TestSpreadDeep_DiagnosticsCarryToDepth(t *testing.T) {
	const src = spreadDeepDecls + `fn main() {
  o = L1{l2: L2{l3: L3{v: 1, w: 2}, m: 3}, n: 4}
  _a = {..o, l2: {l3: {v: "s"}}}
}
`
	_, errs := checkSourceWithStdlib(src)
	expectExactlyOne(t, errs, "field 'v' of L3: expected Int, got String")
}

// expectExactlyOne asserts the message fired AND that it is the only
// diagnostic. The count matters here: a nested arm that reported the nested
// problem and then fell through to the outer type mismatch would satisfy a
// containment check and hand the user two errors for one mistake.
func expectExactlyOne(t *testing.T, errs []analysis.TypeError, want string) {
	t.Helper()
	if len(errs) != 1 || !strings.Contains(errs[0].Message, want) {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Message
		}
		t.Fatalf("want exactly one diagnostic containing %q, got %d:\n  %s",
			want, len(errs), strings.Join(msgs, "\n  "))
	}
}

// --- the leak guard: depth exists only underneath a spread -----------------

// `Outer{inner: {a: 9}}` HAS NO BASE for `inner.b` to come from, so it is not
// a patch. Outside a spread a bare brace at a struct field builds that struct
// (target typing), so a partial one is that struct's missing-field error.
// This is the test that separates "the spread patches deeply" from "a bare
// brace at a struct field is always a patch", and the second would be unsound.
//
// Three spellings of construction, because the validator is shared by all
// three and the `patch` parameter is what keeps them apart.
func TestSpreadDeep_OrdinaryConstructionIsNotAPatch(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"the literal-attach form", "  _a = Outer{inner: {a: 9}, k: 3}\n", "missing field 'b' of Inner"},
		{"the record call form", "  _a = Outer({inner: {a: 9}, k: 3})\n", "missing field 'b' of Inner"},
		{"a nested construction", "  _a = L1{l2: {l3: {v: 9, w: 1}}, n: 1}\n", "missing field 'm' of L2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(spreadDeepDecls + "fn main() {\n" + tc.body + "}\n")
			expectExactlyOne(t, errs, tc.want)
		})
	}
}

// And the positive behind that refusal: a COMPLETE brace in ordinary
// construction builds the nominal field's struct. Without this row the test
// above would pass for a build that had simply broken anonymous literals at
// struct fields.
func TestSpreadDeep_OrdinaryConstructionBuildsACompleteBrace(t *testing.T) {
	const src = spreadDeepDecls + `fn main() {
  _a = Outer{inner: {a: 9, b: 8}, k: 3}
}
`
	_, errs := checkSourceWithStdlib(src)
	if len(errs) != 0 {
		t.Fatalf("a complete brace at a struct field was refused: %v", errs)
	}
}

// --- opacity, which the depth could have opened a door around -------------

// `opaquePatchField`'s header names this exact shape as the reason the
// `Struct.update` guard is keyed on the PATCH rather than on the receiver: a
// transparent wrapper reaches an opaque field with a non-opaque receiver. The
// spread's nested arm is a second route to it, so it asks the same question
// through the same helper.
func TestSpreadDeep_OpaqueFieldThroughASpreadIsRefused(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{self, Counter}

struct Box {
  inner: counter.Counter
  label: String
}

fn wreck(b: Box): Box {
  {..b, inner: {value: -999}}
}
`))
	expectOpaquePatchRefused(t, errs, "value")
}

// Three levels, with the opaque struct at the bottom. A guard that only asked
// at the first nested level would pass the test above and miss this.
func TestSpreadDeep_OpaqueFieldTwoLevelsDownIsRefused(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{self, Counter}

struct Box {
  inner: counter.Counter
  label: String
}

struct Crate {
  box: Box
  n: Int
}

fn wreck(c: Crate): Crate {
  {..c, box: {inner: {value: -999}}}
}
`))
	expectOpaquePatchRefused(t, errs, "value")
}

// The positives behind those two zeros. An over-broad guard — one that refused
// any nested patch reaching a struct declared in another file, or any patch
// whose path passes an opaque field — passes both negatives and breaks these.
func TestSpreadDeep_OpaqueNeighbourAndWholesaleReplacementStillWork(t *testing.T) {
	cases := map[string]string{
		// Patching the wrapper's own transparent field, with an opaque
		// field sitting beside it untouched.
		"a transparent field beside an opaque one": `  {..b, label: "x"}`,
		// Replacing the opaque field wholesale. The patch names `Box.inner`,
		// which is Box's field and not Counter's, so nothing is exposed.
		"replacing the opaque field wholesale": `  {..b, inner: counter.new()}`,
		// A nested patch into a NON-opaque struct from another file.
		"a nested patch into a non-opaque sibling type": `  {..b, open: {a: 9}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{self, Counter}
import open.{self, Open}

struct Box {
  inner: counter.Counter
  open: open.Open
  label: String
}

fn ok(b: Box): Box {
`+body+`
}
`))
			if len(errs) != 0 {
				t.Fatalf("expected no diagnostics, got %v", errs)
			}
		})
	}
}

// --- the generic question, answered in both directions --------------------

// An UNSOLVED type parameter is not struct-shaped, so the position takes a
// value and the brace is refused. The solved case is a row in
// spreadDeepPatches; this is the other side of it, and it is what says the
// depth follows the substitution rather than guessing.
func TestSpreadDeep_UnsolvedTypeParameterFieldTakesAValue(t *testing.T) {
	const src = spreadDeepDecls + `fn patch_it<T>(b: Box<T>): Box<T> {
  {..b, item: {a: 9}}
}

fn main() {
  _a = patch_it(Box{item: Inner{a: 1, b: 2}, tag: "t"})
}
`
	_, errs := checkSourceWithStdlib(src)
	if len(errs) == 0 {
		t.Fatal("a brace was accepted at an UNSOLVED type-parameter field; the depth must follow the substitution, not the syntax alone")
	}
}

// The same generic function with a VALUE at the type-parameter field, which
// must still work. Without this the test above would pass for a build that
// had broken `Box<T>` entirely.
func TestSpreadDeep_UnsolvedTypeParameterFieldStillTakesItsValue(t *testing.T) {
	const src = spreadDeepDecls + `fn patch_it<T>(b: Box<T>, v: T): Box<T> {
  {..b, item: v}
}

fn main() {
  _a = patch_it(Box{item: Inner{a: 1, b: 2}, tag: "t"}, Inner{a: 9, b: 8})
}
`
	if _, errs := checkSourceWithStdlib(src); len(errs) != 0 {
		t.Fatalf("expected no diagnostics, got %v", errs)
	}
}

// --- the editor side, which came for free and is pinned anyway -------------

// THE NESTED FIELD LABEL CARRIES THE NESTED STRUCT'S FIELD TYPE, so hover on
// the `city` in `{..user, address: {city: "NYC"}}` reads `city: String`
// against `Address.city`.
//
// It comes for free because the recursion is the validator:
// `recordLitFieldLabelTypes` runs at the end of every
// `checkStructLitAgainstStruct` call, including the nested one, and mints a
// `SymbolField` for a label the builder skipped — which is every label of an
// anonymous literal, since `registerStructLitFieldRefs` returns early for
// `TypeName == nil`.
//
// # THE FIRST VERSION OF THIS TEST WAS VACUOUS, AND THE REASON GENERALIZES
//
// It scanned every entry of `References` for a `SymbolField` named `a` with
// type `Int`, and passed under two mutants that deleted the nested recursion
// outright. The symbol it was finding belonged to `Inner{a: 1, b: 2}` on the
// line ABOVE — the base's own nominal literal, which of course records its
// labels. A whole-map scan cannot tell two same-named labels apart, and this
// source necessarily contains both: the patch has nothing to patch without a
// base, and building the base names the same fields.
//
// So the assertion is on the POSITION, located by searching the source text
// rather than by counting columns. Verified to fail under both mutants.
func TestSpreadDeep_NestedFieldLabelHoversTheNestedType(t *testing.T) {
	const patchLine = `  _n = {..o, inner: {a: 9}}`
	src := spreadDeepDecls + "fn main() {\n" +
		"  o = Outer{inner: Inner{a: 1, b: 2}, k: 3}\n" +
		patchLine + "\n}\n"

	fa, errs := checkSourceWithStdlib(src)
	if len(errs) != 0 {
		t.Fatalf("expected no diagnostics, got %v", errs)
	}

	pos, ok := labelPos(t, src, patchLine, "{a: 9}", "a")
	if !ok {
		return
	}
	sym := fa.References[pos]
	if sym == nil {
		t.Fatalf("the nested patch label at %v has no symbol at all; hover would read nothing where Struct.update's nested label reads 'a: Int'", pos)
	}
	if sym.Kind != analysis.SymbolField || sym.Name != "a" {
		t.Fatalf("the nested patch label at %v resolved to kind %v %q, want the field 'a'", pos, sym.Kind, sym.Name)
	}
	if sym.Type == nil || sym.Type.String() != "Int" {
		t.Fatalf("the nested patch label 'a' carries type %v, want Int — the type of Inner.a, not of Outer.inner", sym.Type)
	}
}

// labelPos locates a label inside one line of a source, by text. `needle`
// narrows to the right occurrence within the line (there is more than one `a`
// on the patch line), and `label` is found inside it.
//
// Both offsets are asserted rather than assumed: a refactor that reworded the
// fixture would otherwise silently point this at column 1 and the test would
// go quiet instead of red.
func labelPos(t *testing.T, src, line, needle, label string) (analysis.Pos, bool) {
	t.Helper()
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		if l != line {
			continue
		}
		inNeedle := strings.Index(needle, label)
		at := strings.Index(l, needle)
		if at < 0 || inNeedle < 0 {
			t.Fatalf("the fixture no longer contains %q inside %q", label, needle)
		}
		return analysis.Pos{Line: i + 1, Col: at + inNeedle + 1}, true
	}
	t.Fatalf("the fixture no longer contains the line %q", line)
	return analysis.Pos{}, false
}
