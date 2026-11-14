package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Where a call to a function whose signature the builder refused leads.
//
// A function whose parameter or result type the builder cannot represent
// has no signature a call could use (signature), so its body is never
// attempted and no decline of its own names it. A call to it declines
// instead, in the caller, as `a direct callee that is not lowerable`, and
// that decline's position is the call: a program whose `main` calls
// `run_prompt(state)`, where `state`'s type holds a type the builder
// refuses, was reported at main's call, with nothing pointing at the type.
//
// So such a decline carries the refused signature (Decline.Signature): the
// parameter or result whose type was refused, the builder's reason for that
// type, and the declaration whose own refusal it cascades from (the
// `Big` a parameter's `Interp` holds), found by descending the refused
// type's fields and payloads in the files that declare them. A host
// reports the decline there.

// SignatureDecline is a function signature the builder refused.
type SignatureDecline struct {
	// Fn is the function, and Path its file.
	Fn, Path string
	// Line and Col are the refused parameter's or result's type annotation,
	// or the function's declaration when no one type was refused (a
	// decorator, a generic signature the builder declines).
	Line, Col int
	// Param is the refused parameter's name; empty for the result, and when
	// no one type was refused.
	Param string
	// Type is the refused annotation as written; empty when no one type
	// was refused.
	Type string
	// Reason is the builder's reason, in its own terms.
	Reason string
	// Root is the type whose own refusal Type's cascades from, when that is
	// another declaration than the one Type names (`Big`, which the
	// parameter's `Interp` holds); RootPath, RootLine and RootCol are its
	// declaration's. Empty when Type's own declaration is the cause.
	Root              string
	RootPath          string
	RootLine, RootCol int
}

// calleeNotLowerable is the start of declineCallReason's reason for a call to
// a function whose signature the builder refused.
const calleeNotLowerable = "a direct callee that is not lowerable: "

// calleeSignatureDecline is the refused signature of n's callee, when n is a
// direct call that declined because the builder refused that signature
// (declineCallReason's `not lowerable` arm); nil otherwise.
func (bl *irScalarBuilder) calleeSignatureDecline(n ast.Node, reason string) *SignatureDecline {
	call, isCall := n.(*ast.Call)
	if !isCall || !strings.HasPrefix(reason, calleeNotLowerable) {
		return nil
	}
	callee, direct := call.Func.(*ast.Ident)
	if !direct {
		return nil
	}
	sig := bl.g.funcs[callee.Name]
	if sig == nil || sig.lowerable || sig.decl == nil {
		return nil
	}
	return bl.g.signatureDecline(sig)
}

// signatureDecline is where and why the builder refused sig, a signature of
// this file.
func (g *gen) signatureDecline(sig *fnSig) *SignatureDecline {
	fd := sig.decl
	sd := &SignatureDecline{Fn: fd.Name, Path: g.filePath()}
	// kindInvalid: reports — a kindless parameter or result is the refusal this function locates and names, at its annotation.
	refused := func(k kind) bool { return k == kindInvalid || !irSigDomainKind(sig, k) }
	if sig.genericWhy == "" && len(fd.Decorators) == 0 && fd.Body != nil {
		for i, p := range fd.Params {
			if i < len(sig.params) && p.TypeAnnotation != nil && refused(sig.params[i]) {
				sd.Param = p.Name
				g.refusedAnnotation(sd, p.TypeAnnotation, sig.params[i])
				return sd
			}
		}
		if fd.ReturnTypeExpr != nil && refused(sig.result) {
			g.refusedAnnotation(sd, fd.ReturnTypeExpr, sig.result)
			return sd
		}
	}
	sd.Line, sd.Col = nodePos(fd)
	switch {
	case sig.genericWhy != "":
		sd.Reason = sig.genericWhy
	case len(fd.Decorators) > 0:
		sd.Reason = "decorator"
	case fd.Body == nil:
		sd.Reason = "function without a body"
	default:
		sd.Reason = "a signature the builder refused"
	}
	return sd
}

