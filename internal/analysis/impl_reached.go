package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// reachedImpl is out, a classified callee, with the impl block it reaches
// (Callee.Impl) when the receiver's type is known at the call: the block
// that declares the impl function the reference names or the checker
// chose (DispatchImpl), or, for an interface function, the one block of
// the interface for the receiver's type (interfaceImplAt). A call through
// a type parameter or on an interface value reaches no block here; an
// instance of its body asks CalleeAt.
func (fa *FileAnalysis) reachedImpl(out Callee) Callee {
	if fa == nil || out.Ref == nil {
		return out
	}
	switch out.Kind {
	case CalleeFunction, CalleeHost, CalleeImpl:
		b := fa.implBlockHolding(out.Decl)
		if b == nil {
			return out
		}
		out.Kind, out.Impl = CalleeImpl, b
		return fa.withDeclaringFile(out)
	case CalleeInterface:
		if out.Qualifier.Kind == QualTypeParam {
			return out
		}
		recv := out.For
		if out.Ref.DispatchReceiver != nil {
			recv = out.Ref.DispatchReceiver
		}
		return fa.interfaceImplAt(out, recv, callInterfaceArgs(out.Ref, nil))
	}
	return out
}

// interfaceImplAt is out, a call of an interface function, at recv, its
// receiver's type: with a block (out's own, the one the checker selected,
// else the one block of the interface for recv, at ifaceArgs where recv
// implements the interface at several type arguments), CalleeImpl, Impl
// that block and Decl the function that runs, the block's own or, where
// the block inherits it, the interface's. out unchanged when recv is no
// known type (a type parameter, an interface value) or no single block
// answers. The one resolver of an interface call's impl: reachedImpl asks
// it at the types the call is checked with, CalleeAt at an instance's.
func (fa *FileAnalysis) interfaceImplAt(out Callee, recv Type, ifaceArgs []Type) Callee {
	im, ok := out.Decl.(*ast.InterfaceMethod)
	if !ok {
		return out
	}
	base := resolveRef(out.Ref)
	b, _ := out.Impl.(*ast.ImplBlock)
	if b == nil {
		b = fa.implBlockFor(recv, base.OwningInterface, fa.declaringOrigin(im), ifaceArgs)
	}
	if b == nil {
		return out
	}
	out.Kind, out.Impl = CalleeImpl, b
	if out.For == nil {
		out.For = recv
	}
	if m := implBlockMethod(b, base.Name); m != nil {
		out.Decl = m
	}
	return fa.withDeclaringFile(out)
}

// withDeclaringFile is out with, for a `fn` it reaches, whether the
// program declares it and the path of the file that does.
func (fa *FileAnalysis) withDeclaringFile(out Callee) Callee {
	if fd, ok := out.Decl.(*ast.FuncDef); ok {
		out.InProgram = fa.inProgram(fd)
		if p := fa.Program(); p != nil {
			if declaring := p.ScopedFunctionFiles[fd]; declaring != nil {
				out.File = declaring.FilePath
			}
		}
	}
	return out
}

// withStd is out with its std identity (Callee.Std) when it reaches a
// function no program file declares, and none when a program file does:
// a record CalleeAt answers at an instance names another function than
// the record it starts from.
func (fa *FileAnalysis) withStd(out Callee) Callee {
	switch out.Kind {
	case CalleeFunction, CalleeHost, CalleeImpl, CalleeInterface:
		out.Std = StdFunc{}
		if !out.InProgram {
			out.Std = fa.StdFuncOf(out.Decl)
		}
	}
	return out
}

