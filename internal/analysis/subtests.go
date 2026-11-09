package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// `subtests rows, pattern { body }` runs body once per element of rows, with
// pattern bound to the element. A failed assertion in the body ends that row;
// the statement fails once every row has run, if any row failed, and that
// failure leaves the way one assertion's does. So the statement itself is
// placed by the assertion rules (a test body, a fn returning
// `Result<_, AssertionFailure>`), and the body's own assertions have no
// boundary to check: the row is theirs.
//
// A row is not a function. Every row runs to its end, in order, so `return`,
// `break`, `continue` and `?` written directly in the body are errors
// (rejectRowExit); inside a lambda or a nested fn in the body they leave that,
// as anywhere.

// subtestsRowBoundary is the boundary the checker names for an assertion or a
// `?` in a `subtests` body: the hover says a failure unwinds to it, and the
// IR builder reads it back (internal/irbuild's isSubtestsRowBoundary).
const subtestsRowBoundary = "subtests row"

// SubtestsRowBoundary is subtestsRowBoundary for the IR builder.
const SubtestsRowBoundary = subtestsRowBoundary

// neverFailingSubtestsMsg is the error for a `subtests` statement no row of
// which can fail: its body holds no assertion of its own and its final value
// is no Result<_, AssertionFailure>. A test must be able to fail, and the
// statement is what counts as the test's assertion (the parser's rule), so
// the statement must be able to fail too.
const neverFailingSubtestsMsg = "`subtests` body must contain an assert or refute, or end each row with a Result<_, AssertionFailure>"

const refutableSubtestsPatternMsg = "a `subtests` pattern must match every row; bind it with a name or an irrefutable pattern and match it in the body"

func (c *checker) checkSubtests(n *ast.Subtests) Type {
	rowsTy := c.checkNode(n.Rows)
	rowTy, iterable := c.subtestsRowType(rowsTy)
	switch {
	case rowsTy != nil && !iterable:
		c.report(errAt(n.Rows, c.typef(
			"`subtests` runs its body once per element of what it is given, which must be something `Iter` accepts, such as a List, a Range or an Iter; got %s", rowsTy)))
	case rowsTy != nil && rowTy == nil:
		c.report(errAt(n.Rows, c.typef(
			"the type of the rows of `subtests` is not determined: %s", rowsTy)).WithHint(
			"give the rows a type, as in `rows: List<Int> = []`"))
	}
	switch {
	case c.subtestsRow != nil:
		c.report(TypeError{Line: n.Line, Col: n.Col,
			Message: "`subtests` cannot be written inside another `subtests` body: each row of the outer statement is one case",
			Hints:   []string{"combine the two row lists into one, as in `subtests pairs, (a, b) { ... }`, or write the inner rows as a test of their own"}})
	case c.setupBoundary != "" && c.tryBoundary == c.setupBoundary:
		c.report(TypeError{Line: n.Line, Col: n.Col,
			Message: "`subtests` cannot be used in a `setup`: a setup builds the value each test binds",
			Hints:   []string{"move the rows into a test of the group"}})
	default:
		c.checkSubtestsBoundary(n)
	}
	if n.Pattern != nil {
		if rowTy != nil {
			c.checkParamPatternRefutable(n.Pattern, rowTy, func(line, col int, _ string) {
				c.addError(line, col, refutableSubtestsPatternMsg)
			})
		}
		c.checkPattern(n.Pattern, rowTy)
	}
	if n.Name != nil {
		c.checkSubtestsName(n.Name)
	}
	if rowTy != nil {
		if c.fa.SubtestsRowTypes == nil {
			c.fa.SubtestsRowTypes = map[*ast.Subtests]Type{}
		}
		c.fa.SubtestsRowTypes[n] = rowTy
	}
	if n.Body != nil {
		prevRow, prevBoundary, prevAsserts := c.subtestsRow, c.tryBoundary, c.subtestsRowAsserts
		c.subtestsRow, c.tryBoundary, c.subtestsRowAsserts = n, subtestsRowBoundary, false
		c.checkBlock(n.Body)
		asserts := c.subtestsRowAsserts
		c.subtestsRow, c.tryBoundary, c.subtestsRowAsserts = prevRow, prevBoundary, prevAsserts
		if !asserts && c.rowTailCannotFail(n.Body) {
			c.report(TypeError{Line: n.Line, Col: n.Col,
				Message: neverFailingSubtestsMsg,
				Hints:   []string{"assert something about each row, or end the body with a call that returns Result<_, AssertionFailure>, as a helper that asserts does"}})
		}
	}
	// The keyword's hover, and the boundary its combined failure leaves to,
	// which the IR builder reads back as it does an assertion's.
	c.registerAssertionHoverAt(n.Line, n.Col, n, "subtests", rowsTy, rowTy, TypeUnit)
	return TypeUnit
}

