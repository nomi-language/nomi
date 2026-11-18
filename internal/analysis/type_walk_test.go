package analysis

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// typeKindsInSource is the name of every type in this package that
// implements Type: each receiver of a typeTag method.
func typeKindsInSource(t *testing.T, fset *token.FileSet, files []*ast.File) []string {
	t.Helper()
	var kinds []string
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != "typeTag" || fd.Recv == nil || len(fd.Recv.List) != 1 {
				continue
			}
			star, ok := fd.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				t.Fatalf("%s: typeTag has a receiver that is not a pointer; typeChildren switches on pointer types", fset.Position(fd.Pos()))
			}
			kinds = append(kinds, star.X.(*ast.Ident).Name)
		}
	}
	sort.Strings(kinds)
	return kinds
}

// switchKinds is the pointer types the cases of fn's first type switch
// name.
func switchKinds(t *testing.T, files []*ast.File, fn string) map[string]bool {
	t.Helper()
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != fn || fd.Recv != nil {
				continue
			}
			named := map[string]bool{}
			found := false
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSwitchStmt)
				if !ok || found {
					return !found
				}
				found = true
				for _, c := range ts.Body.List {
					for _, e := range c.(*ast.CaseClause).List {
						if star, ok := e.(*ast.StarExpr); ok {
							named[star.X.(*ast.Ident).Name] = true
						}
					}
				}
				return false
			})
			if !found {
				t.Fatalf("%s has no type switch", fn)
			}
			return named
		}
	}
	t.Fatalf("no function %s", fn)
	return nil
}

// TestTypeChildrenNamesEveryKind holds typeChildrenIn, withTypeChildren and
// sameTypeShape, the switches every walk over a type's structure and every
// walk over two types in step goes through (type_walk.go), to the kinds of
// Type the package declares: a kind one of them does not name is one no
// walk looks inside, no mapping rebuilds, or no pairing pairs.
func TestTypeChildrenNamesEveryKind(t *testing.T) {
	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, p, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	kinds := typeKindsInSource(t, fset, files)
	if len(kinds) < 15 {
		t.Fatalf("found %d kinds of Type (%v), fewer than the package declares: the scan for typeTag methods is broken", len(kinds), kinds)
	}
	for _, fn := range []string{"typeChildrenIn", "withTypeChildren", "sameTypeShape"} {
		named := switchKinds(t, files, fn)
		for _, k := range kinds {
			if !named[k] {
				t.Errorf("%s has no case for *%s: give it one saying which types a %s is built from, or when two have the same shape, so every walk over a type covers it", fn, k, k)
			}
		}
	}
}

