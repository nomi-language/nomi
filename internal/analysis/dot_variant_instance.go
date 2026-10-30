package analysis

import "github.com/nomi-language/nomi/internal/ast"

// resolveDotVariantTypes re-records the type of each leading-dot variant
// under value at the instance a generic construction solved.
//
// A value checked against a slot of a generic declaration is checked before
// the construction has solved its parameters: `.Leaf` in `#Tree.Node{left:
// .Leaf, value: 4}` is checked against `left: Tree<T>`, and `pair(.Leaf, 4)`
// against `t: Tree<T>`. The dot takes the enum its position expects, so its
// recorded type is `Tree<T>` with the declaration's own T, and the IR builder
// finds no instance to build it at. Once the construction's values have
// solved T, the dot's type is that instance, `Tree<Int>`. A parameter subs
// does not solve is left as it was.
//
// It walks the whole value, since a dot may sit inside a tuple, list or
// record the slot's type reaches through (`Pos.Node(#(.Leaf, 4))`). A nested
// construction re-records its own dots first, so only a type that still
// holds one of this construction's parameters changes.
func (c *checker) resolveDotVariantTypes(value ast.Node, subs map[*TypeParam_]Type) {
	if value == nil || len(subs) == 0 || c.fa == nil {
		return
	}
	ast.Inspect(value, func(n ast.Node) bool {
		dv, ok := n.(*ast.DotVariant)
		if !ok {
			return true
		}
		if ty := c.fa.ExprTypes[dv]; ty != nil && ContainsTypeParam(ty) {
			c.fa.ExprTypes[dv] = Substitute(ty, subs)
		}
		return true
	})
}
