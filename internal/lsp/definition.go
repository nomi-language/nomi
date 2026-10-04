package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"golang.org/x/mod/modfile"
)

func (s *Server) textDocumentDefinition(ctx *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	uri := string(params.TextDocument.URI)
	doc := s.docs.Snapshot(uri)
	if doc == nil {
		return nil, nil
	}

	// Convert LSP 0-based to analysis 1-based
	pos := analysis.Pos{
		Line: int(params.Position.Line) + 1,
		Col:  utf16ToByteCol(doc.Content, params.Position.Line, params.Position.Character),
	}

	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		if loc, ok := s.foreignGoAliasDefinitionLocation(doc.Nodes, pos, uri); ok {
			return s.toUTF16Location(loc), nil
		}
		if loc, ok := s.foreignGoDefinitionLocation(doc.Nodes, pos, uri); ok {
			return s.toUTF16Location(loc), nil
		}
		if loc, ok := s.foreignGoPackageDefinitionLocation(doc.Nodes, pos, uri); ok {
			return s.toUTF16Location(loc), nil
		}
		return nil, nil
	}
	_, tokenStart, _, tokenOK := doc.Analysis.TokenAt(pos)

	// Interface-qualified call (`Display.to_string(x)`) with a statically
	// concrete receiver: the checker recorded the concrete impl this call
	// dispatches to. Jump there rather than to the abstract interface
	// method, so go-to-def answers "where does this call land." Falls
	// through to the interface method when the target can't be resolved to
	// a file (treated the same as no dispatch target).
	if sym.DispatchImpl != nil {
		if loc, ok := s.dispatchImplLocation(doc.Analysis, sym.DispatchImpl, uri); ok {
			return s.toUTF16Location(loc), nil
		}
	}

	// Concrete type-body method implementing an interface: jump to the
	// interface method contract. This is the inverse of
	// textDocument/implementation, which jumps from an interface method to
	// concrete methods.
	if sym.Kind == analysis.SymbolFunction && sym.ImplInterface != "" && tokenOK && doc.Analysis.DefinitionAt(tokenStart) == sym {
		if methodSym := s.findInterfaceMethodSymbol(doc.Analysis, sym.ImplInterface, sym.Name); methodSym != nil {
			return s.toUTF16Location(s.definitionLocationForSymbol(uri, methodSym, sym)), nil
		}
	}

	// Follow resolved pointer for selective imports
	defSym := sym
	if sym.Resolved != nil {
		defSym = sym.Resolved
	}

	// Check if this symbol came from a user import statement
	if imp, ok := defSym.Node.(*ast.ImportStmt); ok {
		if imp.Extern {
			return s.toUTF16Location(s.definitionLocationForSymbol(uri, defSym, sym)), nil
		}
		loc, err := s.resolveImportDefinition(uri, imp, defSym)
		return s.toUTF16Result(loc), err
	}

	return s.toUTF16Location(s.definitionLocationForSymbol(uri, defSym, sym)), nil
}

func (s *Server) foreignGoAliasDefinitionLocation(nodes []ast.Node, pos analysis.Pos, currentURI string) (*protocol.Location, bool) {
	aliases := map[string]analysis.Pos{}
	foreignAliasDefinitions(nodes, aliases)
	alias, ok := foreignBindingAliasAt(nodes, pos, aliases)
	if !ok {
		return nil, false
	}
	defPos, ok := aliases[alias]
	if !ok || defPos.Line == 0 || defPos.Col == 0 {
		return nil, false
	}
	startLine := uint32(defPos.Line - 1)
	startChar := uint32(defPos.Col - 1)
	return &protocol.Location{
		URI: protocol.DocumentUri(currentURI),
		Range: protocol.Range{
			Start: protocol.Position{Line: startLine, Character: startChar},
			End:   protocol.Position{Line: startLine, Character: startChar + uint32(len(alias))},
		},
	}, true
}