// TestTypeWalkCoversEveryCompoundKind builds one type of every kind that
// holds other types, with a marker type at each position, and requires
// walkType to reach each marker, mapType to replace each, and
// pairedTypeChildren to pair each with the marker at its position in a
// second type of the same shape.
func TestTypeWalkCoversEveryCompoundKind(t *testing.T) {
	mark := func(name string) *TypeParam_ { return &TypeParam_{Name_: name} }
	iface := func(name string, arg Type) *InterfaceType {
		return &InterfaceType{Name: name, TypeArgs: []Type{arg}}
	}
	cases := map[string]struct {
		build func(ms []Type) Type
		n     int
	}{
		"ListType":  {func(m []Type) Type { return &ListType{Elem: m[0]} }, 1},
		"MapType":   {func(m []Type) Type { return &MapType{Key: m[0], Val: m[1]} }, 2},
		"TupleType": {func(m []Type) Type { return &TupleType{Elems: []Type{m[0], m[1]}} }, 2},
		"FuncType":  {func(m []Type) Type { return &FuncType{Params: []Type{m[0], m[1]}, Return: m[2]} }, 3},
		"AnonStructType": {func(m []Type) Type {
			return &AnonStructType{Fields: []FieldDef{{Name: "a", Type: m[0]}, {Name: "b", Type: m[1]}}}
		}, 2},
		"StructType":    {func(m []Type) Type { return &StructType{Name: "S", TypeArgs: []Type{m[0], m[1]}} }, 2},
		"EnumType":      {func(m []Type) Type { return &EnumType{Name: "E", TypeArgs: []Type{m[0]}} }, 1},
		"DistinctType":  {func(m []Type) Type { return &DistinctType{Name: "D", TypeArgs: []Type{m[0]}} }, 1},
		"InterfaceType": {func(m []Type) Type { return &InterfaceType{Name: "I", TypeArgs: []Type{m[0]}} }, 1},
		"ConjunctionType": {func(m []Type) Type {
			return &ConjunctionType{Members: []*InterfaceType{iface("A", m[0]), iface("B", m[1])}}
		}, 2},
		"PartialType": {func(m []Type) Type { return &PartialType{Inner: m[0]} }, 1},
		"TypeVar":     {func(m []Type) Type { return &TypeVar{ID: 1, Resolved: m[0]} }, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			marks := make([]Type, tc.n)
			for i := range marks {
				marks[i] = mark("M" + string(rune('0'+i)))
			}
			ty := tc.build(marks)
			seen := map[Type]bool{}
			walkType(ty, func(p Type) bool { seen[p] = true; return true })
			for _, m := range marks {
				if !seen[m] {
					t.Errorf("walkType over %s did not reach %s", ty, m)
				}
			}
			var replace func(Type) Type
			replace = func(p Type) Type {
				if _, ok := p.(*TypeParam_); ok {
					return TypeInt
				}
				return mapType(p, replace)
			}
			mapped := replace(ty)
			if typeHolds(mapped, func(p Type) bool { _, ok := p.(*TypeParam_); return ok }) {
				t.Errorf("mapType over %s left a marker: %s", ty, mapped)
			}
			if !typeHolds(ty, func(p Type) bool { return p == marks[0] }) {
				t.Errorf("mapType changed its argument %s", ty)
			}
			if same := mapType(ty, func(p Type) Type { return p }); same != ty {
				t.Errorf("mapType with nothing to replace built a new %s", name)
			}
			others := make([]Type, tc.n)
			for i := range others {
				others[i] = mark("N" + string(rune('0'+i)))
			}
			paired := map[*TypeParam_]Type{}
			matchTypeParams(ty, tc.build(others), paired)
			for i, m := range marks {
				if paired[m.(*TypeParam_)] != others[i] {
					t.Errorf("pairedTypeChildren over two of %s did not pair position %d", ty, i)
				}
			}
		})
	}
	// The kinds with no types inside them, so the table above and
	// TestTypeChildrenNamesEveryKind's list account for every kind.
	leaves := []string{"PrimitiveType", "TypeParam_", "AliasTemplate"}
	fset := token.NewFileSet()
	var files []*ast.File
	paths, _ := filepath.Glob("*.go")
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	for _, k := range typeKindsInSource(t, fset, files) {
		if _, ok := cases[k]; ok {
			continue
		}
		leaf := false
		for _, l := range leaves {
			leaf = leaf || l == k
		}
		if !leaf {
			t.Errorf("no case here builds a %s: add one with a marker at each position it holds a type, or list it as a leaf", k)
		}
	}
}

