package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

func TestFieldAccessor_Parses(t *testing.T) {
	cases := []struct {
		src  string
		path []string
	}{
		{".name", []string{"name"}},
		{".address.city", []string{"address", "city"}},
		{".0", []string{"0"}},
		{".pair.1", []string{"pair", "1"}},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			acc, ok := parseExpr(t, tc.src).(*ast.FieldAccessor)
			if !ok {
				t.Fatalf("Parse(%q) = %T, want *ast.FieldAccessor", tc.src, parseExpr(t, tc.src))
			}
			if acc.Line != 1 || acc.Col != 1 {
				t.Errorf("accessor at %d:%d, want the leading dot at 1:1", acc.Line, acc.Col)
			}
			if len(acc.Path) != len(tc.path) {
				t.Fatalf("path = %d segments, want %v", len(acc.Path), tc.path)
			}
			col := 2
			for i, seg := range acc.Path {
				if seg.Name != tc.path[i] {
					t.Errorf("segment %d = %q, want %q", i, seg.Name, tc.path[i])
				}
				if seg.Line != 1 || seg.Col != col {
					t.Errorf("segment %q at %d:%d, want 1:%d", seg.Name, seg.Line, seg.Col, col)
				}
				col += len(seg.Name) + 1
			}
			if got := acc.Spelling(); got != tc.src {
				t.Errorf("Spelling() = %q, want %q", got, tc.src)
			}
		})
	}
}

// A capitalized name after the dot is still a `.Variant`.
func TestFieldAccessor_CapitalIsAVariant(t *testing.T) {
	if _, ok := parseExpr(t, ".Red").(*ast.DotVariant); !ok {
		t.Fatalf("`.Red` is not a DotVariant")
	}
}

func TestFieldAccessor_AsAnArgument(t *testing.T) {
	call, ok := parseExpr(t, "Iter.map(users, .address.city)").(*ast.Call)
	if !ok || len(call.Args) != 2 {
		t.Fatalf("not a two-argument call")
	}
	acc, ok := call.Args[1].(*ast.FieldAccessor)
	if !ok || acc.Spelling() != ".address.city" {
		t.Fatalf("second argument = %#v, want the accessor `.address.city`", call.Args[1])
	}
}
