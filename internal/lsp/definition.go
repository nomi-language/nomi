package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func (s *Server) textDocumentDefinition(ctx *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	uri := string(params.TextDocument.URI)
	doc := s.requestDoc(ctx, uri)
	if doc == nil {
		return nil, nil
	}

	// Convert LSP 0-based to analysis 1-based
	pos := analysis.Pos{
		Line: int(params.Position.Line) + 1,
		Col:  utf16ToByteCol(doc.Content, params.Position.Line, params.Position.Character),
	}

	// An `embed` pattern opens the files and folders it matched (embed.go).
	if info, i, ok := embedPatternAt(doc.Nodes, pos); ok {
		if locs := embedPatternLocations(info, i); len(locs) > 0 {
			return locs, nil
		}
		return nil, nil
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
		return nil, nil
	}
	// A field of an anonymous struct whose declaration the checker does
	// not know stands at the use; the use is not its definition.
	if sym.Undeclared {
		return nil, nil
	}
	_, tokenStart, _, tokenOK := doc.Analysis.TokenAt(pos)

	// A punned label that reads a binding (`Birth{on:}`, `{..b, on:}`,
	// `json(status:)`) is two names: it offers the field's or parameter's
	// declaration and the binding's.
	if pun, ok := hoverdoc.PunAt(doc.Analysis, doc.Nodes, pos); ok && pun.Kind != hoverdoc.PunPattern {
		if locs := s.punDefinitions(uri, pun); len(locs) > 0 {
			return s.toUTF16Locations(locs), nil
		}
	}

	// A punned pattern field (`{home:} = routes.public`) defines the
	// binding it names: its definition is the field it reads.
	if field := doc.Analysis.PunnedFieldLabels[tokenStart]; tokenOK && field != nil && doc.Analysis.DefinitionAt(tokenStart) == sym {
		return s.toUTF16Location(s.definitionLocationForSymbol(uri, field, field)), nil
	}

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

// punDefinitions are a punned label's two declarations: the field's or
// parameter's, then the binding's it reads.
func (s *Server) punDefinitions(uri string, pun hoverdoc.Pun) []protocol.Location {
	var locs []protocol.Location
	label := pun.Field
	if pun.Kind == hoverdoc.PunArgument && pun.HasParam && pun.Callee != nil {
		// The parameter is declared in the callee's file, at its position
		// in the parameter list.
		param := *pun.Callee
		if param.Resolved != nil {
			param = *param.Resolved
		}
		param.Name = pun.Param.Name
		param.Pos = analysis.Pos{Line: pun.Param.Line, Col: pun.Param.Col}
		// The parameter's own place, not where the callee's declaration
		// is recorded to be.
		param.DefinitionFile, param.DefinitionLine, param.DefinitionCol, param.DefinitionSpan = "", 0, 0, 0
		label = &param
	}
	if label != nil && !label.Undeclared && label.Kind != analysis.SymbolArgHint {
		if loc := s.definitionLocationForSymbol(uri, label, label); loc != nil {
			locs = append(locs, *loc)
		}
	}
	if loc := s.definitionLocationForSymbol(uri, pun.Binding, pun.Binding); loc != nil {
		locs = append(locs, *loc)
	}
	return locs
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

// definitionLocationForSymbol is where the source declares defSym, a symbol
// read in the document at uri: its own position, or the one recorded for a
// symbol whose own declaration is not in the source (Symbol.DefinitionLine:
// a function the front end wrote for a declaration answers that
// declaration, analysis.WrittenFor). A symbol at a synthesized position
// with nothing recorded has no place in the source, and the answer is nil.
func (s *Server) definitionLocationForSymbol(uri string, defSym, clickedSym *analysis.Symbol) *protocol.Location {
	if defSym == nil {
		return nil
	}
	at, span := defSym.Pos, len(defSym.Name)
	switch {
	case defSym.DefinitionLine > 0 && defSym.DefinitionCol > 0:
		at = analysis.Pos{Line: defSym.DefinitionLine, Col: defSym.DefinitionCol}
		if defSym.DefinitionSpan > 0 {
			span = defSym.DefinitionSpan
		}
	case analysis.IsSynthesizedLine(at.Line):
		return nil
	case clickedSym != nil && clickedSym.Kind == analysis.SymbolParam && clickedSym.Name == "it":
		// The implicit `it` parameter is defined at the lambda's `{`, where
		// no `it` token stands: the range is that one character.
		span = 1
	}
	loc := makeLocation(string(s.declaringURI(uri, defSym)), at, "")
	loc.Range.End.Character += uint32(span)
	return &loc
}

// declaringURI is the file that declares sym, a symbol read in the document
// at uri: a std module's file for a module's name, the file a user file's
// symbol records (SourceFile), the document itself for a declaration of its
// own, and otherwise the std module that declares it (stdModuleDeclaring).
// The document's own declarations are asked before the std modules: a std
// file open as a document, like a document with no path, records no
// SourceFile, and stdModuleDeclaring's match by name and position would
// take another module's declaration of that name at that place.
func (s *Server) declaringURI(uri string, sym *analysis.Symbol) protocol.DocumentUri {
	if sym.DefinitionFile != "" {
		return protocol.DocumentUri(pathToURI(sym.DefinitionFile))
	}
	if s.std != nil && sym.Kind == analysis.SymbolModule {
		if _, ok := s.std.Modules[sym.Name]; ok {
			return protocol.DocumentUri(s.stdFileURI(sym.Name))
		}
	}
	if sym.SourceFile != "" {
		return protocol.DocumentUri(pathToURI(sym.SourceFile))
	}
	if doc := s.docs.Snapshot(uri); doc != nil && doc.Analysis != nil && sameDeclaration(doc.Analysis.Definitions[sym.Pos], sym) {
		return protocol.DocumentUri(uri)
	}
	if s.std != nil && sym.Kind != analysis.SymbolModule {
		if modName, ok := s.stdModuleDeclaring(sym); ok {
			return protocol.DocumentUri(s.stdFileURI(modName))
		}
	}
	return protocol.DocumentUri(uri)
}

// resolveImportDefinition resolves go-to-def for symbols that came from an import statement.
// For `{self}` owner imports, jumps to the module file.
// For item imports (import models.{User, Point}), jumps to the definition in the module file.
// Stdlib imports (import std::lists.List, import std::option.Option) jump to the
// materialised stdlib file URI rather than a project-relative path.
func (s *Server) resolveImportDefinition(currentURI string, imp *ast.ImportStmt, sym *analysis.Symbol) (any, error) {
	// Build the module path string
	modPath := make([]string, len(imp.ModulePath))
	for i, node := range imp.ModulePath {
		modPath[i] = ast.ImportNodeName(node)
	}
	// A file reached through a directory import (`admin::users` for
	// `import views::admin`) is the directory's path with the use's
	// segments after it.
	if sym.Kind == analysis.SymbolModule && strings.Contains(sym.Name, ast.PathSeparator) {
		rest := strings.Split(sym.Name, ast.PathSeparator)[1:]
		modPath = append(modPath, rest...)
		imp = &ast.ImportStmt{ModulePath: append(append([]ast.Node(nil), imp.ModulePath...), pathNodes(rest)...)}
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
	if analysis.IsSynthesizedLine(line) {
		// A function the front end wrote: where its symbol says the source
		// declares it (definitionLocationForSymbol), read from the file
		// that holds it.
		home := implHome(fa, key)
		loc := s.definitionLocationForSymbol(string(uri), home.Definitions[analysis.Pos{Line: line, Col: col}], nil)
		return loc, loc != nil
	}
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
		if path, ok := analysis.FileOfKey(root, key); ok {
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

// pathNodes are names as an import path's segments.
func pathNodes(names []string) []ast.Node {
	out := make([]ast.Node, len(names))
	for i, name := range names {
		out[i] = &ast.Ident{Name: name}
	}
	return out
}