func foreignAliasDefinitions(nodes []ast.Node, aliases map[string]analysis.Pos) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ExternPackage:
			aliases[v.Alias] = analysis.Pos{Line: v.AliasLine, Col: v.AliasCol}
		case *ast.ImportStmt:
			if v.Extern && v.ExternAlias != "" && v.ExternAlias != "_" {
				pos := analysis.Pos{Line: v.ExternPathLine, Col: v.ExternPathCol}
				if v.ExternAliasExplicit {
					pos = analysis.Pos{Line: v.ExternAliasLine, Col: v.ExternAliasCol}
				}
				aliases[v.ExternAlias] = pos
			}
		case *ast.ImportBlock:
			for _, entry := range v.Entries {
				foreignAliasDefinitions([]ast.Node{entry}, aliases)
			}
		case *ast.GoBlock:
			for _, imp := range goBlockImports(v) {
				if imp.alias != "" && imp.alias != "_" {
					aliases[imp.alias] = analysis.Pos{Line: imp.aliasLine, Col: imp.aliasCol}
				}
			}
		case *ast.ImplBlock:
			foreignAliasDefinitions(v.Items, aliases)
		}
	}

}
func foreignBindingAliasAt(nodes []ast.Node, pos analysis.Pos, aliases map[string]analysis.Pos) (string, bool) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ExternFunc:
			if positionWithinName(pos, v.ForeignAliasLine, v.ForeignAliasCol, v.ForeignAlias) {
				return v.ForeignAlias, true
			}
			if alias, ok := inlineGoAliasAt(v.GoBody, v.GoBodyLine, v.GoBodyCol, pos, aliasPositionNames(aliases)); ok {
				return alias, true
			}
		case *ast.ExternType:
			if positionWithinName(pos, v.ForeignAliasLine, v.ForeignAliasCol, v.ForeignAlias) {
				return v.ForeignAlias, true
			}
			if alias, ok := inlineGoAliasAt(v.GoBody, v.GoBodyLine, v.GoBodyCol, pos, aliasPositionNames(aliases)); ok {
				return alias, true
			}
		case *ast.ImplBlock:
			if alias, ok := foreignBindingAliasAt(v.Items, pos, aliases); ok {
				return alias, true
			}
		}
	}
	return "", false
}

func (s *Server) foreignGoDefinitionLocation(nodes []ast.Node, pos analysis.Pos, currentURI string) (*protocol.Location, bool) {
	importPath, goName, ok := foreignBindingAt(nodes, pos, nil)
	if !ok || importPath == "" || goName == "" {
		return nil, false
	}
	root := s.docs.FindProjectRoot(uriToPath(currentURI))
	dir, ok := resolveLocalGoImportDir(root, importPath)
	if !ok {
		return nil, false
	}
	file, line, col, ok := findGoTopLevelDefinition(dir, goName)
	if !ok {
		return nil, false
	}
	startLine := uint32(line - 1)
	startChar := uint32(col - 1)
	return &protocol.Location{
		URI: protocol.DocumentUri(pathToURI(file)),
		Range: protocol.Range{
			Start: protocol.Position{Line: startLine, Character: startChar},
			End:   protocol.Position{Line: startLine, Character: startChar + uint32(len(goName))},
		},
	}, true
}

func (s *Server) foreignGoPackageDefinitionLocation(nodes []ast.Node, pos analysis.Pos, currentURI string) (*protocol.Location, bool) {
	importPath, ok := foreignPackagePathAt(nodes, pos)
	if !ok || importPath == "" {
		return nil, false
	}
	return s.foreignGoPackageDefinitionLocationForPath(importPath, currentURI)
}

func (s *Server) foreignGoPackageDefinitionLocationForPath(importPath, currentURI string) (*protocol.Location, bool) {
	root := s.docs.FindProjectRoot(uriToPath(currentURI))
	dir, ok := resolveLocalGoImportDir(root, importPath)
	if !ok {
		return nil, false
	}
	file, line, col, packageName, ok := findGoPackageDeclaration(dir)
	if !ok {
		return nil, false
	}
	startLine := uint32(line - 1)
	startChar := uint32(col - 1)
	return &protocol.Location{
		URI: protocol.DocumentUri(pathToURI(file)),
		Range: protocol.Range{
			Start: protocol.Position{Line: startLine, Character: startChar},
			End:   protocol.Position{Line: startLine, Character: startChar + uint32(len(packageName))},
		},
	}, true
}

