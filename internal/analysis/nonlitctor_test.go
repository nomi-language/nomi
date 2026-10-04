package analysis

import (
	"strings"
	"testing"
)

// The struct CALL form with a NON-LITERAL argument: `MyApp(defaults())`.
//
// checkStructCallForm accepts a non-literal argument as well as an anonymous
// struct LITERAL. Construction reads the argument's fields by name, fills the
// declaration's defaults and brands the result with the module-qualified
// name, so the non-literal form works at run time in every shape including a
// reversed field order. This file is the checker's side.
//
// The four things below are the same rules the literal twin applies, which
// is the point: an argument that is a matching record and an argument that
// is a matching literal are accepted and rejected for the same reasons and
// with the same words. What degrades is the POSITION — an *AnonStructType
// has field names and types and no Pos, so the diagnostic points at the
// argument instead of at the offending field. The field NAME survives in
// every message.

const nonLitP = `
struct P {
  a: Int
  b: String
}
`

// errorTexts is the diagnostics of src with the unrelated unused-binding
// lint dropped, so a table row reads as the shape it is about.
func errorTexts(t *testing.T, src string) []string {
	t.Helper()
	_, errs := checkSource(src)
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		if strings.Contains(e.Message, "is never read") {
			continue
		}
		out = append(out, e.Message)
	}
	return out
}

// TestStructCallForm_RecordArgumentAccepted is interaction 4, FIELD ORDER,
// plus the plain matching case.
//
// Order-independence is REQUIRED rather than incidental: the motivating
// program declares `struct MyApp { context, port }` and calls
// `MyApp(defaults())` where `defaults(): {port: Int, context: Context}`. The
// reversed row is that program's shape reduced to two scalars, and it is the
// row that would fail if the match were positional.
func TestStructCallForm_RecordArgumentAccepted(t *testing.T) {
	rows := []struct {
		name string
		src  string
	}{
		{"literal (control)", nonLitP + "\nfn f(): P { P({a: 1, b: \"x\"}) }\n"},
		{"local binding", nonLitP + "\nfn f(): P {\n  m = {a: 1, b: \"x\"}\n  P(m)\n}\n"},
		{"annotated binding", nonLitP + "\nfn f(): P {\n  m: {a: Int, b: String} = {a: 1, b: \"x\"}\n  P(m)\n}\n"},
		{"call result, declaration order", nonLitP + "\nfn rec(): {a: Int, b: String} { {a: 1, b: \"x\"} }\n\nfn f(): P { P(rec()) }\n"},
		{"call result, REVERSED order", nonLitP + "\nfn rec(): {b: String, a: Int} { {b: \"x\", a: 1} }\n\nfn f(): P { P(rec()) }\n"},
		{"parameter", nonLitP + "\nfn f(m: {a: Int, b: String}): P { P(m) }\n"},
		{"parameter, REVERSED order", nonLitP + "\nfn f(m: {b: String, a: Int}): P { P(m) }\n"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			if got := errorTexts(t, r.src); len(got) != 0 {
				t.Errorf("want accepted, got %v", got)
			}
		})
	}
}

// TestStructCallForm_RecordArgumentDefaults is interaction 1, FIELD
// DEFAULTS. A record omitting a DEFAULTED field is accepted; one omitting a
// REQUIRED field is the literal form's own error, naming the field.
func TestStructCallForm_RecordArgumentDefaults(t *testing.T) {
	const defaulted = `
struct D {
  a: Int
  b: String = "d"
}
`
	t.Run("omitted defaulted field is accepted", func(t *testing.T) {
		src := defaulted + "\nfn rec(): {a: Int} { {a: 1} }\n\nfn f(): D { D(rec()) }\n"
		if got := errorTexts(t, src); len(got) != 0 {
			t.Errorf("want accepted, got %v", got)
		}
	})
	t.Run("empty record against all-defaulted struct", func(t *testing.T) {
		const allDef = `
struct A {
  p: Int = 1
  q: Int = 2
}
`
		// A record with no fields is not expressible, so the parameter
		// position is how an all-defaulted struct is reached by a record.
		src := allDef + "\nfn f(m: {p: Int}): A { A(m) }\n"
		if got := errorTexts(t, src); len(got) != 0 {
			t.Errorf("want accepted, got %v", got)
		}
	})
	t.Run("omitted required field names the field", func(t *testing.T) {
		src := nonLitP + "\nfn rec(): {a: Int} { {a: 1} }\n\nfn f(): P { P(rec()) }\n"
		got := errorTexts(t, src)
		want := "missing field 'b' of P"
		if len(got) != 1 || got[0] != want {
			t.Errorf("got %v, want exactly [%q]", got, want)
		}
	})
	t.Run("the LITERAL form says the same thing", func(t *testing.T) {
		// The identical-behaviour claim, asserted rather than inspected: the
		// two argument shapes must produce the same words for the same fault.
		src := nonLitP + "\nfn f(): P { P({a: 1}) }\n"
		got := errorTexts(t, src)
		want := "missing field 'b' of P"
		if len(got) != 1 || got[0] != want {
			t.Errorf("got %v, want exactly [%q]", got, want)
		}
	})
}

