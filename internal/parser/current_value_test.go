package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A bare `..` is the spread of an update literal with no head: the value
// already at the field or key the literal is written at. The parser gives it
// its own node, *ast.CurrentValue, at the `..`, and leaves the question of
// whether the position has a current value to the checker.

func TestParseCurrentValue_Struct(t *testing.T) {
	for _, src := range []string{
		`#{.., city: "NYC"}`,
		`#{.., city: "NYC",}`,
		`#{.., city}`,
		"#{\n  ..,\n  city: \"NYC\",\n}",
		"#{\n  .., // keep\n  city: \"NYC\",\n}",
		`#{..}`,
	} {
		t.Run(src, func(t *testing.T) {
			lit, ok := parseExpr(t, src).(*ast.StructLit)
			if !ok {
				t.Fatalf("%q: want *ast.StructLit", src)
			}
			cv, ok := lit.Spread.(*ast.CurrentValue)
			if !ok {
				t.Fatalf("%q: spread = %T, want *ast.CurrentValue", src, lit.Spread)
			}
			if cv.Line != lit.SpreadLine || cv.Col != lit.SpreadCol || cv.Line == 0 {
				t.Errorf("%q: CurrentValue at %d:%d, spread at %d:%d", src, cv.Line, cv.Col, lit.SpreadLine, lit.SpreadCol)
			}
		})
	}
}

func TestParseCurrentValue_Map(t *testing.T) {
	lit, ok := parseExpr(t, `%{.., "city" => "Atlanta"}`).(*ast.MapLit)
	if !ok {
		t.Fatalf("want *ast.MapLit")
	}
	if len(lit.Spreads) != 1 || len(lit.Entries) != 1 {
		t.Fatalf("spreads = %d, entries = %d", len(lit.Spreads), len(lit.Entries))
	}
	if _, ok := lit.Spreads[0].Head.(*ast.CurrentValue); !ok {
		t.Fatalf("spread = %T", lit.Spreads[0].Head)
	}
}

func TestParseCurrentValue_List(t *testing.T) {
	for _, src := range []string{`[t, ..]`, `[a, b, ..]`, "[\n  t,\n  ..\n]", `[.., 1]`, `[..]`} {
		lit, ok := parseExpr(t, src).(*ast.ListSpreadLit)
		if !ok {
			t.Fatalf("%q: want *ast.ListSpreadLit", src)
		}
		if len(lit.Spreads) != 1 {
			t.Fatalf("%q: %d spreads", src, len(lit.Spreads))
		}
		if _, ok := lit.Spreads[0].Head.(*ast.CurrentValue); !ok {
			t.Fatalf("%q: spread = %T", src, lit.Spreads[0].Head)
		}
	}
}

func TestParseSetSpread(t *testing.T) {
	cases := []struct {
		src    string
		spread string
		items  int
	}{
		{`%[.., 3]`, "*ast.CurrentValue", 1},
		{`%[..s, 3, 4]`, "*ast.Ident", 2},
		{`%[..f(x), 3]`, "*ast.Call", 1},
		{"%[\n  // note\n  ..,\n  3,\n]", "*ast.CurrentValue", 1},
		{`%[1, ..s]`, "*ast.Ident", 1},
		{`%[1, 2]`, "", 2},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			lit, ok := parseExpr(t, tc.src).(*ast.SetLit)
			if !ok {
				t.Fatalf("want *ast.SetLit")
			}
			got := ""
			if len(lit.Spreads) > 0 {
				got = typeString(lit.Spreads[0].Head)
				if lit.Spreads[0].Line == 0 {
					t.Errorf("spread position unset")
				}
			}
			if got != tc.spread || len(lit.Items) != tc.items {
				t.Errorf("spread %s with %d items, want %s with %d", got, len(lit.Items), tc.spread, tc.items)
			}
		})
	}
}

func TestParseCurrentValue_OnlyFirstInStruct(t *testing.T) {
	err := parseError(t, `#{a: 1, ..}`)
	if !strings.Contains(err.Error(), "struct spread `..` must be the first element") {
		t.Errorf("%v", err)
	}
}