func foreignBindingAt(nodes []ast.Node, pos analysis.Pos, inherited map[string]string) (string, string, bool) {
	aliases := make(map[string]string, len(inherited)+4)
	for alias, importPath := range inherited {
		aliases[alias] = importPath
	}
	for _, n := range nodes {
		switch pkg := n.(type) {
		case *ast.ExternPackage:
			aliases[pkg.Alias] = pkg.ImportPath
		case *ast.ImportStmt:
			if pkg.Extern && pkg.ExternAlias != "" && pkg.ExternAlias != "_" {
				aliases[pkg.ExternAlias] = pkg.ExternPath
			}
		case *ast.ImportBlock:
			for _, entry := range pkg.Entries {
				if entry.Extern && entry.ExternAlias != "" && entry.ExternAlias != "_" {
					aliases[entry.ExternAlias] = entry.ExternPath
				}
			}
		case *ast.GoBlock:
			for _, imp := range goBlockImports(pkg) {
				if imp.alias != "" && imp.alias != "_" {
					aliases[imp.alias] = imp.importPath
				}
			}
		}
	}
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ExternFunc:
			if positionWithinName(pos, v.ForeignNameLine, v.ForeignNameCol, v.ForeignName) {
				return aliases[v.ForeignAlias], v.ForeignName, true
			}
			if alias, goName, ok := inlineGoSelectorAt(v.GoBody, v.GoBodyLine, v.GoBodyCol, pos, mapKeys(aliases)); ok {
				return aliases[alias], goName, true
			}
		case *ast.ExternType:
			if positionWithinName(pos, v.ForeignNameLine, v.ForeignNameCol, v.ForeignName) {
				return aliases[v.ForeignAlias], v.ForeignName, true
			}
			if alias, goName, ok := inlineGoSelectorAt(v.GoBody, v.GoBodyLine, v.GoBodyCol, pos, mapKeys(aliases)); ok {
				return aliases[alias], goName, true
			}
		case *ast.ImplBlock:
			if importPath, goName, ok := foreignBindingAt(v.Items, pos, aliases); ok {
				return importPath, goName, true
			}
		}
	}
	return "", "", false
}

func aliasPositionNames(aliases map[string]analysis.Pos) []string {
	out := make([]string, 0, len(aliases))
	for alias := range aliases {
		out = append(out, alias)
	}
	return out
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func inlineGoSelectorAt(body string, bodyLine, bodyCol int, pos analysis.Pos, aliases []string) (string, string, bool) {
	return inlineGoSelectorAtMode(body, bodyLine, bodyCol, pos, aliases, false)
}

func inlineGoAliasAt(body string, bodyLine, bodyCol int, pos analysis.Pos, aliases []string) (string, bool) {
	alias, _, ok := inlineGoSelectorAtMode(body, bodyLine, bodyCol, pos, aliases, true)
	return alias, ok
}

func inlineGoSelectorAtMode(body string, bodyLine, bodyCol int, pos analysis.Pos, aliases []string, aliasOnly bool) (string, string, bool) {
	if body == "" || bodyLine <= 0 || bodyCol <= 0 {
		return "", "", false
	}
	for _, alias := range aliases {
		if alias == "" || alias == "_" {
			continue
		}
		needle := alias + "."
		for lineIdx, line := range strings.Split(body, "\n") {
			searchFrom := 0
			for {
				idx := strings.Index(line[searchFrom:], needle)
				if idx < 0 {
					break
				}
				idx += searchFrom
				nameStart := idx + len(needle)
				nameEnd := nameStart
				for nameEnd < len(line) && isGoIdentByte(line[nameEnd], nameEnd-nameStart) {
					nameEnd++
				}
				if nameEnd > nameStart {
					sourceLine := bodyLine + lineIdx
					sourceCol := 1 + idx
					if lineIdx == 0 {
						sourceCol = bodyCol + idx
					}
					aliasStart := sourceCol
					aliasEnd := aliasStart + len(alias)
					nameStartCol := sourceCol + len(needle)
					nameEndCol := nameStartCol + (nameEnd - nameStart)
					if pos.Line == sourceLine && pos.Col >= aliasStart && pos.Col < aliasEnd {
						return alias, line[nameStart:nameEnd], true
					}
					if aliasOnly {
						searchFrom = idx + len(needle)
						continue
					}
					if pos.Line == sourceLine && pos.Col >= nameStartCol && pos.Col < nameEndCol {
						return alias, line[nameStart:nameEnd], true
					}
				}
				searchFrom = idx + len(needle)
			}
		}
	}
	return "", "", false
}

func isGoIdentByte(b byte, offset int) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (offset > 0 && b >= '0' && b <= '9')
}

