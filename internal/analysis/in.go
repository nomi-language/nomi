package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A membership test, `x in c`, is a Bool: whether the collection c holds x.
// It is the call `Contains.contains(c, x)` written as an operator, so any
// type with a `Contains` impl, a program's own included, answers it, and a
// type parameter bounded by `Contains<T>` does too. The value is evaluated
// before the collection, left to right as written.

// checkIn checks `x in c` by checking the call it stands for, recorded for
// the IR builder (FileAnalysis.Memberships). A mismatch the call reports at
// one of its arguments names it by its place in the test (argLabel).
func (c *checker) checkIn(n *ast.Binary) Type {
	contains := c.fa.ModuleScope.Lookup("Contains")
	if contains == nil {
		// A std file has no prelude, so it names Contains by importing it.
		c.checkNode(n.Left)
		c.checkNode(n.Right)
		c.addError(n.Line, n.Col, "`in` needs the interface `Contains` in scope: import `contains.Contains`")
		return TypeBool
	}
	call := membershipCall(n)
	if c.fa.Memberships == nil {
		c.fa.Memberships = map[*ast.Binary]*ast.Call{}
	}
	c.fa.Memberships[n] = call
	if c.memberships == nil {
		c.memberships = map[*ast.Call]*ast.Binary{}
	}
	c.memberships[call] = n
	// The `in` names std's Contains, as the call's qualifier does.
	c.fa.References[Pos{Line: n.Line, Col: n.Col}] = contains
	c.fa.recordStandIn(n.Line, n.Col)
	c.fa.recordStandIn(n.Line, n.Col+1)
	before := len(c.errors)
	c.checkNode(call)
	// The callee is written at the `in`, so an error there about the
	// callee names the operator: `in` for `String` has impls at ...
	for i := before; i < len(c.errors); i++ {
		if e := &c.errors[i]; e.Line == n.Line && e.Col == n.Col {
			if rest, ok := strings.CutPrefix(e.Message, "`Contains.contains`"); ok {
				e.Message = "`in`" + rest
			}
		}
	}
	return TypeBool
}

// membershipCall is the call `x in c` stands for, `Contains.contains(c, x)`,
// over the test's own operands. `Contains` is a reserved name, so it is
// std's interface in every file. Its callee is written at the `in`: the
// qualifier at its first column and the function at its second, so each
// keeps its own reference.
func membershipCall(n *ast.Binary) *ast.Call {
	return &ast.Call{
		Func: &ast.FieldAccess{
			Object: &ast.TypeIdent{Name: "Contains", Line: n.Line, Col: n.Col},
			Field:  &ast.Ident{Name: "contains", Line: n.Line, Col: n.Col + 1},
			Line:   n.Line, Col: n.Col,
		},
		Args: []ast.Node{n.Right, n.Left},
		Line: n.Line,
		Col:  n.Col,
	}
}

// StandInReference reports whether References holds a name at pos that the
// text there does not spell (FileAnalysis.StandIns): a column of the `in`
// of a membership test, or a quote, `$` or `{` of an `Html` literal. A tool
// that edits, colors or lists a name's occurrences skips it.
func (fa *FileAnalysis) StandInReference(pos Pos) bool {
	return fa.StandIns[Pos{Line: pos.Line, Col: pos.Col}]
}

// recordStandIn adds the position line:col to fa.StandIns.
func (fa *FileAnalysis) recordStandIn(line, col int) {
	if fa.StandIns == nil {
		fa.StandIns = map[Pos]bool{}
	}
	fa.StandIns[Pos{Line: line, Col: col}] = true
}

// argLabel names the argument at 0-based position i of the call n in a
// mismatch message: `argument 2`, or for a membership test's call the
// collection or the value it looks for.
func (c *checker) argLabel(n *ast.Call, i int) string {
	if _, ok := c.memberships[n]; ok {
		switch i {
		case 0:
			return "the collection `in` looks in"
		case 1:
			return "the value `in` looks for"
		}
	}
	return positionalArgLabel(i)
}
