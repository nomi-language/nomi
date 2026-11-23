package ast

import "math/big"

// IntRuleBounds reads the bounds a refinement's `when` rule states of an Int:
// a lambda of one parameter whose body is comparisons of the parameter
// with Int literals, possibly negated, on either side, joined by `and`
// (`fn n -> n >= 0 and n <= 255`). min and max are nil on a side the rule
// does not bound. exact is set when every conjunct is such a comparison,
// so the bounds are the rule: a value within them satisfies it. A conjunct
// of any other form narrows the values further and is left out, which
// keeps [min, max] a superset of the values the rule admits; a rule of
// another form (a function's name, an `or`) bounds nothing.
func IntRuleBounds(rule Node) (min, max *big.Int, exact bool) {
	lam, ok := rule.(*Lambda)
	if !ok || len(lam.Params) != 1 || lam.Body == nil || len(lam.Body.Stmts) != 1 {
		return nil, nil, false
	}
	param := lam.Params[0].Name
	body := lam.Body.Stmts[0]
	if s, ok := body.(*ExprStmt); ok {
		body = s.Expr
	}
	exact = true
	lower := func(b *big.Int) {
		if min == nil || b.Cmp(min) > 0 {
			min = b
		}
	}
	upper := func(b *big.Int) {
		if max == nil || b.Cmp(max) < 0 {
			max = b
		}
	}
	var walk func(e Node)
	walk = func(e Node) {
		if g, ok := e.(*GroupedExpr); ok {
			walk(g.Expr)
			return
		}
		x, ok := e.(*Binary)
		if !ok {
			exact = false
			return
		}
		if x.Op == "and" {
			walk(x.Left)
			walk(x.Right)
			return
		}
		op := x.Op
		lit := intRuleLiteral(x.Right)
		if !isRuleParam(x.Left, param) {
			// `0 <= n` reads as `n >= 0`.
			if !isRuleParam(x.Right, param) {
				exact = false
				return
			}
			lit = intRuleLiteral(x.Left)
			op = map[string]string{"<": ">", "<=": ">=", ">": "<", ">=": "<=", "==": "=="}[op]
		}
		if lit == nil {
			exact = false
			return
		}
		one := big.NewInt(1)
		switch op {
		case ">=":
			lower(lit)
		case ">":
			lower(new(big.Int).Add(lit, one))
		case "<=":
			upper(lit)
		case "<":
			upper(new(big.Int).Sub(lit, one))
		case "==":
			lower(lit)
			upper(new(big.Int).Set(lit))
		default:
			exact = false
		}
	}
	walk(body)
	if min == nil && max == nil {
		return nil, nil, false
	}
	return min, max, exact
}

func isRuleParam(e Node, param string) bool {
	if g, ok := e.(*GroupedExpr); ok {
		return isRuleParam(g.Expr, param)
	}
	id, ok := e.(*Ident)
	return ok && id.Name == param
}

// intRuleLiteral is the value of an Int literal, negated or not, or nil.
func intRuleLiteral(e Node) *big.Int {
	switch x := e.(type) {
	case *GroupedExpr:
		return intRuleLiteral(x.Expr)
	case *IntLit:
		if x.Big != nil {
			return new(big.Int).Set(x.Big)
		}
		return big.NewInt(x.Value)
	case *Unary:
		if x.Op != "-" {
			return nil
		}
		if v := intRuleLiteral(x.Right); v != nil {
			return v.Neg(v)
		}
	}
	return nil
}
