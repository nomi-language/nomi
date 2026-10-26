package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// What a program cannot name in std. Two kinds of declaration there serve
// std itself and the code a keyword declaration expands into, and nothing
// a program writes:
//
//   - A std file's private declaration (one without `pub`). A program
//     naming one gets "`message.compiled` is internal to std/message"
//     rather than the "has no member" another file's private name gets,
//     since the name is std's and the reader may have seen it in a
//     generated body. An expansion reaches std/message's private
//     constructors (`message$.compiled`, `message$.integer`) through its
//     hidden import, whose alias holds a `$` that no source can write
//     (isHiddenImportAlias), and so does nothing else.
//   - An internal std module (internalStdModules): the Go-backed half of a
//     module a program does use. Only std files and expansions import one.
//     The reference leaves both out (cmd/nomi-docgen).

// internalStdModules maps each internal std module to the std module it
// serves, which is what a program uses instead.
var internalStdModules = map[string]string{
	"fs_host":        "fs",
	"message_format": "message",
	"money_format":   "money",
}

// InternalStdModule reports whether the std module name (`money_format`) is
// internal, and the module a program uses instead (`money`).
func InternalStdModule(name string) (owner string, ok bool) {
	owner, ok = internalStdModules[name]
	return owner, ok
}

// isHiddenImportAlias reports whether alias names a hidden import an
// expansion added (`message$`): no source can write a `$`.
func isHiddenImportAlias(alias string) bool {
	return strings.Contains(alias, "$")
}

// internalToStd is the error for a program naming what, a private
// declaration of the std module std.
func internalToStd(what, std string) string {
	return fmt.Sprintf("`%s` is internal to std/%s", what, std)
}

// checkInternalStdImport reports an import of an internal std module from
// a file outside std, unless an expansion added it.
func (c *checker) checkInternalStdImport(n *ast.ImportStmt) {
	if c == nil || c.fa == nil || n == nil || IsStdlibKey(c.fa.Origin) {
		return
	}
	if n.ModuleAlias != nil && isHiddenImportAlias(ast.ImportNodeName(n.ModuleAlias)) {
		return
	}
	segs := n.ModulePath
	if n.FileSegments > 0 && n.FileSegments <= len(segs) {
		segs = segs[:n.FileSegments]
	}
	if len(segs) != 2 || ast.ImportNodeName(segs[0]) != "std" {
		return
	}
	name := ast.ImportNodeName(segs[1])
	owner, ok := internalStdModules[name]
	if !ok {
		return
	}
	c.report(errAt(segs[1], fmt.Sprintf("`std/%s` is internal to std/%s", name, owner)).
		WithHint(fmt.Sprintf("import std/%s and use its functions", owner)))
}

// stdModuleOf answers the std module (`message`) the file API object obj
// names, when it names one.
func (c *checker) stdModuleOf(obj ast.Node) (string, bool) {
	var pos Pos
	switch o := obj.(type) {
	case *ast.Ident:
		pos = Pos{Line: o.Line, Col: o.Col}
	case *ast.TypeIdent:
		pos = Pos{Line: o.Line, Col: o.Col}
	default:
		return "", false
	}
	return stdModuleOfSymbol(c.fa.References[pos], c.fa.Origin)
}

// stdModuleOfSymbol answers the std module a module symbol's import names,
// in a file of origin.
func stdModuleOfSymbol(sym *Symbol, origin string) (string, bool) {
	if sym == nil {
		return "", false
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	imp, ok := sym.Node.(*ast.ImportStmt)
	if !ok || sym.Kind != SymbolModule {
		return "", false
	}
	return stdModuleOfImportIn(imp, origin)
}

// stdModuleOfImport answers the std module the import n reads.
func (c *checker) stdModuleOfImport(n *ast.ImportStmt) (string, bool) {
	return stdModuleOfImportIn(n, c.fa.Origin)
}

func stdModuleOfImportIn(n *ast.ImportStmt, origin string) (string, bool) {
	segs := n.ModulePath
	if n.FileSegments > 0 && n.FileSegments <= len(segs) {
		segs = segs[:n.FileSegments]
	}
	names := make([]string, len(segs))
	for i, seg := range segs {
		names[i] = ast.ImportNodeName(seg)
	}
	names = QualifyIntraStdlibImport(origin, names)
	if len(names) < 2 || names[0] != "std" {
		return "", false
	}
	return strings.Join(names[1:], "/"), true
}
