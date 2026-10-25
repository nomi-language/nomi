package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A struct pattern's or literal's fields are separated by commas. A line
// break alone does not separate them, so two fields with no comma between
// them are an error at the second, naming the first.
func TestStructFields_NoCommaIsRejected(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"renamed pattern field", "x = case r {\n    #{err: a b} -> 0\n}\n", "line 2, col 14: expected `,` or `}` after the field `err`"},
		{"punned pattern fields", "x = case p {\n    #Point{x y} -> 0\n}\n", "line 2, col 14: expected `,` or `}` after the field `x`"},
		{"nested pattern field", "x = case p {\n    #Point{x: #(a, b) y} -> 0\n}\n", "line 2, col 23: expected `,` or `}` after the field `x`"},
		{"pattern fields on two lines", "x = case p {\n    #Point{\n        x\n        y\n    } -> 0\n}\n", "line 4, col 9: expected `,` or `}` after the field `x`"},
		{"destructure", "#{a b} = p\n", "line 1, col 5: expected `,` or `}` after the field `a`"},
		{"renamed destructure field", "#{x: c y} = p\n", "line 1, col 8: expected `,` or `}` after the field `x`"},
		{"named literal", "q = #Point{x: 1 y: 2}\n", "line 1, col 17: expected `,` or `}` after the field `x`"},
		{"punned named literal", "q = #Point{x y}\n", "line 1, col 14: expected `,` or `}` after the field `x`"},
		{"named literal on two lines", "q = #Point{\n    x: 1\n    y: 2\n}\n", "line 3, col 5: expected `,` or `}` after the field `x`"},
		{"anonymous literal", "q = #{x: 1 y: 2}\n", "line 1, col 12: expected `,` or `}` after the field `x`"},
	} {
		err := parseError(t, tc.src)
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, err, tc.want)
		}
	}
}

// Commas, a trailing comma, and one field per line with a comma after
// each (the layout `nomi fmt` writes) all parse, in a struct update as in
// a plain literal.
func TestStructFields_CommasAreAccepted(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		fields    int
	}{
		{"pattern", "x = case p {\n    #Point{x, y: b} -> 0\n}\n", 2},
		{"pattern with a trailing comma", "x = case p {\n    #Point{x, y,} -> 0\n}\n", 2},
		{"anonymous pattern", "x = case r {\n    #{err: a, b} -> 0\n}\n", 2},
		{"pattern one field per line", "x = case p {\n    #Point{\n        x,\n        y,\n    } -> 0\n}\n", 2},
		{"pattern with no comma before its brace", "x = case p {\n    #Point{\n        x,\n        y\n    } -> 0\n}\n", 2},
		{"pattern with a comment between fields", "x = case p {\n    #Point{\n        x, // first\n        // then\n        y,\n    } -> 0\n}\n", 2},
		{"destructure", "#{a, b: c} = p\n", 2},
		{"destructure one field per line", "#{\n    a,\n    b,\n} = p\n", 2},
		{"named literal", "q = #Point{x: 1, y}\n", 2},
		{"named literal one field per line", "q = #Point{\n    x: 1,\n    y: 2,\n}\n", 2},
		{"anonymous literal with a trailing comma", "q = #{x: 1, y: 2,}\n", 2},
		{"struct update", "q = #{..base, x: 9, y: 1}\n", 2},
		{"struct update with a trailing comma", "q = #{..base, x: 9,}\n", 1},
		{"struct update stacked", "q = #{\n    ..base,\n    x: 9,\n    y: 1,\n}\n", 2},
		{"bare update", "q = #{a: #{.., x: 9, y: 1}}\n", 1},
	} {
		nodes := parse(t, tc.src)
		got := -1
		for _, n := range nodes {
			ast.Inspect(n, func(n ast.Node) bool {
				if got >= 0 {
					return false
				}
				switch n := n.(type) {
				case *ast.StructPattern:
					got = len(n.Fields)
				case *ast.StructDestructure:
					got = len(n.Fields)
				case *ast.StructLit:
					got = len(n.Fields)
				}
				return got < 0
			})
		}
		if got != tc.fields {
			t.Errorf("%s: got %d fields, want %d", tc.name, got, tc.fields)
		}
	}
}

