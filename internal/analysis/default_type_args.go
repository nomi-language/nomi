package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// Default type arguments (spec §4 *Default type arguments*). A type
// declaration's parameter may name a default, `Route<P = Unit>`, which a
// use in a type position that leaves the argument out takes: `Route` is
// `Route<Unit>`. The parser holds the parameters with a default after the
// others; a default names no type parameter, so it is one type, resolved
// where the declaration is, and kept on the parameter's TypeParam_
// (Default), which every instance shares through TypeParamDefs.
//
// A use can be resolved before its declaration is built (unbuilt_decl.go).
// Then the parameters are known only from the declaration's syntax, which
// its shell keeps (unbuiltDecl.params): the use becomes an instance with the
// arguments written, and building the declaration appends the defaults
// (completeDefaultArgs).

// newUnbuilt is the unbuiltDecl of a declaration with type parameters tps.
func newUnbuilt(tps []ast.TypeParam) *unbuiltDecl {
	return &unbuiltDecl{params: tps}
}

// resolveTypeParamDefaults gives each parameter of tps that names a default
// its type, on the parameter's own TypeParam_ in tp. name is the
// declaration's, whose shell is not built yet.
func resolveTypeParamDefaults(fa *FileAnalysis, reg *TypeRegistry, name string, tps []ast.TypeParam, tp map[string]*TypeParam_) []TypeError {
	var errs []TypeError
	for _, p := range tps {
		if p.Default == nil {
			continue
		}
		if name, ok := namedTypeParam(p.Default, tps); ok {
			errs = append(errs, TypeError{Line: p.Line, Col: p.Col, Message: fmt.Sprintf(
				"a type parameter's default can't name a type parameter (`%s`)", name)})
			continue
		}
		resolved, err := ResolveDeclaredType(p.Default, reg, nil, fa)
		if err != nil {
			if te, ok := err.(TypeError); ok {
				errs = append(errs, te)
			}
			continue
		}
		if def := tp[p.Name]; def != nil {
			def.Default = resolved
		}
	}
	return append(errs, rejectEndlessDefaults(reg.Lookup(name), name, tps, tp)...)
}

// A default that names its own declaration and leaves an argument to a
// default, `struct Box<T = Box>`, would expand forever: `Box` is
// `Box<Box>`, which is `Box<Box<Box>>`, and so on. So would one that comes
// back through another declaration's default (`A<T = B>`, `B<U = A>`) or
// through another of its own parameters' defaults. A use that writes every
// argument, `Box<T = List<Box<Int>>>`, takes no default and is an ordinary
// type.
//
// A use resolved before its declaration is built is an instance that
// building completes with the defaults (unbuilt_decl.go). Completing an
// instance with a default that contains the instance is what makes the
// expansion endless, as a cycle of type arguments, so this looks for
// exactly that while the declaration's defaults are resolved and it is not
// built yet: a default that reaches, through type arguments, an instance of
// the declaration that leaves out a parameter whose default leads back to
// it. A cycle through several declarations closes at the build of the last
// of them, since the instances on the way are completed by then, so the
// error is at that one's default.

