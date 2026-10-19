package irbuild

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// TestIRDecline_TheSynthesisMaskFiresAndOnlyThere checks that the decline
// census masks derive-synthesized bodies.
//
// `irScalarBody`'s statement-form check runs before the lead loop's
// `emittableLine` and before the tail's, so a class reported from there would
// be credited with bodies whose real blocker is the synthesized line behind
// it. `irDeclineSynthMask` marks those bodies.
//
// It is a unit reading of `irDeclineSynthMask` rather than a lowering, because
// the mask's population is in `std`, whose index is built once per process and
// cached; a test that lowered source would see the rows only when it ran
// before every other test that builds the index.
//
// Both directions are required: a one-sided reading would pass for a mask that
// fires always and for one that never fires.
func TestIRDecline_TheSynthesisMaskFiresAndOnlyThere(t *testing.T) {
	body := func(line int) *ast.FuncDef {
		return &ast.FuncDef{Name: "f", Line: 1, Body: &ast.Block{
			Stmts: []ast.Node{&ast.ExprStmt{Expr: &ast.IntLit{Value: 1, Line: line}, Line: line}},
		}}
	}
	synthLine := 0
	for n := 1; n < 1<<31; n *= 2 {
		if analysis.IsSynthesizedLine(n) {
			synthLine = n
			break
		}
	}
	if synthLine == 0 {
		t.Fatal("no synthesized line could be constructed, so this plant measures nothing")
	}
	if got := irDeclineSynthMask(body(synthLine)); got == "" {
		t.Fatalf("a body whose only statement is on synthesized line %d carried no "+
			"mask, so the census credits derive artifacts to a statement form", synthLine)
	}
	if got := irDeclineSynthMask(body(7)); got != "" {
		t.Fatalf("a body written on a real line carried the mask %q, so it marks "+
			"every member of the row and distinguishes nothing", got)
	}
}

// An operand that declines leaves its region behind, its blocks terminated
// (a `todo` arm, a jump to the join). The expression around it declines
// without trying another route: `Some(...)` retried as a call of the
// constructor's function value appended to the block the abandoned operand
// terminated, a compiler panic ("ir: Block.Append after jump").
func TestIRDecline_ADeclinedOperandAbandonsItsExpression(t *testing.T) {
	plantDecline(t)
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `fn planted_decline(n: Int) -> Int {
    n
}

fn pick(n: Int) -> Maybe<Int> {
    Some(
        {
            m = Iter.loop(fn s: Maybe<Int> = None -> {
                case s {
                    Some(_) -> break s
                    None -> Some(planted_decline(n))
                }
            })

            case m {
                Some(v) -> v
                None -> todo
            }
        }
    )
}

fn main() {
    _ = pick(1)
}
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects this, so it measures nothing: %v", err)
	}
	var reason string
	IRDeclineObserved = func(fn, r string) {
		if fn == "pick" {
			reason = r
		}
	}
	defer func() { IRDeclineObserved = nil }()
	if _, _, err := GenerateIR(p); err != nil {
		t.Fatal(err)
	}
	if reason != "a planted decline" {
		t.Fatalf("pick declined for %q; want the planted decline in its operand", reason)
	}
}
