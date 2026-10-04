package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// A std declaration inside `impl Hashable for Map<K, V>` is GENERIC, even
// though its block header declares no type parameters of its own.
//
// # Why the block header is not enough
//
// `stdCandidateFor` orders its arms `case generic:` BEFORE `case blocked:`, so
// the ordering is the whole discrimination between "the type has no
// representation" and "this is generic over a type parameter".
//
// `*ast.ImplBlock.Generics` is POPULATED for a derive-synthesized block and
// EMPTY for a hand-written one:
//
//	derive Debug for Channel<T>       Generics=[{T}]
//	impl Hashable for Map<K, V>       Generics=[]
//
// A test on the header alone would file the hand-written ones under `stdlib
// function outside the scalar subset`, over a signature position holding a
// type PARAMETER, which no anchor family can ever anchor. The declarations in
// question include:
//
//	lists.List.{Add.add, compare, each_while, equal?, hash, inspect, known_count, to_string}
//	maps.Map.{each_while, equal?, hash, inspect, known_count, to_string}
//	sets.Set.{each_while, equal?, hash, inspect, known_count, to_string}
//	vectors.Vector.{Add.add, compare, each_while, equal?, hash, inspect, known_count, to_string}
//	ranges.Range.{inspect, to_string}
//	iter.Seq.each_while
//
// # Per SIGNATURE, not per BLOCK
//
// Marking the whole BLOCK generic when its receiver contributes a type
// parameter is what the front end's scope does, and it would be WRONG here.
// `impl Range<T>` contains
//
//	pub fn from(n: Int): Range<Int>
//	pub fn naturals(): Range<Int>
//
// which mention no type parameter at all: `T` is merely IN SCOPE. Marking the
// block generic would move those two onto `stdlib generic function`, a wrong
// reason. They are instead admitted by `genStructSigKind`.
//
// So the rule is the strictly narrower one: a signature that MENTIONS a type
// parameter is generic. It is also sound in the direction that matters: it can
// only ever move a declaration that would be refused anyway, never one that
// lowers. `stdTypeKind` has no arm for a bare `T`, and none for a container
// over one, so any signature mentioning a receiver type parameter has
// `kindInvalid` at that position regardless.
//
// The block-level test for an EXPLICIT `Generics` header stays as it is. It is
// coarser than this rule, and no declaration changes key on account of it.

// sigNamesTypeParam reports whether any position of this signature names one of
// the given type parameters.
//
// A parameter with no annotation is not a mention: `stdParamKind` already
// refuses it as `kindInvalid` on its own terms, and calling that generic would
// blame the dictionary for a missing annotation.
func sigNamesTypeParam(params []ast.Param, ret ast.TypeExpr, tp map[string]bool) bool {
	if len(tp) == 0 {
		return false
	}
	for _, p := range params {
		if typeNamesAny(p.TypeAnnotation, tp) {
			return true
		}
	}
	return typeNamesAny(ret, tp)
}

// typeNamesAny reports whether a type expression names any of `tp`, at any
// depth.
//
// Every composite form is walked rather than only the head, because the mis-keyed
// population is dominated by CONTAINERS over a parameter — `List<T>`,
// `Map<K, V>`, `(T) -> Bool`, `((K, V)) -> Bool` — and a head-only test would
// have caught none of them.
func typeNamesAny(te ast.TypeExpr, tp map[string]bool) bool {
	if te == nil || isNilNode(te) {
		return false
	}
	switch t := te.(type) {
	case *ast.SimpleType:
		return tp[t.Name]
	case *ast.GenericType:
		if tp[t.Name] {
			return true
		}
		for _, p := range t.Params {
			if typeNamesAny(p, tp) {
				return true
			}
		}
	case *ast.QualifiedType:
		// The MEMBER only: the module qualifier is a file name, never a type
		// parameter, and a parameter cannot be qualified.
		return typeNamesAny(t.Member, tp)
	case *ast.FuncType:
		for _, p := range t.Params {
			if typeNamesAny(p, tp) {
				return true
			}
		}
		return typeNamesAny(t.Return, tp)
	case *ast.AnonStructType:
		for _, f := range t.Fields {
			if typeNamesAny(f.TypeAnnotation, tp) {
				return true
			}
		}
	}
	return false
}