// rejectEndlessDefaults is the error at each parameter of the declaration
// self whose default expands forever. The parameter's default becomes Unit,
// so building self makes no cycle.
func rejectEndlessDefaults(self Type, name string, tps []ast.TypeParam, tp map[string]*TypeParam_) []TypeError {
	_, u, _ := nominalParts(self)
	if u == nil {
		return nil
	}
	// leaves[i] is each parameter that a use of self in i's default leaves
	// to its default, with the declarations it is reached through.
	type leave struct {
		param int
		via   []string
	}
	leaves := make([][]leave, len(tps))
	for i, p := range tps {
		def := tp[p.Name]
		if def == nil || def.Default == nil {
			continue
		}
		seen := map[Type]bool{}
		var walk func(t Type, via []string)
		walk = func(t Type, via []string) {
			if t == nil || seen[t] {
				return
			}
			seen[t] = true
			switch tt := t.(type) {
			case *ListType:
				walk(tt.Elem, via)
			case *MapType:
				walk(tt.Key, via)
				walk(tt.Val, via)
			case *TupleType:
				for _, e := range tt.Elems {
					walk(e, via)
				}
			case *FuncType:
				for _, e := range tt.Params {
					walk(e, via)
				}
				walk(tt.Return, via)
			}
			args, iu, ok := nominalParts(t)
			if !ok {
				return
			}
			if iu == u {
				for j := len(args); j < len(tps); j++ {
					leaves[i] = append(leaves[i], leave{j, via})
				}
			}
			_, _, defs := typeParamShape(t)
			for k, a := range args {
				// An argument that is its parameter's default came from
				// the declaration's default, which the path goes through.
				if k < len(defs) && defs[k] != nil && defs[k] == a {
					walk(a, append(append([]string(nil), via...), nominalName(t)))
				} else {
					walk(a, via)
				}
			}
		}
		walk(def.Default, nil)
	}
	var errs []TypeError
	for i, p := range tps {
		// Does i's default lead back to i, directly or through the
		// defaults of the parameters the uses on the way leave out?
		var via []string
		found := false
		visited := map[int]bool{}
		var reach func(k int, path []string)
		reach = func(k int, path []string) {
			for _, l := range leaves[k] {
				if found {
					return
				}
				next := append(append([]string(nil), path...), l.via...)
				if l.param == i {
					found, via = true, next
					return
				}
				if !visited[l.param] {
					visited[l.param] = true
					reach(l.param, next)
				}
			}
		}
		reach(i, nil)
		if !found {
			continue
		}
		msg := fmt.Sprintf("`%s`'s default for `%s` names `%s` itself, which would never end; write the argument where `%s` is used",
			name, p.Name, name, name)
		if len(via) > 0 {
			msg = fmt.Sprintf("`%s`'s default for `%s` names `%s`, whose default names `%s` again, which would never end; write the argument where `%s` is used",
				name, p.Name, via[0], name, via[0])
		}
		errs = append(errs, TypeError{Line: p.Line, Col: p.Col, Message: msg})
		tp[p.Name].Default = TypeUnit
	}
	return errs
}

// nominalParts answers the type arguments of a struct, enum, distinct type
// or interface, and the unbuiltDecl it shares with its declaration while
// that is not built.
func nominalParts(t Type) (args []Type, u *unbuiltDecl, ok bool) {
	switch n := t.(type) {
	case *StructType:
		return n.TypeArgs, n.unbuilt, true
	case *EnumType:
		return n.TypeArgs, n.unbuilt, true
	case *DistinctType:
		return n.TypeArgs, n.unbuilt, true
	case *InterfaceType:
		return n.TypeArgs, n.unbuilt, true
	}
	return nil, nil, false
}

// nominalName is the declared name of a struct, enum, distinct type or
// interface.
func nominalName(t Type) string {
	switch n := t.(type) {
	case *StructType:
		return n.Name
	case *EnumType:
		return n.Name
	case *DistinctType:
		return n.Name
	case *InterfaceType:
		return n.Name
	}
	return ""
}

// namedTypeParam answers the first of tps that te names.
func namedTypeParam(te ast.TypeExpr, tps []ast.TypeParam) (string, bool) {
	found := ""
	ast.Inspect(te, func(n ast.Node) bool {
		if st, ok := n.(*ast.SimpleType); ok && found == "" {
			for _, p := range tps {
				if p.Name == st.Name {
					found = st.Name
				}
			}
		}
		return found == ""
	})
	return found, found != ""
}

// rejectFunctionTypeParamDefaults is the error at each of a function's
// type parameters that names a default: a call infers them.
func rejectFunctionTypeParamDefaults(tps []ast.TypeParam) []TypeError {
	var errs []TypeError
	for _, p := range tps {
		if p.Default != nil {
			errs = append(errs, TypeError{Line: p.Line, Col: p.Col,
				Message: fmt.Sprintf("a function's type parameter can't have a default; a call infers `%s`", p.Name)})
		}
	}
	return errs
}

// typeParamShape answers how many type parameters the declaration t is (or
// is an instance of) takes, how many of them have no default, and the
// defaults, nil where a parameter has none or the declaration is not built
// yet.
func typeParamShape(t Type) (params, required int, defaults []Type) {
	var defs []*TypeParam_
	var u *unbuiltDecl
	switch n := t.(type) {
	case *StructType:
		defs, u = n.TypeParamDefs, n.unbuilt
	case *EnumType:
		defs, u = n.TypeParamDefs, n.unbuilt
	case *DistinctType:
		defs, u = n.TypeParamDefs, n.unbuilt
	case *InterfaceType:
		defs, u = n.TypeParamDefs, n.unbuilt
	default:
		return 0, 0, nil
	}
	if u != nil {
		for _, p := range u.params {
			if p.Default == nil {
				required++
			}
		}
		return len(u.params), required, nil
	}
	defaults = make([]Type, len(defs))
	for i, d := range defs {
		if d != nil && d.Default != nil {
			defaults[i] = d.Default
		} else {
			required++
		}
	}
	return len(defs), required, defaults
}

