package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
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
		// An operator: the function of the impl the checker chose for it
		// (`fn add` of `impl Add<Days, Date> for Date`, std/int's `host
		// fn add` for `Int + Int`).
		if impl := hoverdoc.OperatorImplAt(doc.Analysis, doc.Nodes, pos); impl != nil {
			if loc, ok := s.dispatchImplLocation(doc.Analysis, impl, uri); ok {
				return s.toUTF16Location(loc), nil
			}
			return nil, nil
		}
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
		if methodSym := s.findInterfaceMethodSymbol(doc.Analysis, sym); methodSym != nil {
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
		loc, err := s.resolveImportDefinition(uri, imp, defSym)
		return s.toUTF16Result(loc), err
	}

	// A name an `or` pattern binds is defined once in each alternative:
	// every site is its definition.
	if sites := doc.Analysis.DefinitionSites(defSym); len(sites) > 1 {
		locs := make([]protocol.Location, len(sites))
		for i, p := range sites {
			locs[i] = makeLocation(uri, p, defSym.Name)
		}
		return s.toUTF16Locations(locs), nil
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
		case *ast.ExternType:
			if positionWithinName(pos, v.ForeignAliasLine, v.ForeignAliasCol, v.ForeignAlias) {
				return v.ForeignAlias, true
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
	dir, ok := resolveLocalGoImportDir(uriToPath(currentURI), importPath)
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
	dir, ok := resolveLocalGoImportDir(uriToPath(currentURI), importPath)
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
		}
	}
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ExternFunc:
			if positionWithinName(pos, v.ForeignNameLine, v.ForeignNameCol, v.ForeignName) {
				return aliases[v.ForeignAlias], v.ForeignName, true
			}
		case *ast.ExternType:
			if positionWithinName(pos, v.ForeignNameLine, v.ForeignNameCol, v.ForeignName) {
				return aliases[v.ForeignAlias], v.ForeignName, true
			}
		case *ast.ImplBlock:
			if importPath, goName, ok := foreignBindingAt(v.Items, pos, aliases); ok {
				return importPath, goName, true
			}
		}
	}
	return "", "", false
}

func foreignPackagePathAt(nodes []ast.Node, pos analysis.Pos) (string, bool) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ExternPackage:
			if positionWithinName(pos, v.ImportPathLine, v.ImportPathCol, v.ImportPath) {
				return v.ImportPath, true
			}
		}
	}
	return "", false
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
//
// The go.mod read is the nearest one above the declaring file, the one the IR
// builder resolves a `gopkg` through. It need not sit at the Nomi project
// root: an extensionless script in a module's bin/ is rooted at bin/. A Go
// standard library package needs no go.mod: its directory is in the Go
// toolchain's GOROOT. A package of a required module is read from the Go
// module cache when `nomi fetch`, or the go command, put it there.
func resolveLocalGoImportDir(declaringFile, importPath string) (string, bool) {
	if dir, ok := ffirun.StdPackageDir(importPath); ok {
		return dir, true
	}
	projectRoot, ok := ffirun.GoModRoot(declaringFile)
	if !ok {
		return "", false
	}
	return ffirun.GoPackageDir(projectRoot, importPath)
}

// findGoTopLevelDefinition finds the function or type name declares at the
// top level of the Go package in dir. A package is one directory, so a
// sub-directory is another package and is not searched: `math.Sqrt` is not
// `math/big`'s `(*Float).Sqrt`. A method is not top-level.
func findGoTopLevelDefinition(dir, name string) (string, int, int, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, 0, false
	}
	fset := gotoken.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := goparser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, goparser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			switch v := decl.(type) {
			case *goast.FuncDecl:
				if v.Recv == nil && v.Name != nil && v.Name.Name == name {
					p := fset.Position(v.Name.Pos())
					return p.Filename, p.Line, p.Column, true
				}
			case *goast.GenDecl:
				for _, spec := range v.Specs {
					if ts, ok := spec.(*goast.TypeSpec); ok && ts.Name != nil && ts.Name.Name == name {
						p := fset.Position(ts.Name.Pos())
						return p.Filename, p.Line, p.Column, true
					}
				}
			}
		}
	}
	return "", 0, 0, false
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

