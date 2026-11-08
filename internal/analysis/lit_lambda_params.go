package analysis

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
)

// Lambda parameter types shared across a collection literal's elements.
//
// A collection literal written with no expected type (a list, vector or set,
// or a map's keys or values) has elements of one type. When the elements hold
// lambdas at the same place, `[("a", fn d: Date -> f(d)), ("b", fn d ->
// g(d))]`, the parameter one of them annotates is the type of the others'
// unannotated parameter at that place: each element is a `(String, (Date) ->
// String)` either way, and writing `: Date` thirteen times says nothing the
// first did not.
//
// A place is a path from the element down through tuple positions, record
// fields and the elements, keys and values of nested collection literals,
// to a lambda. Before any element is checked, every annotated parameter at a
// place gives its type to that place's parameter of the same index; the first
// annotation in the literal wins. Then, as each element is checked, the
// elements' joined type gives the places still open a type from an earlier
// element that is not a lambda at all (`("a", month_name)`) or whose
// parameter a default typed. checkLambda reads the result
// (checker.litLambdaParams) where it would report `cannot infer type for
// parameter`. Elements that annotate different types at one place are not
// reconciled: the first annotation types the unannotated ones, and the
// literal's element types then disagree, which is the element type mismatch.
//
// A parameter nothing types this way is still an error. A later use of the
// literal never types one: the parameter's type has to be known to check the
// lambda's body, which is checked where the lambda is written.

// litLambdaSeeds is one literal's places and the lambdas waiting at them.
type litLambdaSeeds struct {
	c *checker
	// types holds, per place, the parameter types fixed so far, by index.
	// Each waiting lambda's checker.litLambdaParams entry is this slice, so
	// a type learned here reaches every lambda still to be checked.
	types map[string][]Type
	// waiting are the lambdas with an unannotated parameter, by place.
	waiting []litLambdaAt
	// added are the lambdas this literal entered in checker.litLambdaParams.
	added []*ast.Lambda
}

type litLambdaAt struct {
	root string
	path []string
	lam  *ast.Lambda
}

// litLambdaRoot is one position of a literal's elements: "" for a list,
// vector or set's items, "k" and "v" for a map's keys and values.
type litLambdaRoot struct {
	root  string
	items []ast.Node
}

// seedLitLambdas finds the lambdas in a literal's elements and gives each
// unannotated parameter the type an annotation at its place fixes. done
// removes what it entered once the literal is checked.
func (c *checker) seedLitLambdas(roots ...litLambdaRoot) *litLambdaSeeds {
	s := &litLambdaSeeds{c: c}
	var all []litLambdaAt
	for _, r := range roots {
		for _, item := range r.items {
			collectLitLambdas(item, r.root, nil, &all)
		}
	}
	if len(all) == 0 {
		return s
	}
	s.types = map[string][]Type{}
	width := map[string]int{}
	for _, at := range all {
		key := at.key()
		if len(at.lam.Params) > width[key] {
			width[key] = len(at.lam.Params)
		}
	}
	for key, w := range width {
		s.types[key] = make([]Type, w)
	}
	for _, at := range all {
		slots := s.types[at.key()]
		open := false
		for i, p := range at.lam.Params {
			if p.Destructure != nil {
				continue
			}
			if p.TypeAnnotation == nil {
				if p.Default == nil {
					open = true
				}
				continue
			}
			if slots[i] != nil {
				continue
			}
			// The lambda's own check resolves the annotation again and
			// reports what is wrong with it; nil records no references.
			if ty, err := ResolveTypeExpr(p.TypeAnnotation, c.reg, c.fnTypeParams, nil); err == nil && ty != nil {
				slots[i] = ty
			}
		}
		if open {
			s.waiting = append(s.waiting, at)
		}
	}
	for _, at := range s.waiting {
		// A lambda an enclosing literal already placed keeps that place: a
		// nested literal is checked inside its parent's element.
		if _, ok := c.litLambdaParams[at.lam]; ok {
			continue
		}
		if c.litLambdaParams == nil {
			c.litLambdaParams = map[*ast.Lambda][]Type{}
		}
		c.litLambdaParams[at.lam] = s.types[at.key()]
		s.added = append(s.added, at.lam)
	}
	return s
}

