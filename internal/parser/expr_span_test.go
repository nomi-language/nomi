package parser

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// spanNodes calls fn for every node under v that carries a span.
func spanNodes(v reflect.Value, fn func(ast.Node, ast.Span), seen map[uintptr]bool) {
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			spanNodes(v.Elem(), fn, seen)
		}
	case reflect.Ptr:
		if v.IsNil() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		if n, ok := v.Interface().(ast.Node); ok {
			if hs, ok := n.(ast.HasSpan); ok {
				fn(n, hs.GetSpan())
			}
		}
		spanNodes(v.Elem(), fn, seen)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				spanNodes(v.Field(i), fn, seen)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			spanNodes(v.Index(i), fn, seen)
		}
	}
}

// exprSpanText is the source text a span covers.
func exprSpanText(lines []string, sp ast.Span) string {
	if sp.StartLine < 1 || sp.EndLine > len(lines) || sp.StartLine > sp.EndLine {
		return ""
	}
	if sp.StartLine == sp.EndLine {
		l := lines[sp.StartLine-1]
		if sp.StartCol < 1 || sp.EndCol-1 > len(l) || sp.EndCol < sp.StartCol {
			return ""
		}
		return l[sp.StartCol-1 : sp.EndCol-1]
	}
	var b strings.Builder
	b.WriteString(lines[sp.StartLine-1][sp.StartCol-1:])
	for i := sp.StartLine; i < sp.EndLine-1; i++ {
		b.WriteString("\n" + lines[i])
	}
	b.WriteString("\n" + lines[sp.EndLine-1][:sp.EndCol-1])
	return b.String()
}

func TestExprSpans_CoverTheExpression(t *testing.T) {
	src := `fn f(xs: List<Int>, p: Point): Maybe<(Int) -> Int> {
  total = p.x + g(1, 2) * 3
  s = Point{x: 1, y: 2}
  h = |n| n + 1
  m = "a\tb"
  g = "hi ${p.x}!"
  r = xs |> List.length()
  case p {
    Point{x: 0, y} -> y
    _ -> 0
  }
}
`
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(src, "\n")
	got := map[string]bool{}
	spanNodes(reflect.ValueOf(nodes), func(n ast.Node, sp ast.Span) {
		if !sp.IsZero() {
			got[n.NodeType()+" "+exprSpanText(lines, sp)] = true
		}
	}, map[uintptr]bool{})
	for _, want := range []string{
		"Binary p.x + g(1, 2) * 3",
		"FieldAccess p.x",
		"Binary g(1, 2) * 3",
		"Call g(1, 2)",
		"IntLit 3",
		"StructLit Point{x: 1, y: 2}",
		"Lambda |n| n + 1",
		`StringLit "a\tb"`,
		`StringInterp "hi ${p.x}!"`,
		"Binary n + 1",
		"Binary xs |> List.length()",
		"Call List.length()",
		"GenericType List<Int>",
		"GenericType Maybe<(Int) -> Int>",
		"FuncType (Int) -> Int",
		"Binding total = p.x + g(1, 2) * 3",
	} {
		if !got[want] {
			keys := make([]string, 0, len(got))
			for k := range got {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			t.Errorf("no span %q; have:\n%s", want, strings.Join(keys, "\n"))
		}
	}
}

// Every expression the parser builds from source carries its extent.
func TestExprSpans_EveryParsedExpressionHasOne(t *testing.T) {
	exprs := map[string]bool{
		"Binary": true, "Call": true, "FieldAccess": true, "StructLit": true, "Lambda": true,
		"IntLit": true, "StringLit": true, "ListLit": true, "TupleLit": true, "If": true,
		"Case": true, "Unary": true, "TryOp": true, "RangeLit": true, "MapLit": true,
	}
	missing := map[string]int{}
	total := 0
	for _, root := range []string{"../../tests", "../../std"} {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".nomi") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			nodes, err := Parse(lexer.Lex(string(src)))
			if err != nil {
				return nil
			}
			spanNodes(reflect.ValueOf(nodes), func(n ast.Node, sp ast.Span) {
				if !exprs[n.NodeType()] || n.LineNum() == 0 {
					return
				}
				total++
				if sp.IsZero() {
					missing[n.NodeType()]++
				}
			}, map[uintptr]bool{})
			return nil
		})
	}
	if total == 0 {
		t.Fatal("no expressions parsed")
	}
	if len(missing) > 0 {
		t.Errorf("expressions with no span, by kind: %v (of %d)", missing, total)
	}
}