// refusedAnnotation fills sd from te, a parameter's or the result's type
// annotation that the builder refused at kind k.
func (g *gen) refusedAnnotation(sd *SignatureDecline, te ast.TypeExpr, k kind) {
	sd.Line, sd.Col = nodePos(te)
	sd.Type = te.TypeString()
	if construct, detail, ok := g.typeRefusal(te); ok {
		sd.Reason = construct + ": " + detail
	} else {
		sd.Reason = "a kind outside the value domain: " + k.nomi()
	}
	named := g.refusedDefs(te)
	if len(named) == 0 {
		return
	}
	root, owner := g.refusalRoot(named[0], map[*typeDef]bool{})
	if root == nil || root.decl == nil || root.decl == named[0].decl {
		return
	}
	sd.Root = root.nomi
	sd.RootPath = owner.filePath()
	sd.RootLine, sd.RootCol = nodePos(root.decl)
}

// filePath is the file g lowers, or "" when its analysis has none.
func (g *gen) filePath() string {
	if g.fa == nil {
		return ""
	}
	return g.fa.FilePath
}

// refusedDefs are the declarations te names, in g's scope, that the builder
// refused, a container's arguments before the container: `Maybe<Big>` names
// Big.
func (g *gen) refusedDefs(te ast.TypeExpr) []*typeDef {
	var out []*typeDef
	var walk func(te ast.TypeExpr)
	walk = func(te ast.TypeExpr) {
		switch t := te.(type) {
		case *ast.SimpleType:
			if d, ok := g.namedType(t.Name); ok && d != nil && !d.lowerable {
				out = append(out, d)
			}
		case *ast.QualifiedType:
			if d, ok := g.namespacedType(t); ok && d != nil && !d.lowerable {
				out = append(out, d)
			}
		case *ast.GenericType:
			for _, p := range t.Params {
				walk(p)
			}
		case *ast.FuncType:
			for _, p := range t.Params {
				walk(p)
			}
			if t.Return != nil {
				walk(t.Return)
			}
		case *ast.AnonStructType:
			for _, f := range t.Fields {
				walk(f.TypeAnnotation)
			}
		}
	}
	walk(te)
	return out
}

// refusalRoot follows d, a refused declaration, to the one whose refusal is
// its own: down each field and payload whose type the builder refused, in the
// file that declares it. A declaration none of whose components is refused
// (or whose refused components are all on the walk already, as on a cycle
// the builder refused) is the root. owner is the gen of the root's file.
func (g *gen) refusalRoot(d *typeDef, seen map[*typeDef]bool) (*typeDef, *gen) {
	owner := g.declaringGen(d)
	if seen[d] {
		return nil, nil
	}
	seen[d] = true
	var annotations []ast.TypeExpr
	switch t := d.decl.(type) {
	case *ast.StructDef:
		for _, f := range t.Fields {
			annotations = append(annotations, f.TypeAnnotation)
		}
	case *ast.EnumDef:
		for _, v := range t.Variants {
			annotations = append(annotations, v.DataTypeExpr, v.EmbeddedTypeExpr)
			for _, f := range v.Fields {
				annotations = append(annotations, f.TypeAnnotation)
			}
		}
	}
	for _, te := range annotations {
		if te == nil {
			continue
		}
		for _, inner := range owner.refusedDefs(te) {
			if root, rootOwner := owner.refusalRoot(inner, seen); root != nil {
				return root, rootOwner
			}
		}
	}
	return d, owner
}

// declaringGen is the gen of the file that declares d, g when d is g's own or
// no registry knows it.
func (g *gen) declaringGen(d *typeDef) *gen {
	if g.reg == nil || d.decl == nil {
		return g
	}
	o := g.reg.byDecl[d.decl]
	if o == nil || o.unit < 0 || o.unit >= len(g.reg.gens) || g.reg.gens[o.unit] == nil {
		return g
	}
	return g.reg.gens[o.unit]
}