// A literal takes one bare `..`: it stands for the one value at its
// position.
func TestParseCurrentValue_OnePerLiteral(t *testing.T) {
	for _, tc := range []struct {
		src, want string
		line, col int
	}{
		{`[.., 1, ..]`, "list literal takes one bare `..`", 1, 9},
		{`#[.., 1, ..]`, "vector literal takes one bare `..`", 1, 10},
		{`%[.., ..]`, "set literal takes one bare `..`", 1, 7},
		{`%{.., "a" => 1, ..}`, "map literal takes one bare `..`", 1, 17},
	} {
		err := parseError(t, tc.src)
		want := fmt.Sprintf("line %d, col %d: %s", tc.line, tc.col, tc.want)
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", tc.src, err, want)
		}
	}
}

// A vector takes a bare `..` anywhere and no named spread: `#[.., x]` is
// the old vector then x, `#[x, ..]` is x then the old vector.
func TestParseVectorCurrentValue(t *testing.T) {
	cases := []struct {
		src   string
		at    int
		items int
	}{
		{`#[.., 1]`, 0, 1},
		{`#[1, 2, ..]`, 2, 2},
		{`#[1, .., 2]`, 1, 2},
		{"#[\n  ..,\n  1,\n]", 0, 1},
		{"#[\n  1,\n  ..\n]", 1, 1},
	}
	for _, tc := range cases {
		lit, ok := parseExpr(t, tc.src).(*ast.VectorLit)
		if !ok {
			t.Fatalf("%q: want *ast.VectorLit", tc.src)
		}
		if len(lit.Spreads) != 1 {
			t.Fatalf("%q: %d spreads", tc.src, len(lit.Spreads))
		}
		sp := lit.Spreads[0]
		cv, ok := sp.Head.(*ast.CurrentValue)
		if !ok || sp.At != tc.at || len(lit.Items) != tc.items {
			t.Fatalf("%q: spread %T at=%d items=%d", tc.src, sp.Head, sp.At, len(lit.Items))
		}
		if cv.Line != sp.Line || cv.Col != sp.Col {
			t.Errorf("%q: CurrentValue at %d:%d, spread at %d:%d", tc.src, cv.Line, cv.Col, sp.Line, sp.Col)
		}
	}
}

func TestParseVectorSpreadRefusals(t *testing.T) {
	const named = "vector spread `..` has no named form"
	for _, tc := range []struct {
		src, want string
		line, col int
	}{
		{`#Path[1, ..v]`, named, 1, 10},
		{`#Path[..v, 1]`, named, 1, 7},
	} {
		err := parseError(t, tc.src)
		want := fmt.Sprintf("line %d, col %d: %s", tc.line, tc.col, tc.want)
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", tc.src, err, want)
		}
	}
}

// TestParseVectorNamedSpread: `#[..v, x]` is v then x, `#[x, ..v]` is x
// then v. The spread is any expression.
func TestParseVectorNamedSpread(t *testing.T) {
	cases := []struct {
		src       string
		spread    string
		at        int
		items     int
		line, col int
	}{
		{`#[..names, "Noah"]`, "Ident", 0, 1, 1, 3},
		{`#["Noah", ..names]`, "Ident", 1, 1, 1, 11},
		{`#[1, 2, ..f(x)]`, "Call", 2, 2, 1, 9},
		{`#[..Team.names(t), 1]`, "Call", 0, 1, 1, 3},
		{`#[..#[], 1]`, "VectorLit", 0, 1, 1, 3},
		{`#[..v]`, "Ident", 0, 0, 1, 3},

		{"#[\n  ..names,\n  \"Noah\",\n]", "Ident", 0, 1, 2, 3},
		{"#[\n  \"Noah\",\n  ..names\n]", "Ident", 1, 1, 3, 3},
	}
	for _, tc := range cases {
		lit, ok := parseExpr(t, tc.src).(*ast.VectorLit)
		if !ok {
			t.Fatalf("%q: want *ast.VectorLit", tc.src)
		}
		if len(lit.Spreads) != 1 {
			t.Fatalf("%q: %d spreads", tc.src, len(lit.Spreads))
		}
		sp := lit.Spreads[0]
		if got := sp.Head.NodeType(); got != tc.spread || sp.At != tc.at || len(lit.Items) != tc.items {
			t.Fatalf("%q: spread %s at=%d items=%d, want %s at=%d items=%d", tc.src, got, sp.At, len(lit.Items), tc.spread, tc.at, tc.items)
		}
		if sp.Line != tc.line || sp.Col != tc.col {
			t.Errorf("%q: spread at %d:%d, want %d:%d", tc.src, sp.Line, sp.Col, tc.line, tc.col)
		}
	}
}