// A struct or map update separates its spread from the first field or entry
// with a comma, and each field or entry from the next, as a plain literal
// does. The error for a missing comma after the spread names it.
func TestUpdates_NoCommaIsRejected(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"after a named spread", "q = #{..p x: 10}\n", "line 1, col 11: expected `,` or `}` after `..p`"},
		{"after a field path spread", "q = #{..p.home x: 10}\n", "line 1, col 16: expected `,` or `}` after `..p.home`"},
		{"after a call spread", "q = #{..f(p) x: 10}\n", "line 1, col 14: expected `,` or `}` after the spread"},
		{"between fields after a spread", "q = #{..p, x: 10 y: 20}\n", "line 1, col 18: expected `,` or `}` after the field `x`"},
		{"stacked after a spread", "q = #{\n    ..p\n    x: 7\n    y: 8\n}\n", "line 3, col 5: expected `,` or `}` after `..p`"},
		{"stacked between fields", "q = #{\n    ..p,\n    x: 7\n    y: 8\n}\n", "line 4, col 5: expected `,` or `}` after the field `x`"},
		{"stacked after a commented spread", "q = #{\n    ..p // base\n    x: 7,\n}\n", "line 3, col 5: expected `,` or `}` after `..p`"},
		{"after a bare spread", "q = #{a: #{.. city: \"NYC\"}}\n", "line 1, col 15: expected `,` or `}` after `..`"},
		{"between fields after a bare spread", "q = #{a: #{.., city: \"NYC\" zip: \"1\"}}\n", "line 1, col 28: expected `,` or `}` after the field `city`"},
		{"in a Struct.update patch", "q = Struct.update(u, #{address: #{.., city: \"NYC\" zip: \"1\"}})\n", "line 1, col 51: expected `,` or `}` after the field `city`"},
		{"stacked bare spread", "q = #{a: #{\n    ..\n    city: \"NYC\",\n}}\n", "line 3, col 5: expected `,` or `}` after `..`"},
		{"after a map spread", "q = %{..m \"b\" => 2}\n", "line 1, col 11: expected `,` or `}` after `..m`"},
		{"between entries after a map spread", "q = %{..m, \"a\" => 1 \"b\" => 2}\n", "line 1, col 21: expected `,` or `}` after the map entry"},
		{"stacked map update", "q = %{\n    ..m\n    \"b\" => 2,\n}\n", "line 3, col 5: expected `,` or `}` after `..m`"},
		{"map literal", "q = %{\"a\" => 1 \"b\" => 2}\n", "line 1, col 16: expected `,` or `}` after the map entry"},
		{"map literal on two lines", "q = %{\n    \"a\" => 1\n    \"b\" => 2\n}\n", "line 3, col 5: expected `,` or `}` after the map entry"},
		{"map pattern", "x = case m {\n    %{\"a\" => a \"b\" => b} -> 0\n}\n", "line 2, col 16: expected `,` or `}` after the map entry"},
	} {
		err := parseError(t, tc.src)
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, err, tc.want)
		}
	}
}

// Commas after the spread and between entries, with or without a trailing
// comma, parse in a map update, a map literal and a map pattern.
func TestMapEntries_CommasAreAccepted(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		entries   int
	}{
		{"map update", "q = %{..m, \"a\" => 1, \"b\" => 2}\n", 2},
		{"map update with a trailing comma", "q = %{..m, \"a\" => 1,}\n", 1},
		{"map update stacked", "q = %{\n    ..m,\n    \"a\" => 1,\n    \"b\" => 2,\n}\n", 2},
		{"map update with a comment after the spread", "q = %{\n    ..m, // base\n    \"a\" => 1,\n}\n", 1},
		{"map literal stacked", "q = %{\n    \"a\" => 1,\n    \"b\" => 2\n}\n", 2},
		{"map pattern", "x = case m {\n    %{\"a\" => a, \"b\" => b,} -> 0\n}\n", 2},
	} {
		nodes := parse(t, tc.src)
		got := -1
		for _, n := range nodes {
			ast.Inspect(n, func(n ast.Node) bool {
				if got >= 0 {
					return false
				}
				switch n := n.(type) {
				case *ast.MapLit:
					got = len(n.Entries)
				case *ast.MapPattern:
					got = len(n.Entries)
				}
				return got < 0
			})
		}
		if got != tc.entries {
			t.Errorf("%s: got %d entries, want %d", tc.name, got, tc.entries)
		}
	}
}
