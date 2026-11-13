package analysis

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/parser"
)

// Short names, directory imports and paths at a use (spec §14 *Short names*
// and *Directory imports*).
//
// A path-only import binds one name: the last segment of its path or its
// `as` alias. When the path names a file, the name is the file's (`posts`
// for `import db::posts`). When it names a directory and no file, the name
// is the directory's (`admin` for `import views::admin`), a SymbolDirectory,
// and a path written below it at a use reaches a file under the directory:
// `admin::users.index(rows)` is views/admin/users.nomi.
//
// The import's PathUses, which the parser collects from the file's text,
// are the paths below the name. A directory import resolves each of them
// as the import of the file it names (the directory's path with the use's
// segments after it) and binds the file under the use's spelling,
// `admin::users`, in the import's scope and module tables; no identifier
// can spell that name, so nothing else takes it, and every pass that reads
// a qualifier by name reads a path as it reads a short name
// (ast.QualifierIdent). Only those files are loaded.
//
// A path at a use is checked where it is written (walkPath): its first
// segment must be a name a directory import binds, in the closest scope
// that binds the name. Anything else is an error that says what the file
// imports instead.
//
// Two imports may not bind one name: the second is an error that says to
// import the directories and write `db::notifications`, or to alias one.
// Neither may an import bind a name a declaration of the file holds.

// ImportedPath is a path-only import at the top of a file: the path as
// written, the name it binds, and whether it names a directory.
type ImportedPath struct {
	Spelled string // `db::posts`
	Bound   string // `posts`, or the alias
	Dir     bool
	Line    int
	stmt    *ast.ImportStmt
}

// DirectoryBinding is what a directory import binds (SymbolDirectory).
type DirectoryBinding struct {
	// Path is the import's path as resolution reads it: `views/admin`,
	// `std`, or a module's name for its root.
	Path []string
	// Spelled is the path as written, `views::admin`.
	Spelled string
	// Listing is what the directory holds.
	Listing *DirectoryError
	// Reached maps each use path the import's scope writes (`users`,
	// `reports::daily`) to the binding of the file it reaches.
	Reached map[string]*Symbol
	// unreached holds the error for each use path that reaches no file:
	// its message and hint, placed at each use.
	unreached map[string]TypeError
}

// importSpelledPath is n's file path as written: `db::posts`, `io`.
func importSpelledPath(n *ast.ImportStmt) string {
	segs := n.ModulePath
	if n.FileSegments > 0 && n.FileSegments <= len(segs) {
		segs = segs[:n.FileSegments]
	}
	parts := make([]string, len(segs))
	for i, seg := range segs {
		parts[i] = ast.ImportNodeName(seg)
	}
	return strings.Join(parts, ast.PathSeparator)
}

// importParentDir is the directory to import so that a path at a use
// reaches the file or directory n imports: `db` for `db::posts`, the
// module's name for a file at its root, "" when there is none to name.
func (b *builder) importParentDir(n *ast.ImportStmt) string {
	spelled := strings.Split(importSpelledPath(n), ast.PathSeparator)
	if len(spelled) > 1 {
		return strings.Join(spelled[:len(spelled)-1], ast.PathSeparator)
	}
	if _, dep := b.moduleIndex[spelled[0]]; dep {
		return spelled[0]
	}
	return b.currentModuleName
}