func foreignPackagePathAt(nodes []ast.Node, pos analysis.Pos) (string, bool) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ExternPackage:
			if positionWithinName(pos, v.ImportPathLine, v.ImportPathCol, v.ImportPath) {
				return v.ImportPath, true
			}
		case *ast.ImportStmt:
			if v.Extern && positionWithinName(pos, v.ExternPathLine, v.ExternPathCol, v.ExternPath) {
				return v.ExternPath, true
			}
		case *ast.ImportBlock:
			for _, entry := range v.Entries {
				if entry.Extern && positionWithinName(pos, entry.ExternPathLine, entry.ExternPathCol, entry.ExternPath) {
					return entry.ExternPath, true
				}
			}
		case *ast.GoBlock:
			for _, imp := range goBlockImports(v) {
				if positionWithinName(pos, imp.pathLine, imp.pathCol, imp.importPath) {
					return imp.importPath, true
				}
			}
		}
	}
	return "", false
}

type goBlockImport struct {
	alias      string
	importPath string
	aliasLine  int
	aliasCol   int
	pathLine   int
	pathCol    int
}

func goBlockImports(block *ast.GoBlock) []goBlockImport {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "inline_go.nomi.go", "package main\n"+strings.TrimSpace(block.Body)+"\n", goparser.ParseComments)
	if err != nil {
		return nil
	}
	var out []goBlockImport
	for _, imp := range file.Imports {
		importPath, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		alias := defaultGoImportAlias(importPath)
		aliasLine, aliasCol := goBlockSourcePos(fset, block, imp.Path.Pos())
		if imp.Name != nil {
			alias = imp.Name.Name
			aliasLine, aliasCol = goBlockSourcePos(fset, block, imp.Name.Pos())
		}
		pathLine, pathCol := goBlockSourcePos(fset, block, imp.Path.Pos())
		out = append(out, goBlockImport{
			alias:      alias,
			importPath: importPath,
			aliasLine:  aliasLine,
			aliasCol:   aliasCol,
			pathLine:   pathLine,
			pathCol:    pathCol,
		})
	}
	return out
}

func goBlockSourcePos(fset *gotoken.FileSet, block *ast.GoBlock, pos gotoken.Pos) (int, int) {
	p := fset.Position(pos)
	line := block.BodyLine + p.Line - 2
	col := p.Column
	if p.Line == 2 {
		col = block.BodyCol + p.Column - 1
	}
	return line, col
}

func defaultGoImportAlias(importPath string) string {
	importPath = strings.TrimSuffix(importPath, "/")
	if idx := strings.LastIndex(importPath, "/"); idx >= 0 {
		return importPath[idx+1:]
	}
	return importPath
}

func positionWithinName(pos analysis.Pos, line, col int, name string) bool {
	return line > 0 && col > 0 && pos.Line == line && pos.Col >= col && pos.Col < col+len(name)
}