// TestStructCallForm_RecordArgumentFieldFaults is interaction 5, EXTRA and
// MISSING fields, and the field-type mismatch beside them. Every message
// names the field.
func TestStructCallForm_RecordArgumentFieldFaults(t *testing.T) {
	rows := []struct {
		name string
		src  string
		want string
	}{
		{
			"field the target does not have",
			nonLitP + "\nfn rec(): {a: Int, b: String, c: Int} { {a: 1, b: \"x\", c: 2} }\n\nfn f(): P { P(rec()) }\n",
			"P has no field 'c'",
		},
		{
			"field of the wrong type",
			nonLitP + "\nfn rec(): {a: String, b: String} { {a: \"n\", b: \"x\"} }\n\nfn f(): P { P(rec()) }\n",
			"field 'a' of P: expected Int, got String",
		},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			got := errorTexts(t, r.src)
			if len(got) != 1 || got[0] != r.want {
				t.Fatalf("got %v, want exactly [%q]", got, r.want)
			}
			// The degradation contract: the column may be the argument's
			// rather than the field's, but the NAME must be in the text.
			if !strings.Contains(got[0], "'c'") && !strings.Contains(got[0], "'a'") {
				t.Errorf("message lost the field name: %q", got[0])
			}
		})
	}
	// The literal twin's wording, for the same two faults.
	for _, r := range []struct {
		name string
		src  string
		want string
	}{
		{"literal, unknown field", nonLitP + "\nfn f(): P { P({a: 1, b: \"x\", c: 2}) }\n", "P has no field 'c'"},
		{"literal, wrong type", nonLitP + "\nfn f(): P { P({a: \"n\", b: \"x\"}) }\n", "field 'a' of P: expected Int, got String"},
	} {
		t.Run(r.name, func(t *testing.T) {
			got := errorTexts(t, r.src)
			if len(got) != 1 || got[0] != r.want {
				t.Errorf("got %v, want exactly [%q]", got, r.want)
			}
		})
	}
}

// TestStructCallForm_StillRefusesNonRecordArguments is the boundary of the
// change. The refusal was NARROWED, not removed: an argument whose type is
// not an anonymous struct keeps the original message, and the nominal
// literal `P(Q{...})` is the row that matters — two structs of the same
// shape are two types (nominal identity), so accepting it would be the
// structural coercion that is out of scope.
func TestStructCallForm_StillRefusesNonRecordArguments(t *testing.T) {
	rows := []struct {
		name string
		src  string
		want string
	}{
		{
			"scalar argument",
			nonLitP + "\nfn f(): P { P(7) }\n",
			"P expects an anonymous struct literal {...} matching its fields, got Int",
		},
		{
			"same-shaped NOMINAL struct",
			nonLitP + "\nstruct Q {\n  a: Int\n  b: String\n}\n\nfn f(): P { P(Q{a: 1, b: \"x\"}) }\n",
			"P expects an anonymous struct literal {...} matching its fields, got Q",
		},
		{
			"tuple argument",
			nonLitP + "\nfn rec(): (Int, String) { (1, \"x\") }\n\nfn f(): P { P(rec()) }\n",
			"P expects an anonymous struct literal {...} matching its fields, got (Int, String)",
		},
		{
			"two arguments",
			nonLitP + "\nfn f(): P { P({a: 1}, {b: \"x\"}) }\n",
			"P takes 1 argument, got 2; expected P({...}) with the struct's fields",
		},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			got := errorTexts(t, r.src)
			found := false
			for _, m := range got {
				if m == r.want {
					found = true
				}
			}
			if !found {
				t.Errorf("got %v, want it to contain %q", got, r.want)
			}
		})
	}
}

