package analysis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// The keywords a declaration by the general rule (ast.KeywordDecl) may
// name. `message` expands into the function it declares and `noun` into
// the `once` it names (ExpandDeclarations). Any other keyword is a
// macro's, which a file imports (macro_expand.go).
const (
	keywordMessage = "message"
	keywordNoun    = "noun"
)

// isBuiltinKeyword reports whether kd is written with a keyword Nomi
// builds in, `message` or `noun`, which is never qualified.
func isBuiltinKeyword(kd *ast.KeywordDecl) bool {
	return kd.Module == "" && (kd.Keyword == keywordMessage || kd.Keyword == keywordNoun)
}

// unmarkedMacroError is the error at a macro's keyword written
// without its `!`.
func unmarkedMacroError(n *ast.KeywordDecl) TypeError {
	return TypeError{Line: n.Line, Col: n.StartCol(), EndLine: n.Line, EndCol: n.KeywordEndCol(),
		Message: fmt.Sprintf("`%s` is a macro, so it is written `%s!`", n.QualifiedKeyword(), n.QualifiedKeyword())}.
		WithHint("the `!` tells a reader that a library expands the declaration; only `message` and `noun`, which Nomi builds in, are written without it")
}

// unexpandedKeywordError is the error at a keyword declaration nothing
// expanded: one whose keyword Nomi does not know and no import brings, one
// qualified by a file that declares no such macro, or one whose
// macro cannot run where the file is.
func (c *checker) unexpandedKeywordError(n *ast.KeywordDecl) TypeError {
	e := TypeError{Line: n.Line, Col: n.StartCol(), EndLine: n.Line, EndCol: n.KeywordEndCol()}
	notExpanded := fmt.Sprintf("this `%s` declaration was not expanded: a macro expands declarations in a module, a directory with a nomi.toml whose go.mod requires the macro's module", n.QualifiedKeyword())
	if n.Module != "" {
		mod := moduleScopeOfSym(c.fa.ModuleScope.Lookup(n.Module))
		if mod == nil {
			e.Message = fmt.Sprintf("`%s` names no imported file, so `%s` names no macro", n.Module, n.QualifiedKeyword())
			return e.WithHint(fmt.Sprintf("a macro's keyword is qualified by the file that declares it, imported whole: `import %s`", n.Module))
		}
		if sym := mod.LookupLocal(n.Keyword); sym != nil && sym.Kind == SymbolMacro && sym.Public {
			if !n.Marked {
				return unmarkedMacroError(n)
			}
			e.Message = notExpanded
			return e
		}
		var names []string
		for name, sym := range mod.Symbols {
			if sym.Kind == SymbolMacro && sym.Public {
				names = append(names, "`"+name+"`")
			}
		}
		sort.Strings(names)
		for i, name := range names {
			names[i] = strings.TrimSuffix(name, "`") + "!`"
		}
		e.Message = fmt.Sprintf("file `%s` has no macro `%s`", n.Module, n.Keyword)
		switch len(names) {
		case 0:
			return e.WithHint(fmt.Sprintf("`%s` declares no `pub macro`", n.Module))
		case 1:
			return e.WithHint(fmt.Sprintf("its macro is %s", names[0]))
		default:
			return e.WithHint(fmt.Sprintf("its macros are %s and %s", strings.Join(names[:len(names)-1], ", "), names[len(names)-1]))
		}
	}
	switch n.Keyword {
	case keywordMessage, keywordNoun:
		// LowerDerives expands every message and noun; one that reaches the
		// checker is a defect of the path that prepared its file.
		e.Message = fmt.Sprintf("this `%s` declaration was not expanded, a defect in Nomi", n.Keyword)
		return e
	}
	if sym := c.fa.ModuleScope.Lookup(n.Keyword); sym != nil && sym.Kind == SymbolMacro {
		if !n.Marked {
			return unmarkedMacroError(n)
		}
		e.Message = notExpanded
		return e
	}
	e.Message = fmt.Sprintf("unknown declaration keyword `%s`", n.QualifiedKeyword())
	return e.WithHint(fmt.Sprintf("a declaration that starts with a keyword and a name is written with a keyword Nomi knows, `message` or `noun`, or with a macro's, marked with `!` and qualified by the file that declares it, `mylib.%s!` after `import mylib`, or imported by name, `%s!` after `import mylib.%s`", n.Keyword, n.Keyword, n.Keyword))
}

// keywordImportFailed reports whether the import that would bring n's
// keyword is written and did not resolve: the qualifier's whole-file
// import, or the item import of a bare keyword. Its error at the import
// says what is missing.
func (c *checker) keywordImportFailed(n *ast.KeywordDecl, nodes []ast.Node) bool {
	if n.Module != "" {
		_, written := fileImportNamed(nodes, n.Module)
		return written && moduleScopeOfSym(c.fa.ModuleScope.Lookup(n.Module)) == nil
	}
	_, _, written := importedMacro(nodes, n.Keyword)
	sym := c.fa.ModuleScope.Lookup(n.Keyword)
	// An item import that resolved nothing binds its name to no symbol.
	return written && (sym == nil || sym.Resolved == nil && sym.Kind == SymbolBinding)
}

// checkMacroDecl checks `macro name: Type`: the type is one the
// file declares, and the file implements std/macro's `Macro` for
// it, which is what the compiler runs.
func (c *checker) checkMacroDecl(n *ast.MacroDecl, nodes []ast.Node) {
	if _, err := ResolveTypeExpr(n.Impl, c.reg, nil, c.fa.References); err != nil {
		c.errors = append(c.errors, errAt(n.Impl, err.Error()))
		return
	}
	impl := TypeExprBaseName(n.Impl)
	for _, node := range nodes {
		b, ok := node.(*ast.ImplBlock)
		if ok && b.Interface != nil && TypeExprBaseName(b.Interface) == "Macro" && TypeExprBaseName(b.Receiver) == impl {
			return
		}
	}
	c.errors = append(c.errors, errAt(n.Impl, fmt.Sprintf("`%s` implements no `Macro` in this file, so the macro `%s` has nothing to run", impl, n.Name)).
		WithHint(fmt.Sprintf("write `impl Macro for %s { fn expand(call: ExpandCall) -> Expansion { ... } }` beside the declaration, with `Macro` from std/macro", impl)))
}