// TestPairedTypeChildren holds what pairedTypeChildren answers beyond
// position: an anonymous struct's fields pair by name, a function type
// with no result pairs with one that writes Unit, and two types of
// different shapes pair nothing.
func TestPairedTypeChildren(t *testing.T) {
	anon := func(fields ...FieldDef) Type { return &AnonStructType{Fields: fields} }
	as, bs, same := pairedTypeChildren(
		anon(FieldDef{Name: "a", Type: TypeInt}, FieldDef{Name: "b", Type: TypeString}),
		anon(FieldDef{Name: "b", Type: TypeBool}, FieldDef{Name: "a", Type: TypeFloat}))
	if !same || len(as) != 2 || as[0] != TypeInt || bs[0] != TypeFloat || as[1] != TypeString || bs[1] != TypeBool {
		t.Errorf("anonymous structs with the same field names in another order: as=%v bs=%v same=%v", as, bs, same)
	}
	as, bs, same = pairedTypeChildren(&FuncType{Params: []Type{TypeInt}}, &FuncType{Params: []Type{TypeString}, Return: TypeUnit})
	if !same || len(as) != 2 || as[1] != TypeUnit || bs[1] != TypeUnit {
		t.Errorf("a function with no result beside one returning Unit: as=%v bs=%v same=%v", as, bs, same)
	}
	solved := &TypeVar{ID: 1, Resolved: &ListType{Elem: TypeInt}}
	if as, bs, same = pairedTypeChildren(solved, &ListType{Elem: TypeString}); !same || as[0] != TypeInt || bs[0] != TypeString {
		t.Errorf("a solved variable beside a list: as=%v bs=%v same=%v", as, bs, same)
	}
	for name, pair := range map[string][2]Type{
		"a list and a tuple":               {&ListType{Elem: TypeInt}, &TupleType{Elems: []Type{TypeInt}}},
		"tuples of different lengths":      {&TupleType{Elems: []Type{TypeInt}}, &TupleType{Elems: []Type{TypeInt, TypeInt}}},
		"anonymous structs, another field": {anon(FieldDef{Name: "a", Type: TypeInt}), anon(FieldDef{Name: "b", Type: TypeInt})},
		"functions of different arity":     {&FuncType{Params: []Type{TypeInt}}, &FuncType{}},
		"two declarations":                 {&StructType{Name: "S", Origin: "a", TypeArgs: []Type{TypeInt}}, &StructType{Name: "S", Origin: "b", TypeArgs: []Type{TypeInt}}},
		"two primitives":                   {TypeInt, TypeString},
		"two unsolved variables":           {&TypeVar{ID: 1}, &TypeVar{ID: 2}},
		"nothing":                          {nil, TypeInt},
	} {
		if as, bs, same := pairedTypeChildren(pair[0], pair[1]); same || as != nil || bs != nil {
			t.Errorf("%s have the same shape: as=%v bs=%v", name, as, bs)
		}
	}
	if _, _, same := pairedTypeChildren(TypeInt, TypeInt); !same {
		t.Errorf("a primitive does not have its own shape")
	}
}

// ReplaceTypeParams puts a type for each type parameter at every depth,
// shares a type that holds none, and says when one had no answer.
func TestTypeWalk_ReplaceTypeParams(t *testing.T) {
	tp, other := &TypeParam_{Name_: "T"}, &TypeParam_{Name_: "U"}
	by := func(p *TypeParam_) (Type, bool) {
		if p.Name_ == "T" {
			return TypeInt, true
		}
		return nil, false
	}
	in := &FuncType{Params: []Type{&ListType{Elem: tp}}, Return: &MapType{Key: TypeString, Val: tp}}
	want := &FuncType{Params: []Type{&ListType{Elem: TypeInt}}, Return: &MapType{Key: TypeString, Val: TypeInt}}
	out, all := ReplaceTypeParams(in, by)
	if !all || out.String() != want.String() {
		t.Errorf("ReplaceTypeParams(%s) = %s, %v; want %s", in, out, all, want)
	}
	if in.Params[0].(*ListType).Elem != Type(tp) {
		t.Errorf("ReplaceTypeParams changed the type it was given")
	}
	closed := &ListType{Elem: TypeString}
	if out, all := ReplaceTypeParams(closed, by); out != Type(closed) || !all {
		t.Errorf("a type with no type parameter is not itself: %s, %v", out, all)
	}
	if _, all := ReplaceTypeParams(&TupleType{Elems: []Type{tp, other}}, by); all {
		t.Errorf("a type parameter with no answer is reported as replaced")
	}
}