// bindShortName binds sym, a path-only import's file or directory, under
// its name in scope, and reports whether it took the name:
//
//   - another import binds the name to another file or directory: an
//     error at this import, and the first keeps the name;
//   - a declaration of the file holds the name: an error at this import,
//     and the declaration keeps it;
//   - otherwise sym takes the name.
func (b *builder) bindShortName(scope *Scope, sym *Symbol, n *ast.ImportStmt) bool {
	name := sym.Name
	if !IsSynthesizedLine(sym.Pos.Line) && scope == b.file.ModuleScope {
		b.file.ImportedPaths = append(b.file.ImportedPaths, ImportedPath{
			Spelled: importSpelledPath(n), Bound: name, Dir: sym.Kind == SymbolDirectory, Line: n.Line, stmt: n,
		})
	}
	prior := scope.LookupLocal(name)
	if prior == nil || IsSynthesizedLine(sym.Pos.Line) {
		scope.Define(sym)
		return true
	}
	if (prior.Kind == SymbolModule || prior.Kind == SymbolDirectory) && symbolCameFromImport(prior) && !IsSynthesizedLine(prior.Pos.Line) {
		if sameImportBinding(prior, sym) {
			// The same file imported twice is the unused-import check's
			// to report, and a file that did not resolve is already an
			// error at its path.
			scope.Define(sym)
			return true
		}
		b.file.TypeErrors = append(b.file.TypeErrors, b.twoImportsOneName(prior, sym, n))
		b.file.markUnbound(n)
		return false
	}
	if prior.Kind == SymbolModule || symbolCameFromImport(prior) ||
		prior.Kind == SymbolInterfaceMethod || isLocalVariant(prior) ||
		prior.Pos.Line <= 0 || IsSynthesizedLine(prior.Pos.Line) {
		scope.Define(sym)
		return true
	}
	b.file.TypeErrors = append(b.file.TypeErrors, b.importNameTaken(n, sym, kindLabel(prior), prior.Pos.Line))
	b.file.markUnbound(n)
	return false
}

// markUnbound notes n, a path-only import that binds nothing, since
// another import or a declaration holds its name: its error says so, and
// it is not also unused.
func (fa *FileAnalysis) markUnbound(n *ast.ImportStmt) {
	if fa.unboundImports == nil {
		fa.unboundImports = map[*ast.ImportStmt]bool{}
	}
	fa.unboundImports[n] = true
}

// sameImportBinding reports whether two path-only imports' bindings name
// one file or one directory, or one of them resolved to nothing.
func sameImportBinding(a, b *Symbol) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Kind == SymbolDirectory {
		return a.Directory != nil && b.Directory != nil && a.Directory.Spelled == b.Directory.Spelled
	}
	return a.ModuleScope == b.ModuleScope || a.ModuleScope == nil || b.ModuleScope == nil
}

// twoImportsOneName is the error at n, the second of two imports that
// bind one name: `'notifications' is already imported on line 1 from
// db::notifications`.
func (b *builder) twoImportsOneName(prior, sym *Symbol, n *ast.ImportStmt) TypeError {
	first, _ := prior.Node.(*ast.ImportStmt)
	firstPath := ""
	if first != nil {
		firstPath = importSpelledPath(first)
	}
	e := TypeError{
		Line:    sym.Pos.Line,
		Col:     sym.Pos.Col,
		EndLine: sym.Pos.Line,
		EndCol:  sym.Pos.Col + len(sym.Name),
		Message: fmt.Sprintf("'%s' is already imported on line %d from %s; two imports cannot bind one name", sym.Name, prior.Pos.Line, firstPath),
	}
	var dirs []string
	if first != nil {
		dirs = append(dirs, b.importParentDir(first))
	}
	dirs = append(dirs, b.importParentDir(n))
	// A std module nested under its parent (std::fs::testing) has no
	// directory import beside it, since `std::fs` is a file: the help
	// renames one after its parent.
	if strings.HasPrefix(firstPath, "std"+ast.PathSeparator) || strings.HasPrefix(importSpelledPath(n), "std"+ast.PathSeparator) {
		path, parent := importSpelledPath(n), dirs[len(dirs)-1]
		if parent == "std" && first != nil && dirs[0] != "std" {
			path, parent = firstPath, dirs[0]
		}
		alias := "other_" + sym.Name
		if parent != "std" {
			alias = lastPathSegment(parent) + "_" + sym.Name
		}
		return e.WithHint(fmt.Sprintf("import one under another name, as in `import %s as %s`", path, alias))
	}
	if first != nil && dirs[0] != "" && dirs[1] != "" && dirs[0] != dirs[1] {
		return e.WithHint(fmt.Sprintf("import the directories (`import %s` and `import %s`) and write `%s::%s` and `%s::%s`, or import one under another name with `as`",
			dirs[0], dirs[1], lastPathSegment(dirs[0]), lastPathSegment(firstPath), lastPathSegment(dirs[1]), lastPathSegment(importSpelledPath(n))))
	}
	return e.WithHint(fmt.Sprintf("import one under another name, as in `import %s as other_%s`", importSpelledPath(n), sym.Name))
}