// stdModuleDeclaring names the std module that declares sym: a top-level
// binding (an interface, a struct, a fn), a member of one (an interface
// method, a variant's field) or a type-body method (List.concat, in the
// file's TypeMethods). The declaration is matched by sameDeclaration, so
// another module's symbol of the same name at the same line and column is
// not taken. Only when no module holds the declaration itself, as for a
// per-site copy of a symbol that records no AST node (a field), is it
// matched by name and position; that fallback walks the modules in name
// order, so its answer does not change from run to run.
func (s *Server) stdModuleDeclaring(sym *analysis.Symbol) (string, bool) {
	names := make([]string, 0, len(s.std.Files))
	for name := range s.std.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	matchIn := func(fa *analysis.FileAnalysis, same func(a, b *analysis.Symbol) bool) bool {
		if fa == nil || fa.ModuleScope == nil {
			return false
		}
		if same(fa.ModuleScope.LookupLocal(sym.Name), sym) {
			return true
		}
		for _, parent := range fa.ModuleScope.Symbols {
			if same(parent.Members[sym.Name], sym) {
				return true
			}
		}
		for _, byName := range fa.TypeMethods {
			if same(byName[sym.Name], sym) {
				return true
			}
		}
		return false
	}
	for _, name := range names {
		if matchIn(s.std.Files[name], sameDeclaration) {
			return name, true
		}
	}
	samePosition := func(a, b *analysis.Symbol) bool { return a != nil && a.Name == b.Name && a.Pos == b.Pos }
	for _, name := range names {
		if matchIn(s.std.Files[name], samePosition) {
			return name, true
		}
	}
	return "", false
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
				targetURI = protocol.DocumentUri(s.stdFileURI(defSym.Name))
			}
		} else if modName, ok := s.stdModuleDeclaring(defSym); ok {
			targetURI = protocol.DocumentUri(s.stdFileURI(modName))
		}
	}

	// Location uses defSym so we land at the real definition,
	// not the click position. The "it" implicit-param check stays on
	// `clickedSym` because that's specifically about the local binding shape.
	at := defSym.Pos
	startLine := toZeroBased(at.Line)
	startChar := toZeroBased(at.Col)
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
		targetURI = protocol.DocumentUri(s.stdFileURI(moduleName))
	} else {
		// Resolve relative to the project root (so an import in a
		// sub-directory file resolves against the same root the runtime
		// uses, not the file's immediate parent).
		// A drill-through form (`import shape.Shape.{Circle}`) ends in the
		// type it selects through, which is not a path segment.
		filePath := ""
		for i := len(imp.ModulePath); i > 0; i-- {
			if path, ok := analysis.ImportFile(s.docs.FindProjectRoot(uriToPath(currentURI)), modPath[:i]); ok {
				filePath = path
				break
			}
			if _, isType := imp.ModulePath[i-1].(*ast.TypeIdent); !isType {
				break
			}
		}
		if filePath == "" {
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
// module-path key (Program().ImplIndex.ImplFiles); the FuncDef's own Line/Col
// place the cursor on the method name. Returns ok=false when the home
// module can't be determined, so the caller falls back to the interface
// method.
func (s *Server) dispatchImplLocation(fa *analysis.FileAnalysis, impl ast.ImplMethodDecl, openDocURI string) (*protocol.Location, bool) {
	if fa == nil || fa.Program().ImplIndex == nil || impl == nil {
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
		key, ok = fa.Program().ImplIndex.ImplFiles[n]
		line, col, name = n.Line, n.Col, n.Name
	case *ast.ExternFunc:
		key, ok = fa.Program().ImplIndex.ImplExternFiles[n]
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
// in IfaceMethodImplFiles / Program().ImplIndex.ImplFiles) to the file URI its
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
		return protocol.DocumentUri(s.stdFileURI(strings.TrimPrefix(key, "std/")))
	default:
		root := s.docs.FindProjectRoot(uriToPath(openDocURI))
		if path, ok := analysis.ImportFile(root, strings.Split(key, "/")); ok {
			return protocol.DocumentUri(pathToURI(path))
		}
		absPath := filepath.Join(root, filepath.FromSlash(key)) + ".nomi"
		return protocol.DocumentUri(pathToURI(absPath))
	}
}

// uriToPath converts a file:// URI to a filesystem path, unescaping it.
func uriToPath(uri string) string {
	if runtime.GOOS == "windows" {
		return fileuri.ToPath(uri)
	}
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
	return fileuri.FromPath(path)
}
