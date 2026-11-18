package analysis

import (
	"slices"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// TestPlaceArgs holds the one routing of a call's arguments to its rules
// (spec §7 *Lambdas at Call Sites*), for a direct call and a pipe stage
// alike.
func TestPlaceArgs(t *testing.T) {
	val := func() ast.Node { return &ast.IntLit{Value: 1} }
	named := func(name string) ast.Node { return &ast.NamedArg{Name: name, Value: val()} }
	name := &ast.Ident{Name: "double"}
	fn := &FuncType{Params: []Type{TypeInt}, Return: TypeInt}
	for _, tc := range []struct {
		what   string
		args   []ast.Node
		piped  int
		names  []string
		params []Type
		want   []int
	}{
		{"a name places its argument wherever it is written",
			[]ast.Node{named("b"), val()}, 0, []string{"a", "b"}, []Type{TypeInt, TypeInt}, []int{1, 0}},
		{"a positional after a named argument skips the slot the name claimed",
			[]ast.Node{val(), named("opts"), &ast.Lambda{}}, 0, []string{"items", "opts", "handler"}, []Type{TypeInt, TypeInt, fn}, []int{0, 1, 2}},
		{"a positional before a named argument means the slot it sits in",
			[]ast.Node{val(), named("a")}, 0, []string{"a", "b"}, []Type{TypeInt, TypeInt}, []int{0, 0}},
		{"a name on the last parameter keeps a trailing lambda in its slot",
			[]ast.Node{val(), &ast.Lambda{}, named("max_running")}, 0, []string{"items", "body", "max_running"}, []Type{TypeInt, fn, TypeInt}, []int{0, 1, 2}},
		{"a name no parameter has is placed nowhere",
			[]ast.Node{named("nope")}, 0, []string{"a"}, []Type{TypeInt}, []int{-1}},
		{"with no parameter names a named argument is placed nowhere and a positional one is",
			[]ast.Node{named("a"), val()}, 0, nil, []Type{TypeInt, TypeInt}, []int{-1, 0}},
		{"a trailing lambda goes to the last parameter",
			[]ast.Node{val(), &ast.Lambda{}}, 0, nil, []Type{TypeInt, TypeInt, TypeInt}, []int{0, 2}},
		{"a parenthesized lambda routes as the lambda does",
			[]ast.Node{val(), &ast.GroupedExpr{Expr: &ast.Lambda{}}}, 0, nil, []Type{TypeInt, TypeInt, TypeInt}, []int{0, 2}},
		{"a field accessor routes as a lambda does",
			[]ast.Node{&ast.FieldAccessor{}}, 0, nil, []Type{TypeInt, TypeInt}, []int{1}},
		{"a function by name routes when the last parameter takes a function and its own slot does not",
			[]ast.Node{val(), name}, 0, nil, []Type{TypeInt, TypeInt, fn}, []int{0, 2}},
		{"a function by name stays where its own slot takes a function",
			[]ast.Node{val(), name}, 0, nil, []Type{TypeInt, fn, fn}, []int{0, 1}},
		{"a function by name stays where the last parameter takes no function",
			[]ast.Node{val(), name}, 0, nil, []Type{TypeInt, TypeInt, TypeInt}, []int{0, 1}},
		{"a block is a value and stays in its slot",
			[]ast.Node{&ast.Block{}}, 0, nil, []Type{TypeInt, TypeInt}, []int{0}},
		{"a pipe stage's arguments start after the piped value",
			[]ast.Node{val(), val()}, 1, nil, []Type{TypeInt, TypeInt, TypeInt}, []int{1, 2}},
		{"a pipe stage's trailing lambda goes to the last parameter",
			[]ast.Node{&ast.Lambda{}}, 1, nil, []Type{TypeInt, TypeInt, fn}, []int{2}},
		{"a pipe stage's named argument fills its parameter",
			[]ast.Node{named("c"), val()}, 1, []string{"a", "b", "c"}, []Type{TypeInt, TypeInt, TypeInt}, []int{2, 1}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			if got := placeArgs(tc.args, tc.piped, tc.names, tc.params); !slices.Equal(got, tc.want) {
				t.Fatalf("placeArgs = %v, want %v", got, tc.want)
			}
		})
	}
}