// importNameTaken is the error at n, a path-only import whose name a
// declaration of the file, a what declared on line, holds.
func (b *builder) importNameTaken(n *ast.ImportStmt, sym *Symbol, what string, line int) TypeError {
	where := ""
	if line > 0 && !IsSynthesizedLine(line) {
		where = fmt.Sprintf(" on line %d", line)
	}
	e := TypeError{
		Line:    sym.Pos.Line,
		Col:     sym.Pos.Col,
		EndLine: sym.Pos.Line,
		EndCol:  sym.Pos.Col + len(sym.Name),
		Message: fmt.Sprintf("'%s' is already defined in this scope as a %s%s, so `import %s` cannot bind it", sym.Name, what, where, importSpelledPath(n)),
	}
	if dir := b.importParentDir(n); dir != "" && n.ModuleAlias == nil {
		return e.WithHint(fmt.Sprintf("import its directory (`import %s`) and write `%s::%s`, or import it under another name with `as`", dir, lastPathSegment(dir), sym.Name))
	}
	return e.WithHint("import it under another name with `as`")
}

// lastPathSegment is the last `::` segment of a spelled path.
func lastPathSegment(spelled string) string {
	if i := strings.LastIndex(spelled, ast.PathSeparator); i >= 0 {
		return spelled[i+len(ast.PathSeparator):]
	}
	return spelled
}

// directoryOf answers what path, an import's path that resolved to no file,
// names when it names a directory, or nil: a directory of the module, the
// standard library's root (`std`), a dependency's root (its name, when it
// has no file of that name), or the importing module's root (its name).
func (b *builder) directoryOf(path []string, miss *importMiss) *DirectoryError {
	if miss == nil {
		return nil
	}
	if miss.dir != nil {
		if miss.dir.Module {
			return nil
		}
		return miss.dir
	}
	if miss.std && len(miss.path) == 0 {
		return b.stdDirectory()
	}
	root := ""
	switch {
	case len(miss.path) == 0 && miss.dep != "":
		root = miss.root
	case len(path) == 1 && path[0] == b.currentModuleName && b.currentModuleName != "":
		root = b.projectRoot
	default:
		return nil
	}
	return b.listDirectory(root, nil)
}

// listDirectory is what the directory path below root holds: the
// loader's answer for the files it holds in memory, else the disk's. Nil
// when path names no directory.
func (b *builder) listDirectory(root string, path []string) *DirectoryError {
	if b.loader != nil {
		if _, err := b.loader(root, path); err != nil {
			var dir *DirectoryError
			if errors.As(err, &dir) {
				return dir
			}
		}
	}
	return DiskDirectory(root, path)
}

// stdDirectory is the standard library's root as a directory: the modules
// a program may import.
func (b *builder) stdDirectory() *DirectoryError {
	out := &DirectoryError{}
	for _, name := range b.stdlibModuleNames() {
		if strings.Contains(name, "/") || name == "prelude" || strings.HasPrefix(name, "_") {
			continue
		}
		if _, internal := internalStdModules[name]; internal {
			continue
		}
		out.Files = append(out.Files, name)
	}
	return out
}

