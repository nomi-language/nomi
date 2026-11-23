package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// irrefutableSrc writes patterns that always match and patterns that can
// fail in each position: a binding with and without `else`, a `case` arm,
// an `if` pattern, an `is` pattern and nested ones.
const irrefutableSrc = `enum Opt
    Has Int
    Not
end

enum Side
    L Int
    R Int
end

enum Only
    One Int
end

struct Point
    x: Int
    y: Int
end

refine Port Int when fn port -> port >= 1 and port <= 65535

fn bindings(o: Opt, s: Side, only: Only, pt: Point) -> Int
    (a, _) = (1, 2)
    {x:, y: _} = pt
    .One(n) = only
    .L(k) or .R(k) = s
    .Has(h) = o else return 0
    a + x + n + k + h
end

fn fallback(o: Opt) -> Int
    .Has(h) = o else 0
    h
end

fn arms(o: Opt, p: Port) -> Int
    m = case o
        .Has(1) -> 1
        .Has(v) -> v
        .Not -> 0
    end
    q = case p
        80 -> 80
        Port(n) -> n
    end
    m + q
end

fn tests(o: Opt) -> Bool
    if .Has(v) = o then
        v > 0
    else
        o is .Not
    end
end
`

// TestIrrefutablePatterns_RecordWhetherAPatternAlwaysMatches holds
// IrrefutablePatterns to the answer for each pattern in irrefutableSrc,
// by its source text, and BindingFallbacks to each binding's `else`.
func TestIrrefutablePatterns_RecordWhetherAPatternAlwaysMatches(t *testing.T) {
	fa, nodes := checkNodes(t, irrefutableSrc)
	want := map[string]bool{
		"(a, _)":         true,
		"a":              true,
		"{x:, y: _}":     true,
		".One(n)":        true,
		".L(k) or .R(k)": true,
		".L(k)":          false,
		".Has(h)":        false,
		".Has(1)":        false,
		"1":              false,
		".Has(v)":        false,
		".Not":           false,
		"80":             false,
		"Port(n)":        true,
	}
	seen := map[string]bool{}
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			if _, typed := fa.PatternTypes[n]; !typed {
				return true
			}
			text := patternSource(n)
			got, recorded := fa.PatternIrrefutable(n)
			if !recorded {
				t.Errorf("%s at line %d has a type and no record of whether it always matches", text, n.LineNum())
				return true
			}
			expect, listed := want[text]
			if !listed {
				return true
			}
			seen[text] = true
			if got != expect {
				t.Errorf("%s at line %d: recorded %v, want %v", text, n.LineNum(), got, expect)
			}
			return true
		})
	}
	for text := range want {
		if !seen[text] {
			t.Errorf("no pattern %s was recorded", text)
		}
	}
	fallbacks := 0
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			pb, isBinding := n.(*ast.PatternBinding)
			if !isBinding || pb.Else == nil {
				return true
			}
			fallbacks++
			payload, recorded := fa.BindingFallback(pb)
			if !recorded || payload == nil || !TypesEqual(payload, TypeInt) {
				t.Errorf("the binding at line %d records the fallback %v (recorded %v), want Int", pb.Line, payload, recorded)
			}
			return true
		})
	}
	if fallbacks != 2 {
		t.Errorf("found %d bindings with an else, want 2", fallbacks)
	}
}

// TestIrrefutablePatterns_TheGateHoldsTheRecords removes each pattern's
// record of whether it always matches, and each binding's record of its
// fallback, and holds VerifyElaboration to reporting it.
func TestIrrefutablePatterns_TheGateHoldsTheRecords(t *testing.T) {
	fa, nodes := checkNodes(t, irrefutableSrc)
	for _, v := range VerifyElaboration(fa, nodes) {
		switch v.Category {
		case ElabPatternUnrecorded, ElabPatternIrrefutableUnrecorded, ElabBindingFallbackUnrecorded:
			t.Fatalf("want no pattern reported with every record, got %v", v)
		}
	}
	patterns, bindings := 0, 0
	for n, always := range fa.IrrefutablePatterns {
		switch n.(type) {
		case *ast.IntLit:
			// A literal pattern is not held (checkPattern).
			continue
		}
		patterns++
		delete(fa.IrrefutablePatterns, n)
		if !hasElabCategory(VerifyElaboration(fa, nodes), n, ElabPatternIrrefutableUnrecorded) {
			t.Errorf("%s at line %d with no record is not reported", patternSource(n), n.LineNum())
		}
		fa.IrrefutablePatterns[n] = always
	}
	for pb, payload := range fa.BindingFallbacks {
		bindings++
		delete(fa.BindingFallbacks, pb)
		if !hasElabCategory(VerifyElaboration(fa, nodes), pb, ElabBindingFallbackUnrecorded) {
			t.Errorf("the binding at line %d with no fallback record is not reported", pb.Line)
		}
		fa.BindingFallbacks[pb] = payload
	}
	if patterns == 0 || bindings == 0 {
		t.Fatalf("the source has %d patterns and %d bindings with an else to hold", patterns, bindings)
	}
}
