package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// reachedImpl is out, a classified callee, with the impl block it reaches
// (Callee.Impl) when the receiver's type is known at the call, and how it
// is answered (Callee.Form): the block that declares the impl function
// the reference names or the checker chose (DispatchImpl), by that
// block; for an interface function, what interfaceImplAt answers. A call
// through a type parameter or on an interface value reaches no block
// here; an instance of its body asks CalleeAt.
func (fa *FileAnalysis) reachedImpl(out Callee) Callee {
	if fa == nil || out.Ref == nil {
		return out
	}
	switch out.Kind {
	case CalleeFunction, CalleeHost, CalleeImpl:
		info := fa.implBlockInfoHolding(out.Decl)
		if info == nil {
			return out
		}
		out.Kind, out.Impl, out.Form = CalleeImpl, info.Block, OperatorByImpl
		if out.For == nil {
			out.For = implReceiverAt(info, out.Ref)
		}
		return fa.withDeclaringFile(out)
	case CalleeInterface:
		recv := out.For
		if out.Ref.DispatchReceiver != nil {
			recv = out.Ref.DispatchReceiver
		}
		return fa.interfaceImplAt(out, recv, callInterfaceArgs(out.Ref, nil))
	}
	return out
}

// interfaceImplAt is out, a call of an interface function, at recv, its
// receiver's type, answered as interfaceUse answers a use of the function
// at recv (Callee.Form), with out's own block, the one the checker
// selected, as the block it reaches where out has one, and ifaceArgs the
// interface's type arguments at the call. Where a block answers, Kind is
// CalleeImpl, Impl that block and Decl the function that runs, the
// block's own or, where the block inherits it, the interface's. Form is
// zero where nothing answers, which TestElaborationGate reports
// (ElabImplUnrecorded), and for a callee no interface declares. The one
// resolver of an interface call's impl: reachedImpl asks it at the types
// the call is checked with, CalleeAt and InterfaceCallee at an instance's
// or the builder's.
func (fa *FileAnalysis) interfaceImplAt(out Callee, recv Type, ifaceArgs []Type) Callee {
	out.Form = 0
	im, ok := out.Decl.(*ast.InterfaceMethod)
	if !ok || out.Ref == nil {
		return out
	}
	base := resolveRef(out.Ref)
	chosen, _ := out.Impl.(*ast.ImplBlock)
	if recv == nil && chosen != nil {
		// A block the checker selected for a call no argument types
		// (`Box.holds_none(b, xs)` at one of Box's Holds impls): the call
		// is for the block's receiver.
		recv = implReceiverAt(fa.implBlockInfo(chosen), out.Ref)
	}
	use, b := fa.interfaceUse(base.OwningInterface, base.Name, fa.declaringOrigin(im), recv, ifaceArgs, chosen)
	if use != nil {
		out.Form = use.Form
	}
	if out.Form == OperatorPerInstance {
		// Each instance selects the block at its types (CalleeAt).
		out.Impl = nil
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

// implReceiverAt is the receiver of the impl block info records at a call
// of one of its functions whose reference is ref: the concrete receiver
// the checker chose the impl at (Symbol.DispatchReceiver), else the
// block's receiver with each of the block's type parameters the type the
// call instantiated it at (CallTypeArgs): `List<Int>` for `List.equal(xs,
// ys)` over two `List<Int>`, `Int` for `Int.to_string(n)`. A parameter
// the call does not fix is the block's own, since the call is in the
// block: `Route<P>` for `check(statics)` in `impl CheckedLiteral<String,
// Route<P>> for Route<P>`.
func implReceiverAt(info *ImplTypeArgs, ref *Symbol) Type {
	if ref != nil && ref.DispatchReceiver != nil {
		return ref.DispatchReceiver
	}
	if info == nil || info.Receiver == nil {
		return nil
	}
	args := CallTypeArgs(ref)
	recv, _ := ReplaceTypeParams(info.Receiver, func(p *TypeParam_) (Type, bool) {
		if t := args[p.Name_]; t != nil {
			return t, true
		}
		return p, true
	})
	return recv
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
// reachedImpl fills them, and Form, all as interfaceImplAt answers them
// (interfaceUse). c unchanged for
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
	// A type argument the checker closed (`E` of an `Ok(x)` nothing else
	// constrains) is its closed type.
	recv = Elaborate(recv)
	at := c
	at.For = recv
	at = fa.interfaceImplAt(at, recv, callInterfaceArgs(c.Ref, by))
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
// (interfaceImplAt), and Form as it answers it; where recv is nil,
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
		out = fa.interfaceImplAt(out, recv, nil)
	}
	return fa.withStd(out)
}

// callInterfaceArgs is the type arguments of the interface instance ref, a
// call's reference, goes through (Symbol.CallInterface: `Holds<T>` for
// `Holds.holds(b, x)` with `x: T`), with each type parameter replaced by
// what by answers for it (closedTypeArgs). Nil when the call names no
// instance.
func callInterfaceArgs(ref *Symbol, by func(*TypeParam_) (Type, bool)) []Type {
	if ref == nil || ref.CallInterface == nil {
		return nil
	}
	return closedTypeArgs(ref.CallInterface.TypeArgs, by)
}

// closedTypeArgs is args, an interface's type arguments at a use, with
// each type parameter replaced by what by answers for it (by nil: none
// replaced). Nil when there are none or one stays open: no type argument
// narrows the blocks then (implBlockFor).
func closedTypeArgs(args []Type, by func(*TypeParam_) (Type, bool)) []Type {
	if len(args) == 0 {
		return nil
	}
	out := make([]Type, len(args))
	for i, a := range args {
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