// resolveLocalGoImportDir maps a Go import path to a directory on disk, for
// go-to-definition on a `gopkg` handle's symbols.
//
// It used to special-case `nomi/std/<name>`, so a handle in a stdlib facade
// jumped to the co-located adapter under `std/<name>/`. No std facade carries
// a `gopkg` handle any more — every std declaration is a `host fn` and every
// Go implementation is a sibling package — so the branch had no import path
// left to match. What remains is the `go.mod` reading a USER project needs.
func resolveLocalGoImportDir(projectRoot, importPath string) (string, bool) {
	f, err := parseGoMod(projectRoot)
	if err != nil {
		return "", false
	}
	bestPrefix := ""
	bestDir := ""
	for _, r := range f.Replace {
		if r.New.Version != "" || r.New.Path == "" {
			continue
		}
		if importPath != r.Old.Path && !strings.HasPrefix(importPath, r.Old.Path+"/") {
			continue
		}
		target := r.New.Path
		if !filepath.IsAbs(target) {
			target = filepath.Join(projectRoot, target)
		}
		suffix := strings.TrimPrefix(importPath, r.Old.Path)
		if len(r.Old.Path) > len(bestPrefix) {
			bestPrefix = r.Old.Path
			bestDir = filepath.Join(target, filepath.FromSlash(strings.TrimPrefix(suffix, "/")))
		}
	}
	if bestDir != "" {
		return filepath.Clean(bestDir), true
	}
	if f.Module != nil {
		modPath := f.Module.Mod.Path
		if importPath == modPath || strings.HasPrefix(importPath, modPath+"/") {
			suffix := strings.TrimPrefix(importPath, modPath)
			return filepath.Clean(filepath.Join(projectRoot, filepath.FromSlash(strings.TrimPrefix(suffix, "/")))), true
		}
	}
	return "", false
}

func parseGoMod(projectRoot string) (*modfile.File, error) {
	goModPath := filepath.Join(projectRoot, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return nil, err
	}
	return modfile.Parse(goModPath, data, nil)
}

func findGoTopLevelDefinition(dir, name string) (string, int, int, bool) {
	fset := gotoken.NewFileSet()
	var outFile string
	var outLine, outCol int
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		file, err := goparser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}
		for _, decl := range file.Decls {
			switch v := decl.(type) {
			case *goast.FuncDecl:
				if v.Name != nil && v.Name.Name == name {
					p := fset.Position(v.Name.Pos())
					outFile, outLine, outCol = p.Filename, p.Line, p.Column
					return filepath.SkipAll
				}
			case *goast.GenDecl:
				for _, spec := range v.Specs {
					if ts, ok := spec.(*goast.TypeSpec); ok && ts.Name != nil && ts.Name.Name == name {
						p := fset.Position(ts.Name.Pos())
						outFile, outLine, outCol = p.Filename, p.Line, p.Column
						return filepath.SkipAll
					}
				}
			}
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return "", 0, 0, false
	}
	return outFile, outLine, outCol, outFile != ""
}

func findGoPackageDeclaration(dir string) (string, int, int, string, bool) {
	fset := gotoken.NewFileSet()
	var outFile, outPackage string
	var outLine, outCol int
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		file, err := goparser.ParseFile(fset, path, nil, goparser.PackageClauseOnly)
		if err != nil || file.Name == nil {
			return nil
		}
		p := fset.Position(file.Name.Pos())
		outFile, outLine, outCol, outPackage = p.Filename, p.Line, p.Column, file.Name.Name
		return filepath.SkipAll
	})
	if err != nil && err != filepath.SkipAll {
		return "", 0, 0, "", false
	}
	return outFile, outLine, outCol, outPackage, outFile != ""
}

