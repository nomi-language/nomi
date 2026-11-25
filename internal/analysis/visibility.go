package analysis

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Sees reports whether this file may name sym (spec §14 *Visibility*). It
// is the one answer to that question: name lookup through a file's
// qualifier, an import by name, a re-export, a type's functions reached
// from another file, completion and auto-import all ask it.
//
//   - A public declaration is seen from every file.
//   - An internal one, a declaration with no modifier, from every file of
//     its package (Symbol.Package, FileAnalysis.Package).
//   - A private one from its own file alone (inside).
//
// An imported name is judged by the declaration it imports. A symbol with
// no SourceFile (a stdlib file's, or one of a file with no path) is in
// this file when this file defines it at its position. With no analysis
// (a nil fa), only a public declaration is seen.
func (fa *FileAnalysis) Sees(sym *Symbol) bool {
	if sym == nil {
		return false
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	if fa != nil && sym.SourceFile == "" && fa.Definitions[sym.Pos] == sym {
		return true
	}
	return fa.SeesDeclaration(sym.Visibility, sym.Package, sym.SourceFile)
}

// SeesDeclaration is Sees for a declaration known by its level, the package
// it is in and the path of its file, as a parse of a file the language
// server has not analyzed gives them. It is the rule Sees applies.
func (fa *FileAnalysis) SeesDeclaration(vis ast.Visibility, module, file string) bool {
	switch {
	case vis == ast.Public:
		return true
	case fa == nil:
		return false
	case vis == ast.Internal && module == fa.Package:
		return true
	}
	return file != "" && fa.inside(file)
}

// PrivateDeclarationCode is the code of the error for naming a private
// declaration from a file other than its own. Args
// are the name and the declaring file's name.
const PrivateDeclarationCode = "private-declaration"

// InternalDeclarationCode is the code of the error for naming an internal
// declaration from another package. Args are the name and the declaring
// package's key name ("" for the program's own).
const InternalDeclarationCode = "internal-declaration"

// unseen is the error for naming sym, which this file does not see
// (Sees), as name at line:col: it says the declaration's level and, as
// help, the edit of its declaration that would make it reachable. A std
// declaration is reported as any other package's is, "`compiled` is
// internal to package `std`", but its help says it is not part of std's
// public surface, since no edit of the program's reaches it
// (stdSurfaceHint). A declaration of a package modules says a fetch
// supplied (PackageIndex.Fetched) gets no help: its file is in the package
// cache, not the reader's to edit.
func (fa *FileAnalysis) unseen(modules PackageIndex, sym *Symbol, name string, line, col int) TypeError {
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	var e TypeError
	var hint string
	if sym.Visibility == ast.Private {
		e = TypeError{
			Line: line, Col: col, Code: PrivateDeclarationCode,
			Message: fmt.Sprintf("`%s` is private to %s", name, fileNamed(sym.SourceFile)),
			Args:    []string{name, filepath.Base(sym.SourceFile)},
		}
		hint = "drop `private` to name it from the other files of its package, or write `pub` to name it from any package"
	} else {
		module := "the program's own package"
		if sym.Package != "" {
			module = "package `" + sym.Package + "`"
		}
		e = TypeError{
			Line: line, Col: col, Code: InternalDeclarationCode,
			Message: fmt.Sprintf("`%s` is internal to %s", name, module),
			Args:    []string{name, sym.Package},
		}
		hint = "write `pub` on its declaration to name it from another package"
	}
	switch {
	case sym.Package == "std":
		hint = stdSurfaceHint(sym, name)
	case modules.Fetched[sym.Package]:
		return e
	}
	return e.WithHint(hint)
}

// stdSurfaceHint is the help for a std declaration a program may not name:
// it is not part of std's public surface, and when one public std module
// declares a type of its name, that type's reference page.
func stdSurfaceHint(sym *Symbol, name string) string {
	hint := fmt.Sprintf("`%s` is not part of std's public surface", name)
	if isTypeKind(sym.Kind) {
		if mods := stdTypeModules(sym.Name); len(mods) == 1 {
			hint += fmt.Sprintf("; the public `%s` is in `std::%s`, %s", sym.Name, stdImportPath(mods[0]), StdReferenceURL(mods[0]))
		}
	}
	return hint
}

// StdReferenceURL is the reference page of the public std module (`http`,
// `fs/testing`).
func StdReferenceURL(module string) string {
	return "https://nomi-lang.org/reference/" + module + "/"
}

// fileNamed is how a message names the file at path that a private
// declaration or an opaque type's inside belongs to: its name in
// backquotes, or "its file" for a declaration with no path (std's).
func fileNamed(path string) string {
	if path == "" {
		return "its file"
	}
	return "`" + filepath.Base(path) + "`"
}

// SeesDeclarationIn is SeesDeclaration for a declaration of level vis in
// the file home, as the IR builder asks it of a program's files.
func (fa *FileAnalysis) SeesDeclarationIn(vis ast.Visibility, home *FileAnalysis) bool {
	if home == nil {
		return vis == ast.Public
	}
	return fa.SeesDeclaration(vis, home.Package, home.FilePath)
}

// isHiddenImportAlias reports whether alias names a hidden import an
// expansion added (`phrase$`): no source can write a `$`. A name reached
// through one is the expansion's, which may name what the expanding
// package's own files see, std's internal constructors included
// (`phrase$.compiled`), so the sites that judge a qualified name by Sees
// let it through.
func isHiddenImportAlias(alias string) bool {
	return strings.Contains(alias, "$")
}

// inside reports whether this file is the file at path, the one file that
// names that file's private declarations and reaches inside its opaque
// types (spec §14 *Visibility*, §4 *Opaque distinct types*). A file named
// `x_test.nomi` gains nothing from its name. A REPL session's files are
// one file (FileDecls.Session): each is inside every other file of the
// session that has a path, which a stdlib file's declarations lack.
func (fa *FileAnalysis) inside(path string) bool {
	return fa != nil && (fa.FilePath == path || fa.Session && path != "")
}
