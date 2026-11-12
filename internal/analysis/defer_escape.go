package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A deferred release runs when its block ends (spec §9 *Releasing
// resources*), so a value it releases must not leave that block: not as
// the block's value, not in a `return`, not inside a literal or a
// constructor that does, and not captured by a lambda that does. A value
// holding the released one counts too: `defer sql.close_quietly(db.conn)`
// releases part of `db`, so `db` must not leave either. A `defer` written
// directly in an entry's `fn boot` runs after `main` finishes instead
// (spec §15 *Scoped application fields*), so its value may leave in the App.

// releasePath is a value a deferred call releases, written as a name and
// the fields after it: `db` for `defer sql.close_quietly(db)`, `db` and
// `conn` for `defer sql.close_quietly(db.conn)`.
type releasePath struct {
	sym    *Symbol
	fields []string
}

// checkDeferredEscapes reports each `defer` in rb whose call takes a value
// of a Release type that then leaves the `defer`'s block.
func (c *checker) checkDeferredEscapes(rb *releaseBody) {
	var visit func(ast.Node)
	visit = func(n ast.Node) {
		switch v := n.(type) {
		case nil:
			return
		case *ast.FuncDef, *ast.Lambda:
			// A lambda is a body of its own (collectAcquisitions).
			return
		case *ast.Block:
			for i, s := range v.Stmts {
				if d, ok := s.(*ast.Defer); ok && !(rb.boot && v == rb.block) {
					c.checkDeferEscape(rb, v, i, d)
				}
			}
		}
		ast.Children(n, visit)
	}
	visit(rb.block)
}

// rootSymbol is the symbol a reference resolves to, through captures.
func (c *checker) rootSymbol(id *ast.Ident) *Symbol {
	s := c.fa.References[Pos{Line: id.Line, Col: id.Col}]
	for s != nil && s.Resolved != nil {
		s = s.Resolved
	}
	return s
}

// pathOf is n as a name and fields (`db.conn`), with the text it is
// written as, or false when n is no such path.
func (c *checker) pathOf(n ast.Node) (releasePath, string, bool) {
	var fields []string
	for {
		switch v := n.(type) {
		case *ast.GroupedExpr:
			n = v.Expr
			continue
		case *ast.FieldAccess:
			if v.Field == nil {
				return releasePath{}, "", false
			}
			fields = append([]string{v.Field.Name}, fields...)
			n = v.Object
			continue
		case *ast.Ident:
			sym := c.rootSymbol(v)
			if sym == nil {
				return releasePath{}, "", false
			}
			text := strings.Join(append([]string{v.Name}, fields...), ".")
			return releasePath{sym: sym, fields: fields}, text, true
		}
		return releasePath{}, "", false
	}
}

func fieldsPrefix(prefix, of []string) bool {
	if len(prefix) > len(of) {
		return false
	}
	for i := range prefix {
		if prefix[i] != of[i] {
			return false
		}
	}
	return true
}

// deferEscape is how a released value leaves its block.
type deferEscape struct {
	name      string // the name that leaves, as written (`db`)
	inside    bool   // it leaves inside a literal, a constructor or a lambda
	lambda    bool   // inside a lambda that captures it
	ret       bool   // through a `return`, not as the block's value
	line, col int
}

// escapeScan finds a released value in what leaves a block. tainted holds
// the released paths and the names bound after the `defer` to values that
// carry one.
type escapeScan struct {
	c       *checker
	tainted []releasePath
}

// holds reports whether the path p, of type ty, carries a tainted value:
// it is one, or holds one (`db` when `db.conn` is released), or is a value
// of a Release type inside one.
func (s *escapeScan) holds(ty Type, p releasePath) bool {
	for _, t := range s.tainted {
		if t.sym != p.sym {
			continue
		}
		if fieldsPrefix(p.fields, t.fields) {
			return true
		}
		if fieldsPrefix(t.fields, p.fields) && s.c.resourceKindOf(ty) != nil {
			return true
		}
	}
	return false
}

func (s *escapeScan) pathEscape(n ast.Node) *deferEscape {
	p, text, ok := s.c.pathOf(n)
	if !ok || !s.holds(s.c.fa.ExprTypes[n], p) {
		return nil
	}
	line, col := stmtStart(n)
	return &deferEscape{name: text, line: line, col: col}
}

// carries reports how n's value carries a released value, or nil.
func (s *escapeScan) carries(n ast.Node) *deferEscape {
	switch v := n.(type) {
	case nil:
		return nil
	case *ast.ExprStmt:
		return s.carries(v.Expr)
	case *ast.GroupedExpr:
		return s.carries(v.Expr)
	case *ast.TryOp:
		return s.carries(v.Expr)
	case *ast.Unwrap:
		return s.carries(v.Expr)
	case *ast.Ident, *ast.FieldAccess:
		return s.pathEscape(v)
	case *ast.Block:
		return s.carries(tailExpr(v))
	case *ast.If:
		if e := s.carries(v.Then); e != nil {
			return e
		}
		return s.carries(v.Else)
	case *ast.Case:
		for _, br := range v.Branches {
			if e := s.carries(br.Body); e != nil {
				return e
			}
		}
		return nil
	case *ast.Call:
		if !isConstructorCall(v) {
			return nil
		}
		args := make([]ast.Node, 0, len(v.Args))
		for _, arg := range v.Args {
			if na, ok := arg.(*ast.NamedArg); ok {
				arg = na.Value
			}
			args = append(args, arg)
		}
		return s.carriesAny(args)
	case *ast.ListLit:
		return s.carriesAny(v.Operands())
	case *ast.TupleLit:
		return s.carriesAny(v.Items)
	case *ast.MapLit:
		for _, e := range v.Entries {
			if esc := s.carriesAny([]ast.Node{e.Key, e.Value}); esc != nil {
				return esc
			}
		}
		return nil
	case *ast.StructLit:
		var items []ast.Node
		if v.Spread != nil {
			items = append(items, v.Spread)
		}
		for _, f := range v.Fields {
			items = append(items, f.Value)
		}
		return s.carriesAny(items)
	case *ast.Lambda:
		var found *deferEscape
		ast.Inspect(v.Body, func(m ast.Node) bool {
			if found != nil {
				return false
			}
			switch m.(type) {
			case *ast.Ident, *ast.FieldAccess:
				if e := s.pathEscape(m); e != nil {
					e.inside, e.lambda = true, true
					found = e
					return false
				}
			}
			return true
		})
		return found
	}
	return nil
}

func (s *escapeScan) carriesAny(items []ast.Node) *deferEscape {
	for _, it := range items {
		if e := s.carries(it); e != nil {
			e.inside = true
			return e
		}
	}
	return nil
}

// taint adds each name a binding in n binds to a value that carries a
// released one.
func (s *escapeScan) taint(n ast.Node) {
	ast.Inspect(n, func(m ast.Node) bool {
		switch v := m.(type) {
		case *ast.Lambda, *ast.FuncDef:
			return false
		case *ast.Binding:
			if ast.IsDiscardName(v.Name) || s.carries(v.Value) == nil {
				return true
			}
			if sym := s.c.fa.Definitions[Pos{Line: v.Line, Col: v.Col}]; sym != nil {
				s.tainted = append(s.tainted, releasePath{sym: sym})
			}
		}
		return true
	})
}

// returns finds a `return` in n, outside a lambda, whose value carries a
// released value.
func (s *escapeScan) returns(n ast.Node) *deferEscape {
	var found *deferEscape
	ast.Inspect(n, func(m ast.Node) bool {
		if found != nil {
			return false
		}
		switch v := m.(type) {
		case *ast.Lambda, *ast.FuncDef:
			return false
		case *ast.Return:
			if e := s.carries(v.Value); e != nil {
				e.ret = true
				found = e
				return false
			}
		}
		return true
	})
	return found
}

// checkDeferEscape checks the `defer` d, the i-th statement of b.
//
// A `defer on error` is not checked: its release runs only on an error
// exit, so a value it covers is meant to leave on the success path.
func (c *checker) checkDeferEscape(rb *releaseBody, b *ast.Block, i int, d *ast.Defer) {
	call, ok := d.Call.(*ast.Call)
	if !ok || d.OnError {
		return
	}
	s := &escapeScan{c: c}
	released := ""
	for _, arg := range call.Args {
		if na, ok := arg.(*ast.NamedArg); ok {
			arg = na.Value
		}
		p, text, ok := c.pathOf(arg)
		if !ok || c.resourceKindOf(c.fa.ExprTypes[stripParens(arg)]) == nil {
			continue
		}
		s.tainted = append(s.tainted, p)
		if released == "" {
			released = text
		}
	}
	if len(s.tainted) == 0 {
		return
	}
	var esc *deferEscape
	rest := b.Stmts[i+1:]
	for j, st := range rest {
		s.taint(st)
		if esc = s.returns(st); esc != nil {
			break
		}
		if j == len(rest)-1 && isValueStmt(st) && !rb.dropped[b] {
			esc = s.carries(st)
		}
	}
	if esc == nil {
		return
	}
	var how string
	switch {
	case esc.ret && esc.inside:
		how = "leaves the block inside a `return`'s value"
	case esc.ret:
		how = "leaves the block in a `return`"
	case esc.inside:
		how = "leaves the block inside its value"
	default:
		how = "leaves the block as its value"
	}
	subject := fmt.Sprintf("`%s`", esc.name)
	if esc.lambda {
		subject = fmt.Sprintf("a lambda that captures `%s`", esc.name)
	}
	line, col := d.Line, d.Col
	if span := stmtSpan(d); !span.IsZero() {
		line, col = span.StartLine, span.StartCol
	}
	c.report(TypeError{
		Line: line,
		Col:  col,
		Message: fmt.Sprintf("this `defer` releases `%s` when the block ends, but %s %s; defer the release where `%s` is finally used, or hand `%s` on without deferring it here",
			released, subject, how, esc.name, esc.name),
		Related: []RelatedInfo{{Line: esc.line, Col: esc.col, Message: fmt.Sprintf("`%s` leaves the block here", esc.name)}},
		Code:    EscapedReleaseCode,
	})
}

// stripParens takes the parentheses off n.
func stripParens(n ast.Node) ast.Node {
	for {
		g, ok := n.(*ast.GroupedExpr)
		if !ok {
			return n
		}
		n = g.Expr
	}
}
