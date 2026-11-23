package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// An operand that declines leaves its region behind, its blocks terminated
// (a `todo` arm, a jump to the join). The expression around it declines
// without trying another route: `Some(...)` retried as a call of the
// constructor's function value appended to the block the abandoned operand
// terminated, a compiler panic ("ir: Block.Append after jump").
func TestIRDecline_ADeclinedOperandAbandonsItsExpression(t *testing.T) {
	plantDecline(t)
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `fn planted_decline(n: Int) -> Int
    n
end

fn pick(n: Int) -> Option<Int>
    Some(
        begin
            m = Iter.loop(None, fn s: Option<Int> ->
                case s
                    Some(_) -> break s
                    None -> Some(planted_decline(n))
                end
            end)

            case m
                Some(v) -> v
                None -> todo
            end
        end
    )
end

fn main()
    _ = pick(1)
end
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects this, so it measures nothing: %v", err)
	}
	var reason string
	p.Declines.Observed = func(fn, r string) {
		if fn == "pick" {
			reason = r
		}
	}
	if _, _, err := GenerateIR(p); err != nil {
		t.Fatal(err)
	}
	if reason != "a planted decline" {
		t.Fatalf("pick declined for %q; want the planted decline in its operand", reason)
	}
}
