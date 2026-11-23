package highlight_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/internal/virtualproject"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// paymentSrc is the tour home page's exhaustiveness block: describe misses
// a variant, and main, which checks, debugs two of its calls.
const paymentSrc = `enum Payment
    Card String
    Transfer (String, Int)
    Cash
end

fn describe(payment: Payment) -> String
    case payment
        .Card(last4)                        -> "card ${last4}"
        .Transfer(bank, days) when days > 2 -> "slow: ${bank}"
        .Transfer(bank, _)                  -> "from ${bank}"
    end
end

fn main()
    dbg describe(.Card("4242"))
    dbg describe(.Transfer("Acme", 5))
end
`

// The tour's editors show a block that does not compile with the marks
// and lines of what checks (vmhost.ReachedMarked): in the Payment block
// main, which holds the `dbg`s, is marked, each `dbg` hovers with its
// effect, and the call of describe, which does not check, says nothing.
func TestProgramTokensAndHoverBesideAFunctionThatDoesNotCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	reached, err := vmhost.ReachedMarked(paymentSrc, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "non-exhaustive case on Payment") {
		t.Fatalf("ReachedMarked answers %v; want the block refused", err)
	}
	got := effectTokens(paymentSrc, highlight.SemanticTokens(paymentSrc, std.Shared(), reached, highlight.InFile("main.nomi")))
	if want := map[[2]int]string{{15, 4}: "main"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the Payment block marks %v as having effects; want %v", got, want)
	}
	fa, nodes := highlight.AnalyzeNodes(paymentSrc, std.Shared())
	inEntry := func(path string) bool { return virtualproject.NormalizeModuleName(path) == "main" }
	for _, line := range []int{16, 17} {
		if got := hoverdoc.AtReaching(fa, nodes, paymentSrc, analysis.Pos{Line: line, Col: 6}, reached, inEntry); !strings.HasSuffix(got, "\n\nHas effects: `dbg`.") {
			t.Errorf("hover on the dbg on line %d = %q; want it to end with what it reaches", line, got)
		}
		if got := hoverdoc.AtReaching(fa, nodes, paymentSrc, analysis.Pos{Line: line, Col: 10}, reached, inEntry); strings.Contains(got, "Has effects") {
			t.Errorf("hover on the call of describe on line %d = %q; want nothing about effects", line, got)
		}
	}
}
