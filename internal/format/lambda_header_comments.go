package format

import (
	"reflect"

	"github.com/nomi-language/nomi/internal/ast"
)

// gatherLambdaHeaderComments moves each comment written between a lambda's
// parameters that the parser left on a node after it to the lambda body's
// DanglingBefore, where emitLambdaHeader writes it back after the parameter
// it followed.
//
// The tree has no slot for a comment after a parameter's comma, `|acc, //
// why` then `frag| body`. The parser hangs it on the nearest node
// (parser.attachDangling): after the parameter's own pattern, annotation or
// default when it has one, which emitLambdaHeader handles where it writes
// that parameter. A bare name is no node, so after one the comment goes
// before the next node: a later parameter's pattern, annotation or default,
// or the body. Written there it would land after the closing `|` or on a
// line of its own before the body, and the parser takes a comment in
// neither place.
func gatherLambdaHeaderComments(nodes []ast.Node) {
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Ptr:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if lam, ok := v.Interface().(*ast.Lambda); ok {
				gatherHeaderComments(lam)
			}
			walk(v.Elem())
		case reflect.Struct:
			if v.Type() == reflect.TypeOf(ast.Trivia{}) {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	for _, n := range nodes {
		walk(reflect.ValueOf(n))
	}
}

// gatherHeaderComments moves lam's header comments that sit before its last
// parameter, the only place a comment in the header can be, to its body's
// DanglingBefore.
func gatherHeaderComments(lam *ast.Lambda) {
	if len(lam.Params) < 2 || lam.Body == nil {
		return
	}
	last := lam.Params[len(lam.Params)-1]
	var holders []ast.Node
	for _, p := range lam.Params {
		holders = append(holders, p.Destructure, p.TypeAnnotation, p.Default)
	}
	if len(lam.Body.Stmts) > 0 {
		first := lam.Body.Stmts[0]
		holders = append(holders, first)
		if es, ok := first.(*ast.ExprStmt); ok {
			holders = append(holders, es.Expr)
		}
	}
	var moved []ast.Trivia
	for _, h := range holders {
		f := danglingBeforeField(h)
		if !f.IsValid() {
			continue
		}
		var keep []ast.Trivia
		for _, t := range f.Interface().([]ast.Trivia) {
			if paramStartsBefore(last, t) {
				keep = append(keep, t)
			} else {
				moved = append(moved, t)
			}
		}
		if len(keep) < f.Len() {
			f.Set(reflect.ValueOf(keep))
		}
	}
	for _, t := range moved {
		lam.Body.AddDanglingBefore(t)
	}
}

// danglingBeforeField is n's settable DanglingBefore, or the zero Value when
// n is nil or carries none.
func danglingBeforeField(n ast.Node) reflect.Value {
	if n == nil {
		return reflect.Value{}
	}
	v := reflect.ValueOf(n)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return reflect.Value{}
	}
	f := v.Elem().FieldByName("DanglingBefore")
	if !f.IsValid() || !f.CanSet() || f.Type() != reflect.TypeOf([]ast.Trivia(nil)) {
		return reflect.Value{}
	}
	return f
}

// paramStartsBefore reports whether p starts before the comment t.
func paramStartsBefore(p ast.Param, t ast.Trivia) bool {
	return p.Line < t.Line || p.Line == t.Line && p.Col < t.Col
}

// paramHasComment reports whether any node of p (its pattern, annotation
// or default) holds a comment.
func paramHasComment(p ast.Param) bool {
	return valueHasComment(reflect.ValueOf(p), map[uintptr]bool{})
}

// typeHasComment reports whether any node of t holds a comment.
func typeHasComment(t ast.TypeExpr) bool {
	return t != nil && valueHasComment(reflect.ValueOf(t), map[uintptr]bool{})
}

func valueHasComment(v reflect.Value, seen map[uintptr]bool) bool {
	switch v.Kind() {
	case reflect.Interface:
		return !v.IsNil() && valueHasComment(v.Elem(), seen)
	case reflect.Ptr:
		if v.IsNil() || seen[v.Pointer()] {
			return false
		}
		seen[v.Pointer()] = true
		return valueHasComment(v.Elem(), seen)
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(ast.Trivia{}) {
			return v.Interface().(ast.Trivia).Kind == ast.TriviaComment
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() && valueHasComment(v.Field(i), seen) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if valueHasComment(v.Index(i), seen) {
				return true
			}
		}
	}
	return false
}
