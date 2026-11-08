package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// `t.first.second` is two field reads, each at its own name and dot.
func TestTupleOrdinal_ChainParses(t *testing.T) {
	expr := parseExpr(t, "t.second.first.x")
	outer, ok := expr.(*ast.FieldAccess)
	if !ok || outer.Field.Name != "x" {
		t.Fatalf("want .x outermost, got %#v", expr)
	}
	mid, ok := outer.Object.(*ast.FieldAccess)
	if !ok || mid.Field.Name != "first" || mid.Col != 9 || mid.Field.Col != 10 {
		t.Fatalf("want .first at col 9 (name at 10), got %#v", outer.Object)
	}
	inner, ok := mid.Object.(*ast.FieldAccess)
	if !ok || inner.Field.Name != "second" || inner.Col != 2 || inner.Field.Col != 3 {
		t.Fatalf("want .second at col 2 (name at 3), got %#v", mid.Object)
	}
}

// A number after a dot is the error that names the ordinal, and the parse
// goes on as if the ordinal were written: `t.1.0` (the lexer's one Float
// `1.0`) is `t.second.first`, each name at its digits.
func TestTupleOrdinal_NumberIsAnErrorThatRecovers(t *testing.T) {
	nodes, errs, _ := ParseResilient(lexer.Lex("x = t.1.0\n"))
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "write `t.second.first`") {
		t.Fatalf("errors = %v; want the one ordinal error", errs)
	}
	if errs[0].Fix == nil || ApplyEdits("x = t.1.0\n", errs[0].Fix.Edits) != "x = t.second.first\n" {
		t.Fatalf("fix = %+v; want it to write `t.second.first`", errs[0].Fix)
	}
	var reads []string
	ast.Inspect(nodes[0], func(n ast.Node) bool {
		if fa, ok := n.(*ast.FieldAccess); ok {
			reads = append(reads, fa.Field.Name)
		}
		return true
	})
	if strings.Join(reads, " ") != "first second" {
		t.Errorf("recovered reads = %v; want first of second", reads)
	}
}

// Past the last ordinal there is no name to write: the error says so and
// points at a pattern or a struct instead.
func TestTupleOrdinal_PastTheLastIsAnError(t *testing.T) {
	err := parseError(t, "t.20")
	if !strings.Contains(err.Error(), "`.first` to `.twentieth`, and item 21 has none") {
		t.Errorf("Parse(t.20) = %v; want the past-the-last error", err)
	}
	if _, err := Parse(lexer.Lex("t.twentieth")); err != nil {
		t.Errorf("Parse(t.twentieth): %v", err)
	}
}

// A number that is no plain decimal index (`t.01`, `t.1e3`, `t.0x1`) is no
// tuple read at all: the plain field-name error.
func TestTupleOrdinal_OtherNumbersAreFieldNameErrors(t *testing.T) {
	for _, src := range []string{"t.00", "t.01", "t.1_0", "t.0x1", "t.1e3", "t.1.5d"} {
		err := parseError(t, src)
		if !strings.Contains(err.Error(), "expected field name") {
			t.Errorf("Parse(%q) = %v; want the field-name error", src, err)
		}
	}
}

// An accessor reads ordinals as a field access does, `.second.first` as one
// path, and `.1` is the same error with its fix.
func TestTupleOrdinal_Accessor(t *testing.T) {
	call, ok := parseExpr(t, "f(.x.second.first)").(*ast.Call)
	if !ok || len(call.Args) != 1 {
		t.Fatal("want a call with one argument")
	}
	acc, ok := call.Args[0].(*ast.FieldAccessor)
	if !ok || acc.Spelling() != ".x.second.first" {
		t.Fatalf("want the accessor .x.second.first, got %#v", call.Args[0])
	}
	_, errs := ParseAll(lexer.Lex("f(.1)\n"))
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "write `.second`") {
		t.Fatalf("errors = %v; want the ordinal error", errs)
	}
}