// subtestsNameExitless is the site of a row's name: the VM evaluates it on
// its own, before the row's body runs, so it is no part of the row an exit
// could leave (rejectExitlessExit).
var subtestsNameExitless = &exitlessSite{
	what: "the name of a `subtests` row",
	why:  "the name is evaluated on its own, before the row runs",
	hint: "handle the Result or Maybe inside the name with `case`, or compute the name in a function",
}

// checkSubtestsName checks a row's name, `subtests rows, pattern, name`: a
// String expression in the scope of the pattern's bindings. An exit or an
// assertion written directly in it is an error, as in a `once` initializer.
func (c *checker) checkSubtestsName(name ast.Node) {
	prevRow, prevUnwinds := c.subtestsRow, c.tryUnwinds
	c.subtestsRow, c.tryUnwinds = nil, nil
	ty := c.checkExitless(subtestsNameExitless, func() Type {
		return c.checkNodeExpecting(name, TypeString)
	})
	c.subtestsRow, c.tryUnwinds = prevRow, prevUnwinds
	if ty == nil {
		return
	}
	if c.unify(TypeString, ty, nil) != nil {
		c.report(errAt(name, c.typef(
			"the name of a `subtests` row must be a String, got %s", ty)).WithHint(
			"build a String from the row's values, as in `\"${input}\"`"))
	}
}

// checkSubtestsBoundary places the statement as an assertion is placed: its
// failure leaves the activation it is written in.
func (c *checker) checkSubtestsBoundary(n *ast.Subtests) {
	if c.tryUnwinds != nil && c.tryBoundary != "lambda" {
		*c.tryUnwinds = append(*c.tryUnwinds, tryUnwind{
			Flavour: "Result", Err: c.assertionFailureType(), Assertion: true,
			Line: n.Line, Col: n.Col,
		})
		return
	}
	c.checkAssertionBoundaryAt(n.Line, n.Col, "subtests")
}

// subtestsRowType is the element type of rowsTy as an `Iter` sees it: T for
// an `Iter<T>`, and for anything else whatever its `Iter` impl yields (a
// List's or Vector's element, a Set's member, a Map's `(key, value)` pair, a
// Range's value, a user type's own). ok is false when rowsTy is no Iter, and
// the type is nil with ok true when it is one whose element is not
// determined (`[]`).
func (c *checker) subtestsRowType(rowsTy Type) (Type, bool) {
	if rowsTy == nil {
		return nil, false
	}
	if elem, ok := iterElemType(rowsTy); ok {
		return elem, true
	}
	// The std collections answer directly, as their `Iter` impls do.
	switch t := resolveTypeVar(rowsTy).(type) {
	case *ListType:
		if !containsTypeVar(t.Elem) {
			return t.Elem, true
		}
	case *MapType:
		if !containsTypeVar(t.Key) && !containsTypeVar(t.Val) {
			return &TupleType{Elems: []Type{t.Key, t.Val}}, true
		}
	}
	if elem, ok := vectorElemType(rowsTy); ok && !containsTypeVar(elem) {
		return elem, true
	}
	if elem, ok := setElemType(rowsTy); ok && !containsTypeVar(elem) {
		return elem, true
	}
	iface, ok := c.reg.Lookup("Iter").(*InterfaceType)
	if !ok || !isStdIterType(iface) || len(iface.TypeParamDefs) != 1 {
		return nil, false
	}
	elem := c.freshTypeVar()
	want := &InterfaceType{
		Origin:        iface.Origin,
		Name:          iface.Name,
		Methods:       iface.Methods,
		TypeParams:    iface.TypeParams,
		TypeParamDefs: iface.TypeParamDefs,
		TypeArgs:      []Type{elem},
		SelfParam:     iface.SelfParam,
	}
	if c.unifyInto(want, rowsTy, map[*TypeParam_]Type{}) != nil {
		return nil, false
	}
	if resolved := resolveTypeVar(elem); resolved != Type(elem) && !containsTypeVar(resolved) {
		return resolved, true
	}
	// An Iter whose element nothing determines, such as `[]`.
	return nil, true
}