// CalleeAt is c, the callee the checker recorded for a call inside a
// generic body, at an instance of the body: by answers the type each of
// the body's type parameters stands for there (ReplaceTypeParams' form).
// The body is checked once, so a call of an interface function whose
// receiver's type is a type parameter (`T.area(x)`, `Shape.area(x)` with
// `x: T`, `T.from_json(j)`) records the interface's function and no impl
// (CalleeInterface, For the type parameter). At an instance where For is
// a known type, the answer is what a call written at that type records:
// For substituted, and Kind, Decl, Impl, File, InProgram and Std as
// reachedImpl fills them (interfaceImplAt), and Form as callForm does: a
// receiver with no block that meets the interface by its shape is
// answered structurally, an interface type by dispatch. c unchanged for
// any other callee, and where the receiver stays unknown: a type
// parameter by does not answer for, a type nothing answers for.
func (fa *FileAnalysis) CalleeAt(c Callee, by func(*TypeParam_) (Type, bool)) Callee {
	if fa == nil || c.Kind != CalleeInterface || c.Ref == nil || c.Impl != nil || c.For == nil {
		return c
	}
	replaced := false
	recv, all := ReplaceTypeParams(resolveTypeVar(c.For), func(p *TypeParam_) (Type, bool) {
		t, ok := by(p)
		replaced = replaced || ok
		return t, ok
	})
	if !all || !replaced {
		return c
	}
	at := c
	at.For = recv
	at = fa.callForm(fa.interfaceImplAt(at, recv, callInterfaceArgs(c.Ref, by)))
	if at.Impl == nil && (at.Form == 0 || at.Form == OperatorPerInstance) {
		return c
	}
	return fa.withStd(at)
}

// InterfaceCallee is the callee of `Iface.method(x)`, a call of a std
// interface's function on one value of type recv, that the IR builder
// writes (a tuple slot's `Hashable.hash`, a record field's
// `ToJson.to_json`, a rendering's `Debug.inspect`): what the checker
// records for that call written with x of type recv. Kind, Decl, Impl,
// File, InProgram and Std are as reachedImpl fills them
// (interfaceImplAt), and Form as callForm answers it; where recv is nil,
// a type parameter, an interface type or a type with no single block of
// the interface, the record names the interface's function, For recv. The zero Callee when the program
// does not reach iface.
func (fa *FileAnalysis) InterfaceCallee(iface StdType, method string, recv Type) Callee {
	q := fa.StdTypeQualifier(iface.Module, iface.Name)
	if fa == nil || q.Kind != QualInterface {
		return Callee{}
	}
	scope := fa.Program().StdlibModuleScopes[strings.TrimPrefix(iface.Module, "std/")]
	if scope == nil || scope.LookupLocal(iface.Name) == nil {
		return Callee{}
	}
	sym := resolveRef(scope.LookupLocal(iface.Name)).Members[method]
	if sym == nil {
		return Callee{}
	}
	out := Callee{
		Kind: CalleeInterface, Ref: sym, Decl: sym.Node, File: sym.SourceFile,
		Qualifier: q, Owner: q, Method: method, For: recv,
	}
	if recv != nil {
		out = fa.callForm(fa.interfaceImplAt(out, recv, nil))
	}
	return fa.withStd(out)
}

// callForm is out, a classified callee, with how it is answered
// (Callee.Form): by the block it reaches (interfaceImplAt), else by the
// rules an operator through the interface is answered by
// (interfaceUseFor): the receiver's shape (shapeUse), or, for a declared
// type with no block of the interface, noImplUse. Zero where none
// answers, which TestElaborationGate reports (ElabImplUnrecorded).
func (fa *FileAnalysis) callForm(out Callee) Callee {
	out.Form = 0
	switch out.Kind {
	case CalleeImpl:
		if out.Impl != nil {
			out.Form = OperatorByImpl
		}
	case CalleeInterface:
		if _, ok := out.Decl.(*ast.InterfaceMethod); !ok || out.Ref == nil {
			return out
		}
		recv := out.For
		if out.Ref.DispatchReceiver != nil {
			recv = out.Ref.DispatchReceiver
		}
		if recv == nil {
			return out
		}
		iface := resolveRef(out.Ref).OwningInterface
		if use, decided := shapeUse(iface, resolveTypeVar(ResolveClosed(recv))); decided {
			if use != nil {
				out.Form = use.Form
			}
			return out
		}
		t, _ := knownReceiver(recv)
		if t == nil || len(fa.implBlocksOf(t, iface, fa.declaringOrigin(out.Decl))) > 0 {
			return out
		}
		if use := noImplUse(iface, t); use != nil {
			out.Form = use.Form
		}
	}
	return out
}