// defineDirectoryImport binds n, a path-only import whose path names the
// directory dir, under bindName, and the files its PathUses reach.
func (b *builder) defineDirectoryImport(n *ast.ImportStmt, scope *Scope, nested bool, path []string, dir *DirectoryError, bindName string, pos Pos) {
	sym := &Symbol{
		Name: bindName,
		Kind: SymbolDirectory,
		Pos:  pos,
		Node: n,
		Directory: &DirectoryBinding{
			Path:      path,
			Spelled:   importSpelledPath(n),
			Listing:   dir,
			Reached:   map[string]*Symbol{},
			unreached: map[string]TypeError{},
		},
	}
	// Each path segment is the directory it names, for hover.
	for i, seg := range n.ModulePath {
		segPos := Pos{Line: seg.LineNum(), Col: b.nodeCol(seg)}
		if i == len(n.ModulePath)-1 && n.ModuleAlias == nil {
			b.file.References[segPos] = sym
			continue
		}
		b.file.References[segPos] = &Symbol{Name: ast.ImportNodeName(seg), Kind: SymbolDirectory, Pos: segPos, Node: n}
	}
	b.bindShortName(scope, sym, n)
	b.define(sym.Pos, sym)
	for _, use := range n.PathUses {
		b.reachThroughDirectory(sym, use, scope, nested)
	}
}

// reachThroughDirectory resolves use, a path written below dirSym's name,
// to the file it names, and binds that file under the use's spelling. A
// use that names no file records the error each of its sites reports.
func (b *builder) reachThroughDirectory(dirSym *Symbol, use string, scope *Scope, nested bool) {
	d := dirSym.Directory
	segs := strings.Split(use, ast.PathSeparator)
	full := append(slices.Clone(d.Path), segs...)
	spelled := dirSym.Name + ast.PathSeparator + use
	if msg := b.reachAccessError(full, spelled); msg != "" {
		d.unreached[use] = TypeError{Message: msg}
		return
	}
	var modScope *Scope
	var miss *importMiss
	if std := b.resolveStdlibImport(full); std != nil {
		modScope = std
	} else {
		modScope, _, miss = b.resolveImportOrMiss(full)
	}
	if modScope == nil && miss != nil {
		d.unreached[use] = b.reachError(dirSym, segs, full, miss)
		return
	}
	fileSym := &Symbol{Name: spelled, Kind: SymbolModule, Pos: dirSym.Pos, Node: dirSym.Node, ModuleScope: modScope}
	d.Reached[use] = fileSym
	scope.Define(fileSym)
	if nested || modScope == nil {
		return
	}
	if b.modules == nil {
		b.modules = make(map[string]*Scope)
	}
	if b.moduleSyms == nil {
		b.moduleSyms = make(map[string]*Symbol)
	}
	b.modules[spelled] = modScope
	b.moduleSyms[spelled] = fileSym
}

// reachAccessError is the message for a path at a use that reaches a file
// the file may not import: one inside another subtree's `internal/`, or an
// internal std module. "" when it may.
func (b *builder) reachAccessError(full []string, spelled string) string {
	if full[0] == "std" {
		if len(full) < 2 || IsStdlibKey(b.file.Origin) {
			return ""
		}
		if owner, internal := internalStdModules[strings.Join(full[1:], "/")]; internal {
			return fmt.Sprintf("`%s` is internal to std::%s", spelled, owner)
		}
		return ""
	}
	if err := b.internalAccessError(full); err != nil {
		return err.Error()
	}
	// A directory holding a nomi.toml is another module's root.
	root, rel := b.projectRoot, full
	if rest, self := SelfNameImport(full, b.currentModuleName); self {
		rel = rest
	} else if dep, ok := b.moduleIndex[full[0]]; ok {
		root, rel = dep, full[1:]
	}
	if root == "" {
		return ""
	}
	for end := 1; end < len(rel); end++ {
		if isModuleRoot(filepath.Join(append([]string{root}, rel[:end]...)...)) {
			return fmt.Sprintf("`%s` is in another module's directory: %s/ holds a nomi.toml", spelled, strings.Join(rel[:end], "/"))
		}
	}
	return ""
}

