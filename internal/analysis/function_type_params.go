package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// functionTypeParamErrors checks a function's own `<...>` header against its
// resolved signature. Every function form calls it: a file-level or nested
// `fn`, an impl block's `fn`, a `host fn`, and an interface function.
//
// A type parameter's name must be PascalCase. A lowercase name can never
// appear in a type, so the error says how to spell it and stops there.
//
// A type argument is solved at a call from the arguments and the expected
// result, so a type parameter no parameter type, the return type, or a
// `where` bound on one of those mentions can never be solved, and the IR
// builder, which instantiates a generic function per solved tuple, has
// nothing to instantiate it at. Such a parameter is an error at its
// declaration rather than at each call: a call could name it with a
// turbofish, but the declaration is what is wrong.
//
// params, ret and where are the resolved signature; a nil param or return
// type means its annotation already failed, and the check is skipped rather
// than guessing.
func functionTypeParamErrors(fnName string, declared []ast.TypeParam, explicit map[string]*TypeParam_, params []Type, ret Type, where []WhereBound) []TypeError {
	if len(declared) == 0 {
		return nil
	}
	var errs []TypeError
	for _, tp := range declared {
		if isTypeParamName(tp.Name) {
			continue
		}
		errs = append(errs, TypeError{
			Line: tp.Line, Col: tp.Col,
			Message: fmt.Sprintf("type parameter %q must be PascalCase", tp.Name),
			Hints:   []string{"a type parameter is a type name, so it starts with an uppercase letter, such as `T`"},
		})
	}
	if ret == nil {
		return errs
	}
	for _, p := range params {
		if p == nil {
			return errs
		}
	}
	used := map[*TypeParam_]bool{}
	var mark func(t Type, depth int) bool
	mark = func(t Type, depth int) bool {
		if t == nil || depth > 32 {
			return false
		}
		if p, ok := t.(*TypeParam_); ok {
			if used[p] {
				return false
			}
			used[p] = true
			return true
		}
		added := false
		for _, a := range typeChildren(t) {
			if mark(a, depth+1) {
				added = true
			}
		}
		return added
	}
	for _, p := range params {
		mark(p, 0)
	}
	mark(ret, 0)
	// A type parameter a bound projects (`where I: Iter<T>`) is solved once
	// the bounded one is, so it counts as used when the bounded one does.
	// The bounds a type parameter carries itself (an impl block's `where`,
	// or its type declaration's) project the same way.
	for changed := true; changed; {
		changed = false
		for p := range used {
			for _, b := range p.Bounds {
				for _, a := range b.TypeArgs {
					if mark(a, 0) {
						changed = true
					}
				}
			}
		}
		for _, wb := range where {
			if !used[wb.Param] {
				continue
			}
			for _, b := range wb.Bounds {
				for _, a := range b.TypeArgs {
					if mark(a, 0) {
						changed = true
					}
				}
			}
		}
	}
	for _, tp := range declared {
		if !isTypeParamName(tp.Name) {
			continue
		}
		p := explicit[tp.Name]
		if p == nil || used[p] {
			continue
		}
		errs = append(errs, TypeError{
			Line: tp.Line, Col: tp.Col,
			Message: fmt.Sprintf("type parameter `%s` is not used in the signature of `%s`, so no call can determine it", tp.Name, fnName),
			Hints:   []string{fmt.Sprintf("remove `%s` from the type parameters, or use it in a parameter's type or the return type", tp.Name)},
		})
	}
	return errs
}
