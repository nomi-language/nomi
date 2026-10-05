package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// Two files, each declaring an interface of the same name, and a receiver they
// can both reach. `analysis.InterfaceType` carried no `Origin` until this
// slice, so both of these answered by SPELLING.
//
// The witnesses are deliberately a matched pair with the same shape and one
// difference — whether the two same-named interfaces are both implemented for
// the receiver or only one is — because the two shapes need OPPOSITE outcomes
// and a single witness cannot show that.

// buildSiblingProject analyzes an entry plus named sibling files, returning
// every diagnostic the whole project produced.
func buildSiblingProject(t *testing.T, entry string, siblings map[string]string) []analysis.TypeError {
	t.Helper()
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "main.nomi")
	if err := os.WriteFile(mainPath, []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, src := range siblings {
		if err := os.WriteFile(filepath.Join(tmp, name+".nomi"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tokens := lexer.Lex(entry)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa, sibFAs, sibNodes := analysis.BuildProjectFromEntry(
		mainPath, nodes, lib.Primitives, lib.Modules, lib.Files, tmp, std.MakeLoader())
	errs := append([]analysis.TypeError{}, fa.TypeErrors...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	for key, sibFA := range sibFAs {
		errs = append(errs, sibFA.TypeErrors...)
		errs = append(errs, analysis.CheckTypes(sibFA, sibNodes[key])...)
	}
	return errs
}

func joinMessages(errs []analysis.TypeError) string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Message
	}
	return strings.Join(out, "\n  ")
}

const identityShapes = `pub struct Point {
  x: Int
  y: Int
}
`

// TestInterfaceIdentity_ABoundNamesOneDeclarationNotASpelling is the soundness
// witness, and the reason the field was worth adding.
//
// Without the field, this program was accepted, compiled, RAN and
// printed `ascii(1,2)`. `svg.draw`'s `where T: Renderer` names svg.nomi's
// declaration; `Point` implements ascii.nomi's, a different declaration with no
// relationship to it. The bound check keyed `Impls[Point][Renderer]` on the
// BARE name, so it passed, and the call then dispatched into the other
// interface's method.
//
// That is a wrong ANSWER rather than a coverage gap, and it is the same root
// cause as the prelude-interface gap fenced by reserving the prelude's names —
// except no reservation can reach this one, because both names are the user's.
func TestInterfaceIdentity_ABoundNamesOneDeclarationNotASpelling(t *testing.T) {
	errs := buildSiblingProject(t, `import shapes.{Point}
import svg
import ascii

fn main() {
  _tag = ascii.tag()
  _drawn = svg.draw(Point{x: 1, y: 2})
}
`, map[string]string{
		"shapes": identityShapes,
		"svg": `pub interface Renderer {
  fn render(value: self): String
}

pub fn draw<T>(x: T): String where T: Renderer {
  Renderer.render(x)
}
`,
		"ascii": `import shapes.{Point}

pub interface Renderer {
  fn render(value: self): String
}

impl Renderer for Point {
  fn render(p: Point): String {
    "ascii(${p.x},${p.y})"
  }
}

pub fn tag(): String {
  "ascii"
}
`,
	})
	// Exactly one diagnostic, and it is this one. Without the field the
	// program analyzed with ZERO diagnostics and RAN, printing `ascii(1,2)`.
	const want = "Point does not implement Renderer"
	if len(errs) != 1 || !strings.Contains(errs[0].Message, want) {
		t.Fatalf("a bound naming svg's `Renderer` must not be satisfied by an impl of ascii's `Renderer`;\nwant exactly one diagnostic containing %q, got %d:\n  %s",
			want, len(errs), joinMessages(errs))
	}
}

// sameNamedRenderer is a sibling file declaring its own `Renderer`,
// implementing it for the shared `Point`, and calling it.
func sameNamedRenderer(tag string) string {
	return `import shapes.{Point}

pub interface Renderer {
  fn render(value: self): String
}

impl Renderer for Point {
  fn render(p: Point): String {
    "` + tag + `(${p.x},${p.y})"
  }
}

pub fn show(p: Point): String {
  Renderer.render(p)
}
`
}

// TestInterfaceIdentity_TwoSameNamedInterfacesForOneReceiverAreAccepted is the
// other half of the pair: both declarations ARE implemented for the receiver.
//
// Accepting it is correct because dispatch keys each impl under its
// interface's declaration, not under the interface's bare name, so two
// same-named interfaces do not share one slot.
func TestInterfaceIdentity_TwoSameNamedInterfacesForOneReceiverAreAccepted(t *testing.T) {
	errs := buildSiblingProject(t, `import shapes.{Point}
import svg
import ascii

fn main() {
  p = Point{x: 1, y: 2}
  _svg = svg.show(p)
  _ascii = ascii.show(p)
}
`, map[string]string{
		"shapes": identityShapes,
		"svg":    sameNamedRenderer("svg"),
		"ascii":  sameNamedRenderer("ascii"),
	})
	if len(errs) != 0 {
		t.Fatalf("two same-named interfaces, each implemented ONCE for one receiver, were "+
			"rejected; got:\n  %s", joinMessages(errs))
	}
}

// TestInterfaceIdentity_ADuplicateOfOneDeclarationIsStillRejected is the plant
// the acceptance above needs, and loosening the check is the failure mode here.
//
// One `Renderer` declaration, imported by name into a second file, implemented
// for `Point` in BOTH. That is a genuine duplicate: the two impls key on one
// origin, so they contend for one dispatch slot. Byte-identical shape to the accepted program above apart from the
// second file importing the interface instead of declaring its own.
func TestInterfaceIdentity_ADuplicateOfOneDeclarationIsStillRejected(t *testing.T) {
	errs := buildSiblingProject(t, `import shapes.{Point}
import svg
import ascii

fn main() {
  _drawn = svg.show(Point{x: 1, y: 2})
}
`, map[string]string{
		"shapes": identityShapes,
		"svg":    sameNamedRenderer("svg"),
		"ascii": `import shapes.{Point}
import svg.{Renderer}

impl Renderer for Point {
  fn render(p: Point): String {
    "ascii(${p.x},${p.y})"
  }
}
`,
	})
	var got string
	for _, e := range errs {
		if strings.Contains(e.Message, "duplicate impl") && strings.Contains(e.Message, "Renderer") {
			got = e.Message
			break
		}
	}
	if got == "" {
		t.Fatalf("two impls of ONE `Renderer` declaration for one receiver were accepted; got:\n  %s",
			joinMessages(errs))
	}
	if !strings.Contains(got, "shapes.Point") {
		t.Errorf("the duplicate diagnostic does not name the receiver's identity:\n  %s", got)
	}
}

// TestInterfaceIdentity_OneDeclarationStillSatisfiesItsOwnBound is the control
// the pair above needs: identity must not reject the ordinary case.
//
// Without it, a change that made `interfaceImplemented` answer "no" for
// everything would pass both witnesses above.
func TestInterfaceIdentity_OneDeclarationStillSatisfiesItsOwnBound(t *testing.T) {
	errs := buildSiblingProject(t, `import shapes.{Point}
import svg

fn main() {
  _drawn = svg.draw(Point{x: 1, y: 2})
}
`, map[string]string{
		"shapes": identityShapes,
		"svg": `import shapes.{Point}

pub interface Renderer {
  fn render(value: self): String
}

impl Renderer for Point {
  fn render(p: Point): String {
    "svg(${p.x},${p.y})"
  }
}

pub fn draw<T>(x: T): String where T: Renderer {
  Renderer.render(x)
}
`,
	})
	if len(errs) != 0 {
		t.Fatalf("an impl and a bound naming ONE declaration must agree; got:\n  %s", joinMessages(errs))
	}
}

// TestInterfaceIdentity_StdlibBoundsStillHold is the second control, and it is
// the one that would catch the field being populated with the WRONG origin.
//
// A prelude interface is declared in one std file and implemented in others —
// `Display` in std/display.nomi, `impl Display for Int` in std/int.nomi — so
// every stdlib conformance crosses a file boundary. If the impl side and the
// bound side ever disagreed about which file declared `Display`, this fails
// while the two witnesses above keep passing.
func TestInterfaceIdentity_StdlibBoundsStillHold(t *testing.T) {
	errs := buildSiblingProject(t, `import std/io

fn describe<T>(x: T): String where T: Display {
  Display.to_string(x)
}

fn main() {
  io.print(describe(42))
  io.print(describe("hi"))
}
`, nil)
	if len(errs) != 0 {
		t.Fatalf("a stdlib bound must still be satisfied by its stdlib impls; got:\n  %s", joinMessages(errs))
	}
}
