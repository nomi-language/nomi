package analysis

import "github.com/nomi-language/nomi/internal/ast"

// resolveDotVariantTypes re-records the type of each leading-dot variant
// under value, and of each value built around one, at the instance a
// generic construction solved.
//
// A value checked against a slot of a generic declaration is checked before
// the construction has solved its parameters: `.Leaf` in `Tree.Node{left:
// .Leaf, value: 4}` is checked against `left: Tree<T>`, and `pair(.Leaf, 4)`
// against `t: Tree<T>`. The dot takes the enum its position expects, so its
// recorded type is `Tree<T>` with the declaration's own T, and the IR builder
// finds no instance to build it at. Once the construction's values have
// solved T, the dot's type is that instance, `Tree<Int>`. A parameter subs
// does not solve is left as it was.
//
// It walks the whole value, since a dot may sit inside a tuple, list or
// record the slot's type reaches through (`Pos.Node((.Leaf, 4))`). A nested
// construction re-records its own dots first, so only a type that still
// holds one of this construction's parameters changes.
func (c *checker) resolveDotVariantTypes(value ast.Node, subs map[*TypeParam_]Type) {
	if value == nil || len(subs) == 0 || c.fa == nil {
		return
	}
	ast.Inspect(value, func(n ast.Node) bool {
		// The dot's own record, and each value around it that took its
		// type from the dot's (`(.Leaf, 4)`'s `(Tree<T>, Int)`).
		if n == nil {
			return true
		}
		if ty := c.fa.ExprTypes[n]; ty != nil {
			if full := resolveSolvedVars(ty); ContainsTypeParam(full) {
				c.fa.ExprTypes[n] = Substitute(full, subs)
			}
		}
		// A nested call checked against the slot (`Err("x")` against
		// `Result<Result<T, E>, E>`) recorded its instance in the
		// construction's parameters too.
		if call, ok := n.(*ast.Call); ok {
			if pos, named := calleeKey(call.Func); named {
				if ref := c.fa.References[pos]; ref != nil && ref.CallType != nil {
					if full := resolveSolvedVars(ref.CallType); ContainsTypeParam(full) {
						ct := Substitute(full, subs)
						proxy := *ref
						proxy.CallType = ct
						c.fa.References[pos] = &proxy
					}
				}
			}
		}
		return true
	})
}
