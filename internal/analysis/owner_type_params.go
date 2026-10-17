package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// An inherent block of a generic owner (`impl Box<T> { ... }`, `impl Iter<T>
// { ... }`) puts the owner's type parameters in scope, but a function of the
// block is generic over one only when its signature names it. A function
// whose signature names no block parameter (`fn seven() -> Int`, `fn
// twice<U>(x: U) -> #(U, U)`, or `fn find<T>(...)`, whose own `T` shadows the
// block's) is compiled once, without that parameter. Its body therefore
// cannot name it: no call determines it.
//
// checkFunc resolves the body's type names against the signature's type
// parameters only, so such a use is already an unresolved name. This file
// gives that error the reason, at the use.

// explainOwnerTypeParams gives each unresolved-name error that fn's body
// reported since errors[from] its reason, when the name is a type parameter
// of fn's inherent block that fn's signature does not determine. checkFunc
// defers it, so it runs with fn's type parameters still in scope and sees
// every error the body reported, whichever path reported it.
func (c *checker) explainOwnerTypeParams(fn *ast.FuncDef, from int) {
	ib := c.implBlock
	if ib == nil || ib.Interface != nil || IsSynthesizedLine(ib.Line) || from >= len(c.errors) || !implBlockOwns(ib, fn) {
		return
	}
	blockTP, _ := buildImplBlockTypeParams(c.fa, c.reg, ib)
	owner := TypeExprBaseName(ib.Receiver)
	for i := from; i < len(c.errors); i++ {
		e := &c.errors[i]
		if !strings.HasPrefix(e.Message, "unknown type ") {
			continue
		}
		for name := range blockTP {
			if _, own := c.fnTypeParams[name]; own || e.Message != fmt.Sprintf("unknown type %q", name) {
				continue
			}
			e.Message, e.Hints = ownerTypeParamText(name, owner, fn.Name)
		}
	}
}

// ownerTypeParamText is the error's message and help.
func ownerTypeParamText(name, owner, fn string) (string, []string) {
	return fmt.Sprintf("`%s` is `%s`'s type parameter, and `%s`'s signature does not determine it", name, owner, fn),
		[]string{fmt.Sprintf("use `%s` in the signature of `%s`, or declare the function's own type parameter (`fn %s<%s>(...)`)",
			name, fn, fn, name)}
}

// undeterminedOwnerQualifier reports a `T.member` qualifier naming a type
// parameter of the current function's inherent block that the function's
// signature does not determine. The name resolver puts the block's
// parameters in scope for the whole block, so without this the member would
// type permissively and reach the IR builder.
func (c *checker) undeterminedOwnerQualifier(fa *ast.FieldAccess) bool {
	ib, fn := c.implBlock, c.currentFnDecl
	if fa == nil || ib == nil || fn == nil || ib.Interface != nil || IsSynthesizedLine(ib.Line) || !implBlockOwns(ib, fn) {
		return false
	}
	ti, isType := fa.Object.(*ast.TypeIdent)
	if !isType {
		return false
	}
	if _, own := c.fnTypeParams[ti.Name]; own {
		return false
	}
	blockTP, _ := buildImplBlockTypeParams(c.fa, c.reg, ib)
	if _, block := blockTP[ti.Name]; !block {
		return false
	}
	msg, hints := ownerTypeParamText(ti.Name, TypeExprBaseName(ib.Receiver), fn.Name)
	c.report(TypeError{Line: ti.Line, Col: ti.Col, EndLine: ti.Line, EndCol: ti.Col + len(ti.Name), Message: msg, Hints: hints})
	return true
}

// implBlockOwns reports whether fn is one of ib's items rather than a function
// nested in one of their bodies.
func implBlockOwns(ib *ast.ImplBlock, fn *ast.FuncDef) bool {
	for _, item := range ib.Items {
		if item == ast.Node(fn) {
			return true
		}
	}
	return false
}

// checkImplReceiverArity reports an impl block whose receiver names a generic
// type without its type parameters: `impl Box { ... }` for `struct Box<T>`.
// The block's type parameters come from the receiver (§13), so the header
// writes them: `impl Box<T> { ... }`.
func (c *checker) checkImplReceiverArity(n *ast.ImplBlock) {
	st, bare := n.Receiver.(*ast.SimpleType)
	if !bare {
		return
	}
	t := c.reg.Lookup(st.Name)
	name, want, got, generic := declaredArity(t)
	if !generic || got != 0 || want == 0 {
		return
	}
	params := typeParamDefsForType(t)
	names := make([]string, 0, len(params))
	for _, p := range params {
		if p != nil {
			names = append(names, p.Name_)
		}
	}
	if len(names) != want {
		return
	}
	recv := fmt.Sprintf("%s<%s>", name, strings.Join(names, ", "))
	header := "impl " + recv
	if n.Interface != nil {
		header = fmt.Sprintf("impl %s for %s", n.Interface.TypeString(), recv)
	}
	c.report(TypeError{
		Line: st.Line, Col: st.Col, EndLine: st.Line, EndCol: st.Col + len(st.Name),
		Message: arityMessage(st.Name, want, 0),
		Hints:   []string{fmt.Sprintf("an impl block of a generic type names its type parameters: write `%s`", header)},
	})
}
