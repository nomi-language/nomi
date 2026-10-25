package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// The orphan rule's second way in: a type argument the module declares.
//
// `impl Multiply<Rate, Decimal> for Decimal` names std's interface and std's
// receiver, so neither side is the module's own, yet the module that declares
// `Rate` may write it. Only that module can name `Rate` in an impl it owns, so
// no two modules can write the impl, and Nomi's several-instantiations rule
// keeps it apart from every other impl of `Multiply` for `Decimal`: they name
// distinct concrete type arguments, or one of them is generic and the
// collision check reports the overlap. The conditions are RFC 2451's, narrowed
// to input positions:
//
//   - The local type heads the type argument (`Rate`, `Rate<Int>`). One nested
//     in a foreign type (`List<Rate>`) does not count: the head decides who
//     may add impls in that position, so std keeps the freedom to implement an
//     interface generically over its own container there.
//   - The type argument is an input: it appears in a parameter type of one of
//     the interface's functions, so a new impl answers only calls that pass a
//     `Rate`, which no existing call does. One that appears only in results
//     would let a new impl compete for calls that already exist (`m * 2d`
//     would have two results to choose from). A type argument in both a
//     parameter and a result is an input.
//   - Neither the receiver nor an earlier type argument is a bare type
//     parameter (RFC 2451's uncovered parameter): such an impl would claim
//     every type in that position, std's own included.
//   - The impl is not generic in the local position (`Rate<T>`): an impl there
//     must name one concrete instantiation, as several-instantiations requires.
//   - The interface is not std's `Literal`. Its `I` is a parameter's, but a
//     typed literal's handler is chosen by its type alone (one impl per type,
//     §*Coherence* of typed literals), so a library's `impl Literal<Rate, ..>
//     for Date` would take `Date"2024-01-01"` away from std's impl.

// implTypeArgsLookup answers an interface impl function's resolved interface
// type arguments and the interface's declaration, or nil when either is
// unknown.
type implTypeArgsLookup func(fn *ast.FuncDef, iface, recv string) (*ImplTypeArgs, *InterfaceType)

// orphanFacts is what detectOrphanImpls reads beyond the declaring-module
// index to admit an impl through a local type argument: the impl's resolved
// type arguments, and the module a declaration's build key belongs to.
type orphanFacts struct {
	typeArgs     implTypeArgsLookup
	originModule func(origin string) string
}

// verdict says whether an impl whose interface and receiver are
// both foreign to implMod is admitted by a local type argument. When it is
// not and a type argument is local, reason says which condition failed.
func (r *orphanFacts) verdict(fn *ast.FuncDef, iface, recv, implMod string) (admitted bool, reason string) {
	if r == nil || r.typeArgs == nil || r.originModule == nil {
		return false, ""
	}
	info, it := r.typeArgs(fn, iface, recv)
	if info == nil || it == nil || len(info.Args) != len(it.TypeParamDefs) {
		return false, ""
	}
	local := func(t Type) bool {
		origin, ok := nominalOrigin(t)
		return ok && origin != OriginUnresolved && r.originModule(origin) == implMod
	}
	firstReason := ""
	for k, arg := range info.Args {
		if !local(arg) {
			continue
		}
		param := it.TypeParams[k]
		why := ""
		switch {
		case it.Name == "Literal" && it.Origin == "std/literals":
			why = fmt.Sprintf("a typed literal's handler is chosen by its type alone, so a type implements `Literal` once and `%s` cannot set a second impl apart from the first", typeArgName(arg))
		case !interfaceTakesTypeParam(it, it.TypeParamDefs[k]):
			why = fmt.Sprintf("`%s` stands in `%s`'s type parameter `%s`, which only results name, so an impl there would compete for calls that already exist; a local type admits an impl only where a parameter takes it", typeArgName(arg), iface, param)
		case ContainsTypeParam(arg):
			why = fmt.Sprintf("the impl is generic in `%s`'s type parameter `%s` (`%s`); an impl admitted by a local type argument names one concrete type there", iface, param, arg.String())
		case isBareTypeParam(info.Receiver):
			why = fmt.Sprintf("its receiver is the bare type parameter `%s`, which stands for every type, so the impl would claim std's types too; a type argument the module declares admits an impl only for a receiver that is a type", info.Receiver.String())
		default:
			for j := 0; j < k; j++ {
				if isBareTypeParam(info.Args[j]) {
					why = fmt.Sprintf("`%s`'s type parameter `%s`, before `%s`, is the bare type parameter `%s`, which stands for every type, so the impl would claim impls other modules may write; a type argument the module declares admits an impl only when no earlier type argument is a bare type parameter", iface, it.TypeParams[j], typeArgName(arg), info.Args[j].String())
					break
				}
			}
		}
		if why == "" {
			return true, ""
		}
		if firstReason == "" {
			firstReason = why
		}
	}
	if firstReason != "" {
		return false, firstReason
	}
	for _, arg := range info.Args {
		if name, ok := nestedLocalType(arg, local); ok {
			return false, fmt.Sprintf("`%s` is declared in this module but does not head the type argument `%s`; only a type argument the module's own type heads admits the impl", name, arg.String())
		}
	}
	return false, ""
}

// bareReceiver reports whether fn's impl block is for its own type
// parameter: `impl<T> Multiply<Rate, T> for T`.
func (r *orphanFacts) bareReceiver(fn *ast.FuncDef, iface, recv string) bool {
	if r == nil || r.typeArgs == nil {
		return false
	}
	info, _ := r.typeArgs(fn, iface, recv)
	return info != nil && isBareTypeParam(info.Receiver)
}

// typeArgName is the name a type argument's head is written with.
func typeArgName(t Type) string {
	switch t := t.(type) {
	case *StructType:
		return t.Name
	case *EnumType:
		return t.Name
	case *DistinctType:
		return t.Name
	}
	return t.String()
}

// interfaceTakesTypeParam reports whether p appears in a parameter type of
// one of it's functions: an input position.
func interfaceTakesTypeParam(it *InterfaceType, p *TypeParam_) bool {
	for _, m := range it.Methods {
		for _, param := range m.Params {
			if typeMentionsParam(param, p) {
				return true
			}
		}
	}
	return false
}

func isBareTypeParam(t Type) bool {
	_, ok := t.(*TypeParam_)
	return ok
}

// nestedLocalType finds a local nominal type inside t below its head.
func nestedLocalType(t Type, local func(Type) bool) (string, bool) {
	var found string
	var walk func(t Type, head bool) bool
	walk = func(t Type, head bool) bool {
		if t == nil {
			return false
		}
		if !head && local(t) {
			found = typeArgName(t)
			return true
		}
		for _, child := range typeChildren(t) {
			if walk(child, false) {
				return true
			}
		}
		return false
	}
	return found, walk(t, true)
}

// implTypeArgsIn finds the template BuildTypes recorded for fn's impl block
// in its home file, with the interface that block names.
func implTypeArgsIn(fa *FileAnalysis, fn *ast.FuncDef, iface, recv string) (*ImplTypeArgs, *InterfaceType) {
	if fa == nil {
		return nil, nil
	}
	for _, info := range fa.ImplTypeArgSets[recv][iface] {
		if info == nil || info.Block == nil {
			continue
		}
		for _, item := range info.Block.Items {
			if item != fn {
				continue
			}
			it := lookupInterfaceType(fa, nil, iface)
			if it == nil || (info.Origin != OriginUnresolved && it.Origin != info.Origin) {
				return nil, nil
			}
			return info, it
		}
	}
	return nil, nil
}