// learn takes from ty, the joined type of the elements at root checked so
// far, the parameter types of the places still open.
func (s *litLambdaSeeds) learn(root string, ty Type) {
	if ty == nil {
		return
	}
	for _, at := range s.waiting {
		if at.root != root {
			continue
		}
		ft, ok := resolveTypeVar(litTypeAt(ty, at.path)).(*FuncType)
		if !ok {
			continue
		}
		slots := s.types[at.key()]
		for i := range slots {
			if slots[i] != nil || i >= len(ft.Params) {
				continue
			}
			if p := ft.Params[i]; p != nil && !containsTypeVar(p) {
				slots[i] = p
			}
		}
	}
}

// done removes the entries this literal added.
func (s *litLambdaSeeds) done() {
	for _, lam := range s.added {
		delete(s.c.litLambdaParams, lam)
	}
}

func (at litLambdaAt) key() string {
	key := at.root
	for _, p := range at.path {
		key += "/" + p
	}
	return key
}

// collectLitLambdas appends each lambda in n, a literal's element, with its
// place. It looks through parentheses, tuples, records and nested anonymous
// collection literals; a lambda's body is not searched.
func collectLitLambdas(n ast.Node, root string, path []string, out *[]litLambdaAt) {
	at := func(seg string) []string {
		return append(append([]string(nil), path...), seg)
	}
	switch v := ungroupExpr(n).(type) {
	case *ast.Lambda:
		*out = append(*out, litLambdaAt{root: root, path: path, lam: v})
	case *ast.TupleLit:
		for i, item := range v.Items {
			collectLitLambdas(item, root, at("t"+strconv.Itoa(i)), out)
		}
	case *ast.StructLit:
		if v.TypeName != nil || v.Spread != nil {
			return
		}
		for _, f := range v.Fields {
			collectLitLambdas(f.Value, root, at("f"+f.Name), out)
		}
	case *ast.ListLit:
		if v.TypeName != nil {
			return
		}
		for _, item := range v.Items {
			collectLitLambdas(item, root, at("e"), out)
		}
	case *ast.VectorLit:
		if v.TypeName != nil || len(v.Spreads) > 0 {
			return
		}
		for _, item := range v.Items {
			collectLitLambdas(item, root, at("e"), out)
		}
	case *ast.SetLit:
		if v.TypeName != nil || len(v.Spreads) > 0 {
			return
		}
		for _, item := range v.Items {
			collectLitLambdas(item, root, at("e"), out)
		}
	case *ast.MapLit:
		if v.TypeName != nil || len(v.Spreads) > 0 {
			return
		}
		for _, e := range v.Entries {
			collectLitLambdas(e.Key, root, at("k"), out)
			collectLitLambdas(e.Value, root, at("v"), out)
		}
	}
}

// litTypeAt is the type at path inside ty, a literal element's type, or nil.
func litTypeAt(ty Type, path []string) Type {
	for _, seg := range path {
		ty = resolveTypeVar(ty)
		if ty == nil {
			return nil
		}
		switch seg[0] {
		case 't':
			tt, ok := ty.(*TupleType)
			i, err := strconv.Atoi(seg[1:])
			if !ok || err != nil || i >= len(tt.Elems) {
				return nil
			}
			ty = tt.Elems[i]
		case 'f':
			st, ok := ty.(*AnonStructType)
			if !ok {
				return nil
			}
			var next Type
			for _, f := range st.Fields {
				if f.Name == seg[1:] {
					next = f.Type
				}
			}
			ty = next
		case 'e':
			if lt, ok := ty.(*ListType); ok {
				ty = lt.Elem
			} else if e, ok := vectorElemType(ty); ok {
				ty = e
			} else if e, ok := setElemType(ty); ok {
				ty = e
			} else {
				return nil
			}
		case 'k', 'v':
			mt, ok := ty.(*MapType)
			if !ok {
				return nil
			}
			if seg == "k" {
				ty = mt.Key
			} else {
				ty = mt.Val
			}
		default:
			return nil
		}
	}
	return ty
}

// litLambdaParam is the type a collection literal's other elements give
// parameter i of n, or nil.
func (c *checker) litLambdaParam(n *ast.Lambda, i int) Type {
	slots := c.litLambdaParams[n]
	if i >= len(slots) {
		return nil
	}
	return slots[i]
}
