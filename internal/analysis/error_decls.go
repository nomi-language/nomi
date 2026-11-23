package analysis

import "github.com/nomi-language/nomi/internal/ast"

// ErrorDeclarations is each declaration among nodes, a file's top-level
// declarations, that holds one of errs, the errors the check of that file
// reported: a function, a function of an impl block, or a `test` or
// `group`. Every other declaration of the file checked. ok is false when
// an error is in none of them (a type, an import, a `once`, an attached
// test, or another file, TypeError.File), since such an error can change
// what every body that names it means.
//
// An editor's marks read it: the lowering an editor shows a program's
// effects from leaves these declarations unlowered and lowers the rest
// (vmhost's CheckLowering), while every command still refuses the program.
func ErrorDeclarations(nodes []ast.Node, errs []TypeError) (decls map[ast.Node]bool, ok bool) {
	for _, e := range errs {
		if e.File != "" {
			return nil, false
		}
		d := declarationHolding(nodes, e.Line, e.Col)
		if d == nil {
			return nil, false
		}
		if decls == nil {
			decls = map[ast.Node]bool{}
		}
		decls[d] = true
	}
	return decls, true
}

// declarationHolding is the function, impl function, test or group among
// nodes whose span holds line and col, or nil.
func declarationHolding(nodes []ast.Node, line, col int) ast.Node {
	holds := func(s ast.Span) bool {
		if s.StartLine == 0 {
			return false
		}
		after := line > s.StartLine || line == s.StartLine && col >= s.StartCol
		before := line < s.EndLine || line == s.EndLine && col < s.EndCol
		return after && before
	}
	for _, n := range nodes {
		switch d := n.(type) {
		case *ast.FuncDef:
			if holds(d.Span) {
				return d
			}
		case *ast.TestDecl:
			if holds(d.Span) {
				return d
			}
		case *ast.ImplBlock:
			for _, item := range d.Items {
				if fd, isFn := item.(*ast.FuncDef); isFn && holds(fd.Span) {
					return fd
				}
			}
		}
	}
	return nil
}