// reachError is the error for segs, a path below dirSym's name whose file
// full did not resolve: a directory, another module's directory, or no
// file, with what the directory holds.
func (b *builder) reachError(dirSym *Symbol, segs, full []string, miss *importMiss) TypeError {
	spell := func(parts ...string) string {
		return strings.Join(append([]string{dirSym.Name}, parts...), ast.PathSeparator)
	}
	written := spell(segs...)
	if miss.dir != nil && miss.dir.Module {
		return TypeError{Message: fmt.Sprintf("`%s` is another module's directory: it holds a nomi.toml", written)}.
			WithHint("a module is reached by its own name: add it with `nomi add` and import it by that name")
	}
	if miss.dir != nil {
		e := TypeError{Message: fmt.Sprintf("`%s` is a directory, not a file", written)}
		if len(miss.dir.Files) > 0 {
			return e.WithHint(fmt.Sprintf("name a file in it, as in `%s.name`", spell(append(slices.Clone(segs), miss.dir.Files[0])...)))
		}
		return e.WithHint("a path at a use names a .nomi file")
	}
	parent := segs[:len(segs)-1]
	leaf := segs[len(segs)-1]
	listing := dirSym.Directory.Listing
	if len(parent) > 0 {
		listing = nil
		if !miss.std {
			listing = b.listDirectory(miss.root, miss.path[:len(miss.path)-1])
		}
	}
	where := dirSym.Directory.Spelled
	if len(parent) > 0 {
		where += ast.PathSeparator + strings.Join(parent, ast.PathSeparator)
	}
	if listing == nil {
		return TypeError{Message: fmt.Sprintf("`%s` names no file: there is no directory %s", written, strings.ReplaceAll(where, ast.PathSeparator, "/")+"/")}
	}
	e := TypeError{Message: fmt.Sprintf("`%s` names no file: %s has no %s.nomi", written, where, leaf)}
	if best := closestName(leaf, append(slices.Clone(listing.Files), listing.Dirs...)); best != "" {
		return e.WithHint(fmt.Sprintf("did you mean `%s`?", spell(append(slices.Clone(parent), best)...)))
	}
	var held []string
	for _, f := range listing.Files {
		if !strings.HasSuffix(f, "_test") {
			held = append(held, "`"+spell(append(slices.Clone(parent), f)...)+"`")
		}
	}
	for _, sub := range listing.Dirs {
		held = append(held, "the directory `"+spell(append(slices.Clone(parent), sub)...)+"`")
	}
	if len(held) == 0 {
		return e.WithHint(fmt.Sprintf("%s holds no .nomi file", where))
	}
	if len(held) > 6 {
		held = append(held[:6], "more")
	}
	return e.WithHint(fmt.Sprintf("%s holds %s", where, joinList(held)))
}

