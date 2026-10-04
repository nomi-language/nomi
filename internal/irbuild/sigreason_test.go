package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// TestSigReason_NamesAReasonForExactlyWhatTypeOfRefuses is the drift pin on the
// deliberate duplication between typeOf and typeRefusal.
//
// typeRefusal walks the same four naming forms typeOf does, in the same order,
// and does not share code with it — threading a reason string through typeOf
// would allocate on a path that runs for every annotation in every module to
// carry something almost nothing reads. The cost of that choice is that the two
// can disagree, and a disagreement is SILENT in both directions: a type typeOf
// refuses and typeRefusal calls fine vanishes from the tally, and a type typeOf
// accepts and typeRefusal calls broken never fires at all but would if any
// caller changed order.
//
// So this drives both over one corpus of annotations and requires the answers
// to be complements. It is a property, not a table: adding a form to typeOf
// without adding it here fails without anybody remembering to update a list.
func TestSigReason_NamesAReasonForExactlyWhatTypeOfRefuses(t *testing.T) {
	src := `import std/io
import std/literals.Literal

struct Point {
  x: Int
  y: Int
}

interface Speaker {
  fn speak(s: self): String
}

impl Speaker for Point {
  fn speak(s: Point): String { _ = s;
    "point"
  }
}

pub enum Owner.Nested {
  A
  B Int
}

// Handler is a LOWERED row: a module-level alias is transparent (typealias.go).
// u: Literal<Int, Int> supplies the refused half with a type whose absence is a
// language-level decision: a generic interface has no existential
// representation.
typealias Handler (String) -> String

fn every(
  _a: Int,
  _b: Float,
  _c: String,
  _d: Bool,
  _e: Point,
  _f: List<Int>,
  _g: List<Point>,
  _h: Map<String, Int>,
  _i: (Int, String),
  _j: (Int) -> String,
  _k: Maybe<Int>,
  _l: Result<Int, String>,
  _m: Speaker,
  _n: Owner.Nested,
  _o: Ordering,
  _p: Decimal,
  _r: Handler,
  _s: {x: Int},
  _t: List<Ordering>,
  _u: Literal<Int, Int>,
  _v: Iter<Int>
): Int {
  0
}

fn main() {
  io.print("x")
}
`
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("front end rejected the witness: %v", err)
	}
	g := probeGen(t, p)

	var checked, refused int
	for _, n := range p.Modules[0].Nodes {
		fd, isFunc := n.(*ast.FuncDef)
		if !isFunc || fd.Name != "every" {
			continue
		}
		for _, prm := range fd.Params {
			checked++
			k := g.typeOf(prm.TypeAnnotation)
			construct, detail, named := g.typeRefusal(prm.TypeAnnotation)
			switch {
			case k == kindInvalid && !named:
				t.Errorf("%s: typeOf refused %s and typeRefusal named no reason — "+
					"the gap would vanish from the tally",
					prm.Name, prm.TypeAnnotation.TypeString())
			case k != kindInvalid && named:
				t.Errorf("%s: typeOf lowered %s to %s and typeRefusal called it %q (%s) — "+
					"a reason for a type that has none fires the moment a caller reorders",
					prm.Name, prm.TypeAnnotation.TypeString(), k.nomi(), construct, detail)
			case k == kindInvalid:
				refused++
				if construct == "" || detail == "" {
					t.Errorf("%s: a reason must name a construct AND an operand, got %q / %q",
						prm.Name, construct, detail)
				}
			}
		}
	}
	if checked != 21 {
		t.Fatalf("expected to check 21 parameters, checked %d", checked)
	}
	// Both halves must be non-empty or the property is vacuous: a typeRefusal
	// that named everything would pass the first arm, and one that named
	// nothing would pass the second.
	if refused == 0 || refused == checked {
		t.Fatalf("the witness must contain BOTH lowered and refused annotations; %d of %d refused",
			refused, checked)
	}
}

// probeGen builds a module's gen far enough to answer type questions, without
// emitting anything. The same construction inferred_test.go uses, plus the
// interface and mirror tables typeRefusal reads.
func probeGen(t *testing.T, p *Program) *gen {
	t.Helper()
	g := newGen(&p.Modules[0], "nomimod0", nil, nil, -1, nil)
	g.declareTypes()
	return g
}