// callInterfaceArgs is the type arguments of the interface instance ref, a
// call's reference, goes through (Symbol.CallInterface: `Holds<T>` for
// `Holds.holds(b, x)` with `x: T`), with each type parameter replaced by
// what by answers for it (by nil: none replaced). Nil when the call names
// no instance, the interface takes no arguments, or one stays open: no
// type argument narrows the blocks then.
func callInterfaceArgs(ref *Symbol, by func(*TypeParam_) (Type, bool)) []Type {
	if ref == nil || ref.CallInterface == nil || len(ref.CallInterface.TypeArgs) == 0 {
		return nil
	}
	out := make([]Type, len(ref.CallInterface.TypeArgs))
	for i, a := range ref.CallInterface.TypeArgs {
		if by != nil {
			r, all := ReplaceTypeParams(resolveTypeVar(a), by)
			if !all {
				return nil
			}
			a = r
		}
		if a == nil || ContainsTypeParam(a) || containsTypeVar(a) {
			return nil
		}
		out[i] = a
	}
	return out
}

// selfOfDefault is t, the type a call of an interface's function is for,
// with the type of a `self` value of an interface default (an
// InterfaceType marked DefaultSelf) replaced at any depth by the
// interface's SelfParam: the type parameter, named `self`, that each
// instance of the default binds to its implementor. A call on `self` in a
// default (`Shape.area(s)`) is then for the parameter an instance
// substitutes (CalleeAt), as a bare call there is (recordCallSelf).
func selfOfDefault(t Type) Type {
	var walk func(Type, int) Type
	walk = func(t Type, depth int) Type {
		if it, ok := resolveTypeVar(t).(*InterfaceType); ok && it.DefaultSelf && it.SelfParam != nil {
			return it.SelfParam
		}
		if depth > maxTypeWalkDepth {
			return t
		}
		return mapType(t, func(c Type) Type { return walk(c, depth+1) })
	}
	if t == nil {
		return nil
	}
	return walk(t, 0)
}

// holdsDefaultSelf reports whether t holds the type of a `self` value of
// an interface default at any depth.
func holdsDefaultSelf(t Type) bool {
	return t != nil && selfOfDefault(t) != t
}

// declaringOrigin is the build key of the file that declares node, a
// function, or "" when no file the program reaches does.
func (fa *FileAnalysis) declaringOrigin(node ast.Node) string {
	if f := fa.declaringFileOf(node); f != nil {
		return f.Origin
	}
	return ""
}

// knownReceiver is t, a call's receiver type, when it names one nominal
// type at the call: no type parameter, no unsolved variable, no interface
// value. A refinement is its parent, whose impls it has.
func knownReceiver(t Type) (Type, string) {
	t, name := nominalReceiver(t)
	if t == nil || ContainsTypeParam(t) || containsTypeVar(t) {
		return nil, ""
	}
	return t, name
}

// nominalReceiver is t, a receiver type, and its name when it names one
// nominal type, whatever its type arguments hold. A refinement is its
// parent, whose impls it has.
func nominalReceiver(t Type) (Type, string) {
	t = resolveTypeVar(t)
	for {
		dt := refinementOf(t)
		if dt == nil || dt.Inner == nil {
			break
		}
		t = resolveTypeVar(DistinctInner(dt))
	}
	if t == nil {
		return nil, ""
	}
	name := concreteTypeName(t)
	if name == "" {
		return nil, ""
	}
	return t, name
}