func (s *Server) definitionLocationForSymbol(uri string, defSym, clickedSym *analysis.Symbol) *protocol.Location {
	if defSym == nil {
		return nil
	}
	targetURI := protocol.DocumentUri(uri)
	if defSym.DefinitionFile != "" && defSym.DefinitionLine > 0 && defSym.DefinitionCol > 0 {
		startLine := toZeroBased(defSym.DefinitionLine)
		startChar := toZeroBased(defSym.DefinitionCol)
		span := defSym.DefinitionSpan
		if span <= 0 {
			span = len(defSym.Name)
		}
		return &protocol.Location{
			URI: protocol.DocumentUri(pathToURI(defSym.DefinitionFile)),
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      startLine,
					Character: startChar,
				},
				End: protocol.Position{
					Line:      startLine,
					Character: startChar + uint32(span),
				},
			},
		}
	}
	if defSym.SourceFile != "" {
		targetURI = protocol.DocumentUri(pathToURI(defSym.SourceFile))
	}
	if s.std != nil {
		if defSym.Kind == analysis.SymbolModule {
			// Module symbol (e.g., "io") — jump to the module's .nomi file
			if _, ok := s.std.Modules[defSym.Name]; ok {
				targetURI = protocol.DocumentUri(s.std.FileURI(defSym.Name))
			}
		} else {
			found := false
			for modName, fa := range s.std.Files {
				// Top-level symbol match: defSym is the same instance as a
				// stdlib top-level binding (e.g. an Interface, Struct, fn).
				if stdSym := fa.ModuleScope.LookupLocal(defSym.Name); stdSym != nil && stdSym.Pos == defSym.Pos {
					targetURI = protocol.DocumentUri(s.std.FileURI(modName))
					found = true
					break
				}
				// Member match: defSym is an interface method or other
				// member of a stdlib type (e.g. Io.write). Top-level
				// LookupLocal misses members; walk each top-level symbol's
				// Members map and match by Pos.
				for _, parent := range fa.ModuleScope.Symbols {
					if member, ok := parent.Members[defSym.Name]; ok && member.Pos == defSym.Pos {
						targetURI = protocol.DocumentUri(s.std.FileURI(modName))
						found = true
						break
					}
				}
				// Type-promoted method match: defSym is a type-body method
				// (inherent op or impl method, e.g. List.concat) — those
				// live in the FA's TypeMethods table, not module scope and
				// not Members.
				if !found {
					for _, byName := range fa.TypeMethods {
						if m, ok := byName[defSym.Name]; ok && m.Pos == defSym.Pos {
							targetURI = protocol.DocumentUri(s.std.FileURI(modName))
							found = true
							break
						}
					}
				}
				if found {
					break
				}
			}
		}
	}

	// Location uses defSym so we land at the real definition,
	// not the click position. The "it" implicit-param check stays on
	// `clickedSym` because that's specifically about the local binding shape.
	startLine := toZeroBased(defSym.Pos.Line)
	startChar := toZeroBased(defSym.Pos.Col)
	endChar := startChar + uint32(len(defSym.Name))

	// For the implicit 'it' parameter, the definition points to the lambda's '{'
	// but there's no actual 'it' token there. Use a zero-width range at '{'.
	if clickedSym != nil && clickedSym.Kind == analysis.SymbolParam && clickedSym.Name == "it" {
		endChar = startChar + 1
	}

	return &protocol.Location{
		URI: targetURI,
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      startLine,
				Character: startChar,
			},
			End: protocol.Position{
				Line:      startLine,
				Character: endChar,
			},
		},
	}
}

// resolveImportDefinition resolves go-to-def for symbols that came from an import statement.
// For `{self}` owner imports, jumps to the module file.
// For item imports (import models.{User, Point}), jumps to the definition in the module file.
// Stdlib imports (import std/lists.List, import std/maybe.Maybe) jump to the
// materialised stdlib file URI rather than a project-relative path.
func (s *Server) resolveImportDefinition(currentURI string, imp *ast.ImportStmt, sym *analysis.Symbol) (any, error) {
	// Build the module path string
	modPath := make([]string, len(imp.ModulePath))
	for i, node := range imp.ModulePath {
		modPath[i] = ast.ImportNodeName(node)
	}

	var targetURI protocol.DocumentUri

	// Stdlib imports route through the embedded stdlib's FileURI machinery
	// rather than a project-relative path lookup. The "module" for jump
	// purposes is the last path segment that names a stdlib module — for
	// drill-through forms like `std.bool.Bool.{True}`, that's "bool"
	// rather than "Bool" (an enum within bool).
	if len(modPath) >= 2 && modPath[0] == "std" && s.std != nil {
		moduleName := ""
		for i := len(modPath) - 1; i >= 1; i-- {
			if _, ok := s.std.Modules[modPath[i]]; ok {
				moduleName = modPath[i]
				break
			}
		}
		if moduleName == "" {
			return nil, nil
		}
		targetURI = protocol.DocumentUri(s.std.FileURI(moduleName))
	} else {
		// Resolve relative to the project root (so an import in a
		// sub-directory file resolves against the same root the runtime
		// uses, not the file's immediate parent).
		dir := s.docs.FindProjectRoot(uriToPath(currentURI))
		filePath := filepath.Join(append([]string{dir}, modPath...)...) + ".nomi"

		if _, err := os.Stat(filePath); err != nil {
			return nil, nil // file doesn't exist, no definition
		}

		targetURI = protocol.DocumentUri(pathToURI(filePath))
	}

	if sym.Kind == analysis.SymbolModule {
		// Module import — jump to top of the file
		return &protocol.Location{
			URI: targetURI,
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 0},
			},
		}, nil
	}

	// Selective import — find the definition in the target file: in the
	// open document's analysis, else among a closed file's indexed
	// top-level declarations.
	if targetDoc := s.docs.Snapshot(string(targetURI)); targetDoc != nil && targetDoc.Analysis != nil {
		if targetSym := targetDoc.Analysis.ModuleScope.LookupLocal(sym.Name); targetSym != nil {
			loc := makeLocation(string(targetURI), targetSym.Pos, targetSym.Name)
			return &loc, nil
		}
	} else if targetDoc == nil {
		for _, d := range s.docs.FileDecls(uriToPath(string(targetURI))) {
			if d.Name == sym.Name {
				loc := makeLocation(string(targetURI), d.Pos, d.Name)
				return &loc, nil
			}
		}
	}

	// No declaration found: jump to the top of the module file.
	return &protocol.Location{
		URI: targetURI,
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 0, Character: 0},
		},
	}, nil
}

