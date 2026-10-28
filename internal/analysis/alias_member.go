package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A typealias is a spelling of its target in type positions. Nothing hangs
// functions off it: `impl P`, `derive Debug for P` and a type-qualified call
// `P.get(p)` or `shapes.P.get(p)` through `typealias P Pt` are errors that
// name the target, for an alias the file declares and one it imports alike.
// A variant reached through an alias of an enum (`L.Red` under
// `typealias L Color`) is a value of the target and stays allowed.

// aliasTargetText is how an error spells the type the typealias sym stands
// for: its resolved type when the builder has one, else the declaration's
// own spelling of the target.
func aliasTargetText(sym *Symbol) string {
	if sym.Type != nil {
		if _, iface := sym.Type.(*InterfaceType); !iface {
			return sym.Type.String()
		}
	}
	ta, ok := sym.Node.(*ast.TypeAlias)
	if !ok {
		return ""
	}
	if ta.TargetTypeExpr != nil {
		return ta.TargetTypeExpr.TypeString()
	}
	parts := make([]string, len(ta.Bounds))
	for i, b := range ta.Bounds {
		parts[i] = b.TypeString()
	}
	return strings.Join(parts, " and ")
}

// resolvedTypeAlias follows sym's import chain and answers the typealias it
// ends at, or nil.
func resolvedTypeAlias(sym *Symbol) *Symbol {
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil || sym.Kind != SymbolTypeAlias {
		return nil
	}
	return sym
}

// implReceiverAliasError is the error for an impl whose receiver names a
// typealias, at the receiver, or "" when it names none.
func (b *builder) implReceiverAliasError(n *ast.ImplBlock, scope *Scope) (line, col int, msg string) {
	var name string
	switch t := n.Receiver.(type) {
	case *ast.SimpleType:
		name, line, col = t.Name, t.Line, t.Col
	case *ast.GenericType:
		name, line, col = t.Name, t.Line, t.Col
	default:
		return 0, 0, ""
	}
	alias := resolvedTypeAlias(scope.Lookup(name))
	if alias == nil {
		return 0, 0, ""
	}
	target := aliasTargetText(alias)
	header := "impl " + target
	if n.Interface != nil {
		header = "impl " + n.Interface.TypeString() + " for " + target
	}
	return line, col, fmt.Sprintf("`%s` is a typealias for `%s`; write `%s`", name, target, header)
}

// qualifierAlias is the typealias a type qualifier names (`P` in `P.get`,
// `shapes.P` in `shapes.P.get`), or nil.
func (c *checker) qualifierAlias(object ast.Node) *Symbol {
	var sym *Symbol
	switch o := object.(type) {
	case *ast.TypeIdent:
		sym = c.fa.References[Pos{Line: o.Line, Col: o.Col}]
		if sym == nil {
			sym = c.fa.ModuleScope.Lookup(o.Name)
		}
	case *ast.FieldAccess:
		if o.Field != nil {
			sym = c.fa.References[Pos{Line: o.Field.Line, Col: o.Field.Col}]
		}
	}
	return resolvedTypeAlias(sym)
}

// aliasMemberError reports a type-qualified member reached through a
// typealias and answers true, or answers false when the qualifier names no
// alias, the alias stands for an interface, or the member is a variant of
// the enum the alias stands for.
func (c *checker) aliasMemberError(n *ast.FieldAccess, qualifier string) bool {
	alias := c.qualifierAlias(n.Object)
	if alias == nil {
		return false
	}
	switch t := alias.Type.(type) {
	case nil, *InterfaceType:
		return false
	case *EnumType:
		for _, v := range t.Variants {
			if v.Name == n.Field.Name {
				return false
			}
		}
	}
	owner := concreteTypeName(alias.Type)
	if owner == "" {
		owner = alias.Type.String()
	}
	if i := strings.LastIndex(qualifier, "."); i >= 0 {
		owner = qualifier[:i+1] + owner
	}
	c.addError(n.Field.Line, n.Field.Col, fmt.Sprintf(
		"`%s` is a typealias for `%s`; call `%s.%s`",
		qualifier, aliasTargetText(alias), owner, n.Field.Name))
	return true
}