// rejectRowExit reports `return`, `break`, `continue` or `?` written
// directly in a `subtests` body, and says whether it did. Every row runs to
// its end, so there is nothing for any of them to leave.
func (c *checker) rejectRowExit(kw string, line, col int) bool {
	if c.subtestsRow == nil {
		return false
	}
	var msg, hint string
	switch kw {
	case "return":
		msg = "`return` cannot be used in a `subtests` body: a row is not a function, and each row runs to its end"
		hint = "end the row with its last statement, or put what follows in an `if` or `case` arm"
	case "break", "continue":
		msg = fmt.Sprintf("`%s` cannot be used in a `subtests` body: every row runs, in order", kw)
		hint = "choose the rows before the statement, as in `subtests rows |> Iter.filter(keep), row { ... }`"
	case "?":
		msg = "`?` cannot be used in a `subtests` body: a row is not a function, so there is nothing to return the Err or None to"
		hint = "assert the shape instead: `assert Ok(value) = expr` binds value or fails the row"
	default:
		return false
	}
	c.report(TypeError{Line: line, Col: col, Message: msg, Hints: []string{hint}})
	return true
}

// rowTailCannotFail reports whether a `subtests` body's final value is known
// not to be a Result<_, AssertionFailure>, the value that fails a row. It
// reaches through a final `if`, `case` or block to each arm, as the row's
// verdict does, and answers false (it may fail) for an arm whose type the
// checker did not settle, so an error already reported there is not
// compounded.
func (c *checker) rowTailCannotFail(n ast.Node) bool {
	switch v := n.(type) {
	case nil:
		return true
	case *ast.Block:
		if len(v.Stmts) == 0 {
			return true
		}
		return c.rowTailCannotFail(v.Stmts[len(v.Stmts)-1])
	case *ast.ExprStmt:
		return c.rowTailCannotFail(v.Expr)
	case *ast.If:
		return c.rowTailCannotFail(v.Then) && c.rowTailCannotFail(v.Else)
	case *ast.Case:
		for _, b := range v.Branches {
			if !c.rowTailCannotFail(b.Body) {
				return false
			}
		}
		return true
	}
	ty, known := c.fa.ExprTypes[n]
	if !known || ty == nil {
		// A statement (a binding, a `defer`, a `with`) has no value a
		// verdict reads; any other unknown is an error reported elsewhere.
		return isRowStatement(n)
	}
	resolved := resolveTypeVar(ty)
	switch resolved := resolved.(type) {
	case *TypeVar, *TypeParam_:
		return false
	case *EnumType:
		if resolved.Name == "Result" && len(resolved.TypeArgs) == 2 {
			errTy := resolveTypeVar(resolved.TypeArgs[1])
			if _, open := errTy.(*TypeVar); open {
				return false
			}
			return !sameNominal(errTy, c.assertionFailureType())
		}
	}
	// A row that ends by diverging has no value to judge.
	return resolved != Type(TypeInfallible)
}