// withDefaultArgs answers args with the defaults of the parameters after
// them appended, when the declaration t is built; an unbuilt one's
// instance is completed when it is built.
func withDefaultArgs(t Type, args []Type) []Type {
	params, _, defaults := typeParamShape(t)
	if defaults == nil || len(args) >= params {
		return args
	}
	out := append([]Type(nil), args...)
	for i := len(args); i < params; i++ {
		if defaults[i] == nil {
			return args
		}
		out = append(out, defaults[i])
	}
	return out
}

// takesDefaultArgs reports whether a use of t that writes got arguments is
// filled by defaults: t takes more, and those it leaves out have them.
func takesDefaultArgs(t Type, got int) bool {
	params, required, _ := typeParamShape(t)
	return got < params && got >= required
}

// completeDefaultArgs appends to an instance taken before its declaration
// was built the defaults of the arguments it leaves out.
func completeDefaultArgs(args []Type, defs []*TypeParam_) []Type {
	if len(args) >= len(defs) {
		return args
	}
	out := append([]Type(nil), args...)
	for _, d := range defs[len(args):] {
		if d == nil || d.Default == nil {
			return args
		}
		out = append(out, d.Default)
	}
	return out
}

// shownTypeArgs is args with the trailing ones that are their parameter's
// default left out, as a type prints: `Route<Unit>` is `Route`.
func shownTypeArgs(args []Type, defs []*TypeParam_) []Type {
	n := len(args)
	for n > 0 && n <= len(defs) {
		d := defs[n-1]
		if d == nil || d.Default == nil || args[n-1] == nil || !TypesEqual(args[n-1], d.Default) {
			break
		}
		n--
	}
	return args[:n]
}

// instantiateNominal is the instance of the declaration t at args.
func instantiateNominal(t Type, args []Type) Type {
	switch bt := t.(type) {
	case *EnumType:
		inst := &EnumType{
			Origin:           bt.Origin,
			Name:             bt.Name,
			TypeArgs:         args,
			Variants:         bt.Variants,
			TypeParams:       bt.TypeParams,
			TypeParamDefs:    bt.TypeParamDefs,
			Opaque:           bt.Opaque,
			OwningSourceFile: bt.OwningSourceFile,
		}
		inst.unbuilt = bt.unbuilt.recordInstance(inst)
		return inst
	case *StructType:
		inst := &StructType{
			Origin:           bt.Origin,
			Name:             bt.Name,
			TypeArgs:         args,
			Fields:           bt.Fields,
			TypeParams:       bt.TypeParams,
			TypeParamDefs:    bt.TypeParamDefs,
			Opaque:           bt.Opaque,
			OwningSourceFile: bt.OwningSourceFile,
		}
		inst.unbuilt = bt.unbuilt.recordInstance(inst)
		return inst
	case *InterfaceType:
		inst := &InterfaceType{
			Origin:        bt.Origin,
			Name:          bt.Name,
			Methods:       bt.Methods,
			TypeParams:    bt.TypeParams,
			TypeParamDefs: bt.TypeParamDefs,
			TypeArgs:      args,
			SelfParam:     bt.SelfParam,
		}
		inst.unbuilt = bt.unbuilt.recordInstance(inst)
		return inst
	case *DistinctType:
		// A generic distinct (`type Tagged<T> (String, T)`) or
		// generic opaque extern (`host type Task<T>`):
		// instantiate the shell with the resolved TypeArgs so
		// unification at call sites reaches the inner T. The
		// TypeParamDefs pointer is shared with the registry
		// shell, so substitution in checkGenericCall solves the
		// right TypeParam_. An instance taken before the
		// declaration is built is completed by distinctBuilt.
		inst := &DistinctType{
			Origin:           bt.Origin,
			Name:             bt.Name,
			Inner:            bt.Inner,
			Opaque:           bt.Opaque,
			OwningSourceFile: bt.OwningSourceFile,
			TypeParams:       bt.TypeParams,
			TypeParamDefs:    bt.TypeParamDefs,
			TypeArgs:         args,
		}
		inst.unbuilt = bt.unbuilt.recordInstance(inst)
		return inst
	}
	return t
}

// arityRangeMessage is arityMessage for a declaration some of whose
// parameters have defaults: `Pair` expects 1 to 2 type arguments.
func arityRangeMessage(name string, required, params, got int) string {
	if required == params {
		return arityMessage(name, params, got)
	}
	return fmt.Sprintf("`%s` expects %d to %d type arguments, got %d", name, required, params, got)
}
