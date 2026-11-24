package analysis

import (
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
	// The `in` names std's Contains, as the call's qualifier does, and its
	// function `contains`, as the resolver records a written
	// `Contains.contains`'s field: the call is made after resolution, and
	// the checker records the instance and the impl it reaches on that
	// reference.
	c.fa.References[Pos{Line: n.Line, Col: n.Col}] = contains
	if fn := resolveRef(contains).Members["contains"]; fn != nil {
		c.fa.References[Pos{Line: n.Line, Col: n.Col + 1}] = fn
	}
	c.fa.recordStandIn(n.Line, n.Col)
	c.fa.recordStandIn(n.Line, n.Col+1)
	c.checkNode(call)
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

// implCalleeLabel names the interface function a call's callee is in an
// error about which impl the call selects: `Contains.contains`, or for a
// membership test's call the operator, `in`.
func (c *checker) implCalleeLabel(n *ast.Call, iface, method string) string {
	if _, ok := c.memberships[n]; ok {
		return "`in`"
	}
	return implFunctionLabel(iface, method)
}

// implFunctionLabel names an interface function in such an error, as an
// operator's error does too: `Iface.method`.
func implFunctionLabel(iface, method string) string {
	return "`" + iface + "." + method + "`"
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

// reportCollectionWithoutImpl reports a membership test n whose collection,
// of type concrete, has no impl of iface (Contains) that it matches: the
// test's own error at the collection, named by its place in the test
// (argLabel), with the help to write the impl. A collection whose impl
// holds only where its element meets a bound is no error here: its demand
// reaches the element (recordInterfaceSelf). It reports whether it
// reported.
func (c *checker) reportCollectionWithoutImpl(n *ast.Call, iface *InterfaceType, concrete Type) bool {
	at := interfaceAtOwnParams(iface)
	saved := saveTypeVars(concrete)
	err := c.unifyInto(at, concrete, map[*TypeParam_]Type{})
	saved.restore()
	if err == nil {
		return false
	}
	collection := n.Args[0]
	line, col := nodeLineCol(collection)
	before := len(c.errors)
	c.reportGenericArgMismatch(n.Func, at, concrete, nil, collection, c.argLabel(n, 0), line, col, nil, c.argSite(n, 0, collection, line, col))
	return len(c.errors) > before
}