// joinList joins items as `a`, `b` and `c`.
func joinList(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// walkFilePath resolves a path written as a member access's qualifier.
func (b *builder) walkFilePath(fp *ast.FilePath, scope *Scope, member string) {
	b.walkPath(fp.Segments, scope, member)
}

// walkPath resolves segs, a path written at a use (`admin::users`), to the
// file it reaches, recording a reference at each segment, and reports a
// path that reaches none. member is the name written after it, for the
// hints. Nil when the path reaches no file.
func (b *builder) walkPath(segs []ast.PathSegment, scope *Scope, member string) *Symbol {
	if len(segs) < 2 || IsSynthesizedLine(segs[0].Line) {
		return nil
	}
	at := Pos{Line: segs[0].Line, Col: segs[0].Col}
	if b.pathsWalked == nil {
		b.pathsWalked = map[Pos]bool{}
	}
	reported := b.pathsWalked[at]
	b.pathsWalked[at] = true
	spelled := ast.PathSpelling(segs)
	last := segs[len(segs)-1]
	report := func(e TypeError) {
		if reported {
			return
		}
		e.Line, e.Col = at.Line, at.Col
		e.EndLine, e.EndCol = last.Line, last.Col+len(last.Name)
		b.file.TypeErrors = append(b.file.TypeErrors, e)
		if b.file.pathErrors == nil {
			b.file.pathErrors = map[Pos]bool{}
		}
		b.file.pathErrors[at] = true
	}
	head := scope.Lookup(segs[0].Name)
	if head != nil && head.Kind == SymbolDirectory && head.Directory != nil {
		headRef := *head
		headRef.Span = len(segs[0].Name)
		b.file.References[at] = &headRef
		use := ast.PathSpelling(segs[1:])
		if file := head.Directory.Reached[use]; file != nil {
			b.recordPathRefs(segs, file)
			return file
		}
		if e, ok := head.Directory.unreached[use]; ok {
			report(e)
			return nil
		}
		report(TypeError{Message: fmt.Sprintf("`%s` is not reached through `import %s`", spelled, head.Directory.Spelled)})
		return nil
	}
	if head != nil && head.Kind == SymbolModule && head.ModuleScope == nil && symbolCameFromImport(head) {
		// The import of the name resolved to nothing, which is the error
		// at its path.
		if b.file.pathErrors == nil {
			b.file.pathErrors = map[Pos]bool{}
		}
		b.file.pathErrors[at] = true
		return nil
	}
	report(b.pathNotReachable(segs, head, member))
	return nil
}

// pathNotReachable is the error for segs, a path at a use whose first
// segment is no directory import's name here: head is what the name means
// instead, or nil.
func (b *builder) pathNotReachable(segs []ast.PathSegment, head *Symbol, member string) TypeError {
	spelled := ast.PathSpelling(segs)
	if member == "" {
		member = "name"
	}
	// A file import of this very path binds it under its short name.
	for _, imp := range b.file.ImportedPaths {
		if imp.Dir || imp.Spelled != spelled {
			continue
		}
		e := TypeError{Message: fmt.Sprintf("`%s` is not a name in this file: `import %s` binds the file as `%s`", spelled, imp.Spelled, imp.Bound)}
		if !b.file.unboundImports[imp.stmt] {
			e = e.withPathFix(segs, imp.Bound)
		}
		if dir := strings.Join(pathNames(segs[:len(segs)-1]), ast.PathSeparator); dir != "" {
			return e.WithHint(fmt.Sprintf("write `%s.%s`, or import its directory (`import %s`) and keep `%s.%s`", imp.Bound, member, dir, spelled, member))
		}
		return e.WithHint(fmt.Sprintf("write `%s.%s`", imp.Bound, member))
	}
	// A directory import above the path binds it under its own name.
	for _, imp := range b.file.ImportedPaths {
		if !imp.Dir || !strings.HasPrefix(spelled, imp.Spelled+ast.PathSeparator) {
			continue
		}
		rest := strings.TrimPrefix(spelled, imp.Spelled+ast.PathSeparator)
		e := TypeError{Message: fmt.Sprintf("`%s` is not a name in this file: `import %s` binds the directory as `%s`", spelled, imp.Spelled, imp.Bound)}
		if !b.file.unboundImports[imp.stmt] {
			e = e.withPathFix(segs, imp.Bound+ast.PathSeparator+rest)
		}
		return e.WithHint(fmt.Sprintf("write `%s::%s.%s`", imp.Bound, rest, member))
	}
	file := segs[len(segs)-1].Name
	rest := strings.Join(pathNames(segs[1:]), ast.PathSeparator)
	// The name is a file import's: a file has no files below it.
	if head != nil && head.Kind == SymbolModule && symbolCameFromImport(head) {
		if imp, ok := head.Node.(*ast.ImportStmt); ok {
			path := importSpelledPath(imp)
			e := TypeError{Message: fmt.Sprintf("`%s` is not imported: `%s` here is the file %s, not a directory", spelled, segs[0].Name, path)}
			hint := fmt.Sprintf("import the file (`import %s::%s`) and write `%s.%s`", path, rest, file, member)
			if dir := b.importParentDir(imp); dir != "" {
				hint += fmt.Sprintf(", or import the directory above it (`import %s`) and write `%s::%s::%s.%s`", dir, lastPathSegment(dir), lastPathSegment(path), rest, member)
			}
			return e.WithHint(hint)
		}
	}
	// A local or a declaration shadows a directory import's name.
	if head != nil {
		for _, imp := range b.file.ImportedPaths {
			if imp.Dir && imp.Bound == segs[0].Name {
				what := "the " + kindLabel(head)
				if head.Pos.Line > 0 && !IsSynthesizedLine(head.Pos.Line) {
					what += fmt.Sprintf(" on line %d", head.Pos.Line)
				}
				return TypeError{Message: fmt.Sprintf("`%s` here is %s, not the directory %s", segs[0].Name, what, imp.Spelled)}.
					WithHint(fmt.Sprintf("rename the %s, or import the directory under another name with `as`", kindLabel(head)))
			}
		}
	}
	e := TypeError{Message: fmt.Sprintf("`%s` is not imported", spelled)}
	if len(segs) == 2 {
		return e.WithHint(fmt.Sprintf("import the file (`import %s`) and write `%s.%s`, or import its directory (`import %s`) and keep `%s.%s`",
			spelled, file, member, segs[0].Name, spelled, member))
	}
	parent := strings.Join(pathNames(segs[:len(segs)-1]), ast.PathSeparator)
	dirName := segs[len(segs)-2].Name
	return e.WithHint(fmt.Sprintf("import the file (`import %s`) and write `%s.%s`, or a directory above it (`import %s`) and write `%s::%s.%s`",
		spelled, file, member, parent, dirName, file, member))
}

// PathAtUseCode tags the error for a path at a use that a file or a
// directory import binds under another name, whose Fix writes that name:
// `db::posts.find` after `import db::posts` is `posts.find`. An editor's
// `source.fixAll` applies every one.
const PathAtUseCode = "path-at-use"

// withPathFix is e with the code and the fix that replace segs, a path at a
// use, with text.
func (e TypeError) withPathFix(segs []ast.PathSegment, text string) TypeError {
	last := segs[len(segs)-1]
	e.Code = PathAtUseCode
	e.Fix = &parser.Fix{
		Title: fmt.Sprintf("Write `%s`", text),
		Edits: []parser.Edit{{
			Start: parser.Pos{Line: segs[0].Line, Col: segs[0].Col},
			End:   parser.Pos{Line: last.Line, Col: last.Col + len(last.Name)},
			Text:  text,
		}},
	}
	return e
}

// pathNames are segs' names.
func pathNames(segs []ast.PathSegment) []string {
	out := make([]string, len(segs))
	for i, seg := range segs {
		out[i] = seg.Name
	}
	return out
}

// recordPathRefs records file, the file a path at a use reaches, at the
// path's first and last segments, and each segment between as the
// directory it names, for hover, go-to-definition and highlighting. The
// first segment holds the file, as a short name's position does, since
// passes after the builder read a qualifier's reference there; the
// directory import counts it as a use of itself (findUnusedImports). Each
// record spans its own segment.
func (b *builder) recordPathRefs(segs []ast.PathSegment, file *Symbol) {
	last := len(segs) - 1
	for i, seg := range segs {
		pos := Pos{Line: seg.Line, Col: seg.Col}
		if i == 0 || i == last {
			site := *file
			site.Span = len(seg.Name)
			b.file.References[pos] = &site
			continue
		}
		b.file.References[pos] = &Symbol{Name: seg.Name, Kind: SymbolDirectory, Pos: pos, Node: file.Node}
	}
}

// shadowedFileHint is the hint for a member access on name, which resolves
// to sym, a value or a declaration, when the file imports a file of that
// short name. "" when it imports none.
func (fa *FileAnalysis) shadowedFileHint(name, member string, sym *Symbol) string {
	if fa == nil {
		return ""
	}
	for _, imp := range fa.ImportedPaths {
		if imp.Dir || imp.Bound != name {
			continue
		}
		what := "a value"
		if sym != nil {
			what = "the " + kindLabel(sym)
			if sym.Pos.Line > 0 && !IsSynthesizedLine(sym.Pos.Line) {
				what += fmt.Sprintf(" on line %d", sym.Pos.Line)
			}
		}
		hint := fmt.Sprintf("`%s` here is %s, not the file %s; ", name, what, imp.Spelled)
		if i := strings.LastIndex(imp.Spelled, ast.PathSeparator); i >= 0 {
			dir := imp.Spelled[:i]
			return hint + fmt.Sprintf("import its directory (`import %s`) and write `%s::%s.%s`, or import the file under another name with `as`",
				dir, lastPathSegment(dir), lastPathSegment(imp.Spelled), member)
		}
		return hint + "import the file under another name with `as`"
	}
	return ""
}
