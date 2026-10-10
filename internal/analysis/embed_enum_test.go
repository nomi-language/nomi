package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// checkParsedSource is checkSource for a source that must parse: checkSource
// recovers from parse errors and drops them, so a test that expects no
// errors would pass on a source the parser cut short.
func checkParsedSource(t *testing.T, src string) []TypeError {
	t.Helper()
	if _, perrs := parser.ParseWithRecovery(lexer.Lex(src)); len(perrs) > 0 {
		t.Fatalf("the source does not parse: %v", perrs)
	}
	_, errs := checkSource(src)
	return errs
}

// Enums do not embed enums (spec §8 *Embedded Types*). The error sits on
// the embedded name in the `embeds` line, and its help gives the variant
// that carries the enum as its payload.
func TestEmbed_EnumRejected(t *testing.T) {
	src := `enum Shape { Square Int; Dot }
enum Drawable {
    embeds Shape
    Line
}`
	errs := checkParsedSource(t, src)
	want := "`Shape` is an enum, and an enum cannot be embedded in another enum"
	var found *TypeError
	for i := range errs {
		if strings.Contains(errs[i].Message, want) {
			found = &errs[i]
		}
	}
	if found == nil {
		if len(errs) == 0 {
			t.Fatalf("the checker admits `embeds Shape` where Shape is an enum; spec §8 rejects it")
		}
		expectError(t, errs, want)
	}
	if found.Line != 3 || found.Col != 12 || found.EndLine != 3 || found.EndCol != 17 {
		t.Errorf("error at %d:%d-%d:%d, want 3:12-3:17 (the name after `embeds`)", found.Line, found.Col, found.EndLine, found.EndCol)
	}
	wantHint := "to hold a `Shape` in `Drawable`, declare a variant whose payload is the enum: `Shape Shape`"
	if len(found.Hints) != 1 || found.Hints[0] != wantHint {
		t.Errorf("hints = %q, want [%q]", found.Hints, wantHint)
	}
	if len(errs) != 1 {
		t.Errorf("want only the embeds error, got %d errors: %v", len(errs), errs)
	}
}

// A typealias naming an enum is that enum, so embedding it is rejected the
// same way, and the message names both.
func TestEmbed_AliasOfEnumRejected(t *testing.T) {
	src := `enum Shape { Square Int; Dot }
typealias Figure Shape
enum Drawable { embeds Figure; Line }`
	errs := checkParsedSource(t, src)
	if len(errs) == 0 {
		t.Fatalf("the checker admits `embeds Figure` where Figure is an alias of an enum")
	}
	expectError(t, errs, "`Figure` names the enum `Shape`, and an enum cannot be embedded in another enum")
}

// The help's form works: a variant whose payload is the enum checks, and a
// payload pattern matches through it.
func TestEmbed_EnumAsVariantPayloadChecks(t *testing.T) {
	src := `enum Shape { Square Int; Dot }
enum Drawable { Shape Shape; Line }
fn side(d: Drawable): Int {
  case d {
    Drawable.Shape(Shape.Square(n)) -> n
    _ -> 0
  }
}`
	errs := checkParsedSource(t, src)
	expectNoErrors(t, errs)
}

// Structs and distinct types still embed, a distinct over a struct and a
// distinct over an enum among them: a distinct is a type of its own.
func TestEmbed_StructAndDistinctStillCheck(t *testing.T) {
	src := `struct Circle { radius: Float }
type Wrapped Circle
enum Shape { Square Int; Dot }
type Boxed Shape
enum Drawable {
    embeds Circle
    embeds Wrapped
    embeds Boxed
    Line
}
fn radius(d: Drawable): Float {
  case d {
    Drawable.Circle{radius} -> radius
    Drawable.Wrapped({radius}) -> radius
    Drawable.Boxed(Shape.Square(_)) -> 1.0
    _ -> 0.0
  }
}`
	errs := checkParsedSource(t, src)
	expectNoErrors(t, errs)
}
