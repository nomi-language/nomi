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
	if _, ok := lit.Spread.(*ast.CurrentValue); !ok || len(lit.Entries) != 1 {
		t.Fatalf("spread = %T, entries = %d", lit.Spread, len(lit.Entries))
	}
}

func TestParseCurrentValue_List(t *testing.T) {
	for _, src := range []string{`[t, ..]`, `[a, b, ..]`, "[\n  t,\n  ..\n]"} {
		lit, ok := parseExpr(t, src).(*ast.ListSpreadLit)
		if !ok {
			t.Fatalf("%q: want *ast.ListSpreadLit", src)
		}
		if _, ok := lit.TailSpread.(*ast.CurrentValue); !ok {
			t.Fatalf("%q: tail = %T", src, lit.TailSpread)
		}
	}
	// `[..]` is a list of nothing but the current value.
	lit := parseExpr(t, `[..]`).(*ast.ListSpreadLit)
	if _, ok := lit.TailSpread.(*ast.CurrentValue); !ok || len(lit.Heads) != 0 {
		t.Fatalf("[..]: tail = %T, heads = %d", lit.TailSpread, len(lit.Heads))
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
		{`%[1, 2]`, "", 2},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			lit, ok := parseExpr(t, tc.src).(*ast.SetLit)
			if !ok {
				t.Fatalf("want *ast.SetLit")
			}
			got := ""
			if lit.Spread != nil {
				got = typeString(lit.Spread)
				if lit.SpreadLine == 0 {
					t.Errorf("spread position unset")
				}
			}
			if got != tc.spread || len(lit.Items) != tc.items {
				t.Errorf("spread %s with %d items, want %s with %d", got, len(lit.Items), tc.spread, tc.items)
			}
		})
	}
}

func TestParseSetSpread_MustBeFirst(t *testing.T) {
	for _, src := range []string{`%[1, ..s]`, `%[1, ..]`, `%[..a, ..b]`} {
		err := parseError(t, src)
		if !strings.Contains(err.Error(), "set spread `..` must be the first element") {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestParseCurrentValue_OnlyFirstInStruct(t *testing.T) {
	err := parseError(t, `#{a: 1, ..}`)
	if !strings.Contains(err.Error(), "struct spread `..` must be the first element") {
		t.Errorf("%v", err)
	}
	err = parseError(t, `[.., 1]`)
	if !strings.Contains(err.Error(), "list spread `..` must be the last element") {
		t.Errorf("%v", err)
	}
}

// A vector takes a bare `..` at either end and no named spread: `#[.., x]`
// is the old vector then x, `#[x, ..]` is x then the old vector.
func TestParseVectorCurrentValue(t *testing.T) {
	cases := []struct {
		src   string
		last  bool
		items int
	}{
		{`#[.., 1]`, false, 1},
		{`#[1, 2, ..]`, true, 2},
		{"#[\n  ..,\n  1,\n]", false, 1},
		{"#[\n  1,\n  ..\n]", true, 1},
	}
	for _, tc := range cases {
		lit, ok := parseExpr(t, tc.src).(*ast.VectorLit)
		if !ok {
			t.Fatalf("%q: want *ast.VectorLit", tc.src)
		}
		cv, ok := lit.Spread.(*ast.CurrentValue)
		if !ok || lit.SpreadLast != tc.last || len(lit.Items) != tc.items {
			t.Fatalf("%q: spread %T last=%v items=%d", tc.src, lit.Spread, lit.SpreadLast, len(lit.Items))
		}
		if cv.Line != lit.SpreadLine || cv.Col != lit.SpreadCol {
			t.Errorf("%q: CurrentValue at %d:%d, spread at %d:%d", tc.src, cv.Line, cv.Col, lit.SpreadLine, lit.SpreadCol)
		}
	}
}

func TestParseVectorSpreadRefusals(t *testing.T) {
	const middle = "vector spread `..` must be the first or the last element"
	const two = "a vector literal takes one spread `..`; join two vectors with `Vector.concat`"
	for _, tc := range []struct {
		src, want string
		line, col int
	}{
		{`#[1, .., 2]`, middle, 1, 6},
		{`#[1, ..v, 2]`, middle, 1, 6},
		{`#[1, ..f(x), 2]`, middle, 1, 6},
		{`#[.., 1, ..]`, two, 1, 10},
		{`#[..a, ..b]`, two, 1, 8},
		{`#[..a, 1, ..b]`, two, 1, 11},
		{"#[\n  ..a,\n  ..b,\n]", two, 3, 3},
	} {
		err := parseError(t, tc.src)
		want := fmt.Sprintf("line %d, col %d: %s", tc.line, tc.col, tc.want)
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", tc.src, err, want)
		}
	}
}

// TestParseVectorNamedSpread: `#[..v, x]` is v then x, `#[x, ..v]` is x
// then v. The spread is any expression, at either end.
func TestParseVectorNamedSpread(t *testing.T) {
	cases := []struct {
		src       string
		spread    string
		last      bool
		items     int
		line, col int
	}{
		{`#[..names, "Noah"]`, "Ident", false, 1, 1, 3},
		{`#["Noah", ..names]`, "Ident", true, 1, 1, 11},
		{`#[1, 2, ..f(x)]`, "Call", true, 2, 1, 9},
		{`#[..Team.names(t), 1]`, "Call", false, 1, 1, 3},
		{`#[..#[], 1]`, "VectorLit", false, 1, 1, 3},
		{`#[..v]`, "Ident", false, 0, 1, 3},

		{"#[\n  ..names,\n  \"Noah\",\n]", "Ident", false, 1, 2, 3},
		{"#[\n  \"Noah\",\n  ..names\n]", "Ident", true, 1, 3, 3},
	}
	for _, tc := range cases {
		lit, ok := parseExpr(t, tc.src).(*ast.VectorLit)
		if !ok {
			t.Fatalf("%q: want *ast.VectorLit", tc.src)
		}
		if got := lit.Spread.NodeType(); got != tc.spread || lit.SpreadLast != tc.last || len(lit.Items) != tc.items {
			t.Fatalf("%q: spread %s last=%v items=%d, want %s last=%v items=%d", tc.src, got, lit.SpreadLast, len(lit.Items), tc.spread, tc.last, tc.items)
		}
		if lit.SpreadLine != tc.line || lit.SpreadCol != tc.col {
			t.Errorf("%q: spread at %d:%d, want %d:%d", tc.src, lit.SpreadLine, lit.SpreadCol, tc.line, tc.col)
		}
	}
}