// dispatchImplLocation builds a go-to-def Location for the concrete impl
// an interface-qualified call dispatches to (Symbol.DispatchImpl). The
// impl FuncDef's source file is recovered from its project-wide home
// module-path key (ProjectImpls.ImplFiles); the FuncDef's own Line/Col
// place the cursor on the method name. Returns ok=false when the home
// module can't be determined, so the caller falls back to the interface
// method.
func (s *Server) dispatchImplLocation(fa *analysis.FileAnalysis, impl ast.ImplMethodDecl, openDocURI string) (*protocol.Location, bool) {
	if fa == nil || fa.ProjectImpls == nil || impl == nil {
		return nil, false
	}
	// The home-module key + name/position come from a FuncDef impl
	// (ImplFiles) or an `host fn` impl (ImplExternFiles) — both navigable.
	var (
		key       string
		ok        bool
		line, col int
		name      string
	)
	switch n := impl.(type) {
	case *ast.FuncDef:
		key, ok = fa.ProjectImpls.ImplFiles[n]
		line, col, name = n.Line, n.Col, n.Name
	case *ast.ExternFunc:
		key, ok = fa.ProjectImpls.ImplExternFiles[n]
		line, col, name = n.Line, n.Col, n.Name
	default:
		return nil, false
	}
	if !ok {
		return nil, false
	}
	uri := s.moduleKeyToURI(key, openDocURI)
	startLine := toZeroBased(line)
	startChar := toZeroBased(col)
	return &protocol.Location{
		URI: uri,
		Range: protocol.Range{
			Start: protocol.Position{Line: startLine, Character: startChar},
			End:   protocol.Position{Line: startLine, Character: startChar + uint32(len(name))},
		},
	}, true
}

// moduleKeyToURI maps an impl FuncDef's home module-path key (as stored
// in IfaceMethodImplFiles / ProjectImpls.ImplFiles) to the file URI its
// source lives in:
//
//   - "" — the project entry file, which has no module-path key by
//     design; resolve to the open document's own URI.
//   - "std/<mod>" — a stdlib module; route through the materialised
//     stdlib file URI (the same machinery go-to-def uses for stdlib
//     symbols), not a project-relative path.
//   - anything else — a project module resolved against the project root.
func (s *Server) moduleKeyToURI(key, openDocURI string) protocol.DocumentUri {
	switch {
	case key == "":
		return protocol.DocumentUri(openDocURI)
	case s.std != nil && strings.HasPrefix(key, "std/"):
		return protocol.DocumentUri(s.std.FileURI(strings.TrimPrefix(key, "std/")))
	default:
		root := s.docs.FindProjectRoot(uriToPath(openDocURI))
		absPath := filepath.Join(root, filepath.FromSlash(key)) + ".nomi"
		return protocol.DocumentUri(pathToURI(absPath))
	}
}

// uriToPath converts a file:// URI to a filesystem path.
func uriToPath(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		parsed, err := url.Parse(uri)
		if err == nil {
			return parsed.Path
		}
	}
	return uri
}

// pathToURI converts a filesystem path to a file:// URI.
func pathToURI(path string) string {
	return "file://" + path
}
