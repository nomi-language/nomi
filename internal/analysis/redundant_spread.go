package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/parser"
)

// A nested update spells the value its field already holds one way: a bare
// `..`. In
//
//	.{..ada, address: .{..ada.address, city: "NYC"}}
//
// the spread `..ada.address` names the value `address` already holds, so it
// is an error whose fix writes `{.., city: "NYC"}`. The check compares
// resolved paths: the path of the value an update updates (updatePaths) is
// its base's when the base is a binding or a chain of field reads of one
// (namePathOf), the original's in a `Struct.update` call with a literal
// patch, or, for a bare `..`, the enclosing update's path and the field it
// stands at (currentPaths). A nested spread naming that path again is
// redundant at any depth. A base of any other shape, a call included, has
// no path: a bare `..` in place of `..load().address` would not run the
// call a second time, so the two do not mean the same.

// RedundantSpreadCode tags the error for a nested update that spells the
// value its field already holds as a path.
const RedundantSpreadCode = "redundant-nested-spread"

// A namePath is a value named by a binding and a chain of field reads,
// `ada.address.geo`: the binding's resolved symbol and the field names.
// Two paths with the same symbol and fields read the same value, since
// values are immutable.
type namePath struct {
	root   *Symbol
	fields []string
}

func (p namePath) child(field string) namePath {
	fields := make([]string, len(p.fields), len(p.fields)+1)
	copy(fields, p.fields)
	return namePath{root: p.root, fields: append(fields, field)}
}

func (p namePath) equal(q namePath) bool {
	if p.root != q.root || len(p.fields) != len(q.fields) {
		return false
	}
	for i := range p.fields {
		if p.fields[i] != q.fields[i] {
			return false
		}
	}
	return true
}

// namePathOf is the path n names when it is a binding or a chain of field
// reads of one (`ada`, `ada.address`).
func (c *checker) namePathOf(n ast.Node) (namePath, bool) {
	switch n := n.(type) {
	case *ast.Ident:
		sym := c.fa.References[Pos{Line: n.Line, Col: n.Col}]
		if sym == nil {
			return namePath{}, false
		}
		return namePath{root: sym}, true
	case *ast.FieldAccess:
		if n.Field == nil {
			return namePath{}, false
		}
		p, ok := c.namePathOf(n.Object)
		if !ok {
			return namePath{}, false
		}
		return p.child(n.Field.Name), true
	}
	return namePath{}, false
}

// recordUpdatePath records the path of the value the update lit updates,
// whose base is base: the base's own path, or, for a bare `..`, the path
// the enclosing update gave it.
func (c *checker) recordUpdatePath(lit *ast.StructLit, base ast.Node) {
	var p namePath
	var ok bool
	if cv, bare := base.(*ast.CurrentValue); bare {
		p, ok = c.currentPaths[cv]
	} else {
		p, ok = c.namePathOf(base)
	}
	if !ok {
		return
	}
	if c.updatePaths == nil {
		c.updatePaths = map[*ast.StructLit]namePath{}
	}
	c.updatePaths[lit] = p
}

// checkRedundantSpread checks the value of field f of the update lit. A
// nested update there whose spread is the path of the value the field
// already holds is an error whose fix writes `..`. A bare `..` there is
// given that path, so the check reaches the updates nested inside it.
func (c *checker) checkRedundantSpread(lit *ast.StructLit, f ast.StructFieldVal) {
	base, ok := c.updatePaths[lit]
	if !ok {
		return
	}
	nested, ok := f.Value.(*ast.StructLit)
	if !ok || nested.TypeName != nil || nested.Spread == nil {
		return
	}
	want := base.child(f.Name)
	if cv, bare := nested.Spread.(*ast.CurrentValue); bare {
		if c.currentPaths == nil {
			c.currentPaths = map[*ast.CurrentValue]namePath{}
		}
		c.currentPaths[cv] = want
		return
	}
	got, ok := c.namePathOf(nested.Spread)
	if !ok || !got.equal(want) {
		return
	}
	line, col := nested.SpreadLine, nested.SpreadCol
	e := TypeError{
		Line:    line,
		Col:     col,
		Code:    RedundantSpreadCode,
		Message: fmt.Sprintf("`..%s` is the value `%s` already holds here: write `..`", format.RenderNode(nested.Spread), f.Name),
	}
	if endLine, endCol := pathEnd(nested.Spread); endLine == line && endCol > col+2 {
		e.EndLine, e.EndCol = endLine, endCol
		e.Fix = &parser.Fix{
			Title: "Write `..`",
			Edits: []parser.Edit{{
				Start: parser.Pos{Line: line, Col: col + 2},
				End:   parser.Pos{Line: endLine, Col: endCol},
			}},
		}
	}
	c.report(e.WithHint("a bare `..` at a field of an update stands for the value the field already holds"))
}

// pathEnd is the position one past the last byte of a path namePathOf
// accepts.
func pathEnd(n ast.Node) (int, int) {
	switch n := n.(type) {
	case *ast.Ident:
		return n.Line, n.Col + len(n.Name)
	case *ast.FieldAccess:
		return n.Field.Line, n.Field.Col + len(n.Field.Name)
	}
	return 0, 0
}