// TestStructCallForm_OpaqueStructStillPrivate is interaction 3. Opacity is
// enforced BEFORE the argument is looked at, so the non-literal shape is not
// a transparent route past it — the defect closed at 224c94dc.
//
// Both controls are here and both are load-bearing. The LITERAL form from
// outside must refuse too (or the test is measuring the arm rather than the
// guard), and the non-literal form from INSIDE the declaring file must be
// ACCEPTED (or the guard is refusing everything and the zero proves nothing).
func TestStructCallForm_OpaqueStructStillPrivate(t *testing.T) {
	const owner = `pub opaque struct Secret {
  token: String
  ttl: Int
}

fn defaults(): {ttl: Int, token: String} { {ttl: 30, token: "t"} }

pub fn make(): Secret { Secret(defaults()) }

pub fn make_literal(): Secret { Secret({token: "t", ttl: 30}) }
`
	const priv = "constructor of opaque type 'Secret' is private to its defining module — use an exported constructor function"

	t.Run("inside the declaring file, record form is accepted", func(t *testing.T) {
		// THE POSITIVE. `make` and `make_literal` both construct in the
		// owning file; if this row failed, the refusals below would be
		// telling us nothing about opacity.
		_, errs := checkSourceWithModules(t, "import secret\n\nfn f(): Int { 0 }\n",
			map[string]string{"secret": owner})
		for _, e := range errs {
			if strings.Contains(e.Message, "Secret") {
				t.Errorf("owning file refused its own construction: %s", e.Message)
			}
		}
	})
	t.Run("outside, record form still refuses", func(t *testing.T) {
		src := "import secret.{self, Secret}\n\n" +
			"fn rec(): {token: String, ttl: Int} { {token: \"t\", ttl: 9} }\n\n" +
			"fn sneak(): Secret { Secret(rec()) }\n"
		if !hasMessage(t, src, map[string]string{"secret": owner}, priv) {
			t.Error("the record form got past the opacity guard")
		}
	})
	t.Run("outside, literal form still refuses", func(t *testing.T) {
		src := "import secret.{self, Secret}\n\n" +
			"fn sneak(): Secret { Secret({token: \"t\", ttl: 9}) }\n"
		if !hasMessage(t, src, map[string]string{"secret": owner}, priv) {
			t.Error("the literal form got past the opacity guard")
		}
	})
}

func hasMessage(t *testing.T, src string, modules map[string]string, want string) bool {
	t.Helper()
	_, errs := checkSourceWithModules(t, src, modules)
	for _, e := range errs {
		if e.Message == want {
			return true
		}
	}
	t.Logf("diagnostics were: %v", errs)
	return false
}

// TestStructCallForm_GenericRecordArgumentSolvesTypeArgs pins that the
// record form infers a generic struct's type arguments the way the literal
// form does — the returned StructType carries them, so `Box(rec)` is
// `Box<Int>` and not bare `Box`.
func TestStructCallForm_GenericRecordArgumentSolvesTypeArgs(t *testing.T) {
	const src = `
struct Box<T> {
  v: T
}

fn rec(): {v: Int} { {v: 1} }

fn f(): Box<Int> { Box(rec()) }

fn g(): Box<Int> { Box({v: 1}) }
`
	if got := errorTexts(t, src); len(got) != 0 {
		t.Errorf("want accepted, got %v", got)
	}
	// The negative: a record whose field type is not the one the annotation
	// names must still be caught, or the row above would pass for a Box of
	// anything.
	const bad = `
struct Box<T> {
  v: T
}

fn rec(): {v: String} { {v: "s"} }

fn f(): Box<Int> { Box(rec()) }
`
	if got := errorTexts(t, bad); len(got) == 0 {
		t.Error("Box<Int> accepted a {v: String} record")
	}
}

// TestStructCallForm_InterfaceTypedFieldFromARecord covers the field shape
// that goes through `argMatchesParam`'s impl-coercion path rather than
// through TypesEqual: the record supplies a CONCRETE type where the
// declaration wants an interface, so accepting it depends on the impl
// registry and on the coercion being RECORDED for the manifest.
//
// The recording position is the argument's here, not the field value's —
// there is no field value node — so the pair below is what says the
// recording still lands: with an impl the construction is accepted, and
// without one it is refused in the literal form's own words.
func TestStructCallForm_InterfaceTypedFieldFromARecord(t *testing.T) {
	const decl = `
interface Greeter {
  fn greet(g: self): String
}

struct En {
  name: String
}

struct Site {
  greeter: Greeter
  port: Int
}

fn defaults(): {port: Int, greeter: En} { {port: 80, greeter: En{name: "x"}} }
`
	const impl = `
impl Greeter for En {
  fn greet(g: En): String { g.name }
}
`
	t.Run("with an impl, accepted", func(t *testing.T) {
		if got := errorTexts(t, decl+impl+"\nfn f(): Site { Site(defaults()) }\n"); len(got) != 0 {
			t.Errorf("want accepted, got %v", got)
		}
	})
	t.Run("without an impl, refused by field name", func(t *testing.T) {
		got := errorTexts(t, decl+"\nfn f(): Site { Site(defaults()) }\n")
		want := "field 'greeter' of Site: expected Greeter, got En"
		if len(got) != 1 || got[0] != want {
			t.Errorf("got %v, want exactly [%q]", got, want)
		}
	})
	t.Run("the LITERAL form says the same thing", func(t *testing.T) {
		got := errorTexts(t, decl+"\nfn f(): Site { Site({port: 80, greeter: En{name: \"x\"}}) }\n")
		want := "field 'greeter' of Site: expected Greeter, got En"
		if len(got) != 1 || got[0] != want {
			t.Errorf("got %v, want exactly [%q]", got, want)
		}
	})
}