// isRowStatement is a statement whose value a row's verdict never reads.
func isRowStatement(n ast.Node) bool {
	switch n.(type) {
	case *ast.Binding, *ast.Defer, *ast.With, *ast.TupleDestructure, *ast.StructDestructure,
		*ast.DistinctDestructure, *ast.PatternBinding:
		return true
	}
	return false
}

// A group's `subtests rows, pattern, name { ... }` block runs the group's
// boot line, setup and tests once per row, with pattern bound to the row in
// each of them. The rows are read when the tests are collected, not run:
// each row's tests are cases of their own, named with the row
// (ast.GroupRowLabels), which `nomi test` lists, counts and selects before
// anything runs. So the rows are a list literal of literal values, and a
// row's name is a column of it.

const (
	groupRowsListMsg    = "the rows of a `subtests` block in a `group` are a list literal, such as `[\"memory\", \"sqlite\"]`: each row's tests are named and counted when the tests are collected, before anything runs"
	groupRowLiteralMsg  = "a row of a `subtests` block in a `group` is a literal value, read when the tests are collected: a String, a number, True or False, or a tuple of them"
	groupRowTryMsg      = "`?` cannot be used in a row of a `subtests` block in a `group`: the rows are literal values, read when the tests are collected"
	groupRowNameMsg     = "the name of a row of a `subtests` block in a `group` is a String column its pattern binds, such as `subtests [(\"memory\", 1)], (store, workers), store`"
	groupRowNameTypeMsg = "the name of a `subtests` row must be a String, got %s"
)

// checkGroupSubtests checks a group's `subtests` header: literal rows, a
// pattern that matches every row, and a name that is one of its String
// columns.
func (c *checker) checkGroupSubtests(gs *ast.GroupSubtests) {
	rowsTy := c.checkNode(gs.Rows)
	if list, ok := gs.Rows.(*ast.ListLit); !ok || list.TypeName != nil {
		c.report(errAt(gs.Rows, groupRowsListMsg).WithHint(
			"write the rows in place; a test that needs computed rows runs a `subtests` statement in its body"))
	} else {
		for _, row := range list.Items {
			if try := firstTryOp(row); try != nil {
				c.report(errAt(try, groupRowTryMsg))
				continue
			}
			if bad := ast.GroupRowNonLiteral(row); bad != nil {
				c.report(errAt(bad, groupRowLiteralMsg).WithHint(
					"compute the value in the boot line, the setup or the test from the row"))
			}
		}
	}
	rowTy, iterable := c.subtestsRowType(rowsTy)
	if rowsTy != nil && iterable && rowTy == nil {
		c.report(errAt(gs.Rows, c.typef(
			"the type of the rows of `subtests` is not determined: %s", rowsTy)).WithHint(
			"write at least one row"))
	}
	if gs.Pattern != nil {
		if rowTy != nil {
			c.checkParamPatternRefutable(gs.Pattern, rowTy, func(line, col int, _ string) {
				c.addError(line, col, refutableSubtestsPatternMsg)
			})
		}
		c.checkPattern(gs.Pattern, rowTy)
	}
	if gs.Name != nil {
		ty := c.checkNode(gs.Name)
		if _, ok := ast.GroupRowColumn(gs); !ok {
			c.report(errAt(gs.Name, groupRowNameMsg))
		} else if ty != nil && c.unify(TypeString, ty, nil) != nil {
			c.report(errAt(gs.Name, c.typef(groupRowNameTypeMsg, ty)))
		}
	}
	if rowTy != nil {
		if c.fa.GroupRowTypes == nil {
			c.fa.GroupRowTypes = map[*ast.GroupSubtests]Type{}
		}
		c.fa.GroupRowTypes[gs] = rowTy
	}
}

// firstTryOp is the first `?` written in n, or nil.
func firstTryOp(n ast.Node) ast.Node {
	var found ast.Node
	ast.Inspect(n, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if t, ok := n.(*ast.TryOp); ok {
			found = t
			return false
		}
		return true
	})
	return found
}
