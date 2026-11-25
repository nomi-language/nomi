package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"slices"
	"sort"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// textDocumentImplementation answers `textDocument/implementation` requests.
// Powers Zed's `editor::GoToImplementation` (and the equivalent in every
// other LSP-aware editor). The handler resolves the click position to a
// symbol, then routes through the project-wide IfaceMethodImpls index to
// list the methods of `impl Iface for T` blocks that satisfy it, and the
// `derive` entry of each derived block that writes no function
// (shapeBlockLocations):
//
//   - Click on an interface name (e.g. `Display` in `impl Display for T { }` or
//     at a `Display.to_string(...)` call site): return locations for every
//     impl method of that interface across the project, regardless of which
//     method or which implementing type.
//   - Click on an interface method (the `to_string` in the interface's own
//     declaration, or the `to_string` in `Display.to_string(...)`): return
//     locations only for impls of that specific method.
//
// Anything else returns nil (no implementations). Go-to-definition handles
// the inverse direction — clicking on a concrete impl jumps to the
// interface method, not the other way around.
func (s *Server) textDocumentImplementation(ctx *glsp.Context, params *protocol.ImplementationParams) (any, error) {
	uri := string(params.TextDocument.URI)
	doc := s.requestDoc(ctx, uri)
	if doc == nil || doc.Analysis == nil {
		return nil, nil
	}

	pos := analysis.Pos{
		Line: int(params.Position.Line) + 1,
		Col:  utf16ToByteCol(doc.Content, params.Position.Line, params.Position.Character),
	}

	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		return nil, nil
	}
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}

	// A declaration a keyword declaration expanded into (a `phrase`'s
	// function, a `noun`'s value): its implementations are the input spans
	// its span map names, each catalog entry it was made of.
	if info := analysis.ExpansionOfNode(real.Node); info != nil {
		return s.toUTF16Locations(expansionLocations(info)), nil
	}

	switch real.Kind {
	case analysis.SymbolInterface, analysis.SymbolInterfaceMethod:
		if doc.FromRecord && s.docs.CheckWhole(uri) {
			// The impls of an interface are the whole program's, which a
			// document checked against interfaces sees only in part.
			return s.textDocumentImplementation(ctx, params)
		}
	}
	switch real.Kind {
	case analysis.SymbolInterface:
		return s.toUTF16Locations(s.implLocationsForInterface(doc.Analysis, real, uri)), nil
	case analysis.SymbolInterfaceMethod:
		owner := s.findInterfaceForMethod(doc.Analysis, real)
		if owner == nil {
			return nil, nil
		}
		return s.toUTF16Locations(s.implLocationsForMethod(doc.Analysis, owner, real.Name, uri)), nil
	}
	return nil, nil
}

// implLocationsForInterface returns one Location per method of an
// `impl Iface for T` block, across every method the interface declares.
// iface is the interface's symbol, and an impl of another interface of its
// name is not one of its (Program.ImplOfInterface).
func (s *Server) implLocationsForInterface(fa *analysis.FileAnalysis, iface *analysis.Symbol, openDocURI string) []protocol.Location {
	ifaceName := iface.Name
	it, _ := iface.Type.(*analysis.InterfaceType)
	shapes := s.shapeBlockLocations(fa, it, ifaceName, openDocURI)
	methodImpls, hasLocalImpls := fa.Program().IfaceMethodImpls[ifaceName]
	externImpls := map[string][]*ast.ExternFunc(nil)
	_, hasLocalExternImpls := fa.Program().IfaceMethodImplExterns[ifaceName]
	if hasLocalExternImpls {
		externImpls = fa.Program().IfaceMethodImplExterns[ifaceName]
	}
	if !hasLocalImpls && !hasLocalExternImpls && len(shapes) == 0 && fa.Program().ImplIndex != nil {
		if fa.Program().ImplIndex.IfaceMethodImpls != nil {
			methodImpls = fa.Program().ImplIndex.IfaceMethodImpls[ifaceName]
		}
		if fa.Program().ImplIndex.IfaceMethodImplExterns != nil {
			externImpls = fa.Program().ImplIndex.IfaceMethodImplExterns[ifaceName]
		}
	}
	if len(methodImpls) == 0 && len(externImpls) == 0 && len(shapes) == 0 {
		return nil
	}
	locs := shapes
	for _, fns := range methodImpls {
		for _, fn := range fns {
			if !fa.Program().ImplOfInterface(fn, it) {
				continue
			}
			if loc, ok := s.implLocationFor(fa, ifaceName, fn, openDocURI); ok {
				locs = append(locs, loc)
			}
		}
	}
	for _, exts := range externImpls {
		for _, ext := range exts {
			if !fa.Program().ImplOfInterface(ext, it) {
				continue
			}
			if loc, ok := s.implExternLocationFor(fa, ext, openDocURI); ok {
				locs = append(locs, loc)
			}
		}
	}
	return locs
}

// implLocationsForMethod returns one Location per `impl Iface for T { fn method(...) }`
// method for the specific (interface, method) pair, iface the interface's
// symbol.
func (s *Server) implLocationsForMethod(fa *analysis.FileAnalysis, iface *analysis.Symbol, methodName, openDocURI string) []protocol.Location {
	ifaceName := iface.Name
	it, _ := iface.Type.(*analysis.InterfaceType)
	// A block answered by shape writes none of the interface's functions,
	// so it answers each of them.
	shapes := s.shapeBlockLocations(fa, it, ifaceName, openDocURI)
	methodImpls, hasLocalImpls := fa.Program().IfaceMethodImpls[ifaceName]
	externImpls := map[string][]*ast.ExternFunc(nil)
	_, hasLocalExternImpls := fa.Program().IfaceMethodImplExterns[ifaceName]
	if hasLocalExternImpls {
		externImpls = fa.Program().IfaceMethodImplExterns[ifaceName]
	}
	if !hasLocalImpls && !hasLocalExternImpls && len(shapes) == 0 && fa.Program().ImplIndex != nil {
		if fa.Program().ImplIndex.IfaceMethodImpls != nil {
			methodImpls = fa.Program().ImplIndex.IfaceMethodImpls[ifaceName]
		}
		if fa.Program().ImplIndex.IfaceMethodImplExterns != nil {
			externImpls = fa.Program().ImplIndex.IfaceMethodImplExterns[ifaceName]
		}
	}
	fns := methodImpls[methodName]
	exts := externImpls[methodName]
	if len(fns) == 0 && len(exts) == 0 && len(shapes) == 0 {
		return nil
	}
	locs := shapes
	for _, fn := range fns {
		if !fa.Program().ImplOfInterface(fn, it) {
			continue
		}
		if loc, ok := s.implLocationFor(fa, ifaceName, fn, openDocURI); ok {
			locs = append(locs, loc)
		}
	}
	for _, ext := range exts {
		if !fa.Program().ImplOfInterface(ext, it) {
			continue
		}
		if loc, ok := s.implExternLocationFor(fa, ext, openDocURI); ok {
			locs = append(locs, loc)
		}
	}
	return locs
}

func (s *Server) implFiles(fa *analysis.FileAnalysis) map[*ast.FuncDef]string {
	if fa.Program().ImplIndex != nil && fa.Program().ImplIndex.ImplFiles != nil {
		return fa.Program().ImplIndex.ImplFiles
	}
	return fa.Program().IfaceMethodImplFiles
}

func (s *Server) implExternFiles(fa *analysis.FileAnalysis) map[*ast.ExternFunc]string {
	if fa.Program().ImplIndex != nil && fa.Program().ImplIndex.ImplExternFiles != nil {
		return fa.Program().ImplIndex.ImplExternFiles
	}
	return fa.Program().IfaceMethodImplExternFiles
}

// implLocationFor builds a Location for an impl FuncDef. The path
// stored in IfaceMethodImplFiles is the import-form module key (e.g.
// "foo" or "sub/log") — we resolve it to an absolute file path via the
// open document's project root. If the key is empty (the project entry
// file has no module-path key by design), we fall back to the open
// document's URI since "entry file" means "the file the editor has open
// and asked about."
//
// A function the front end wrote stands at a synthesized position
// (analysis.IsSynthesizedLine), which names no line of the file. A derive's
// answers its `derive Iface` entry (derivedLocation), read from the block
// that holds it in the file the impl index names. Any other, which comes
// with a type's declaration (a validated type's `TryFrom`, an opaque type's
// Debug), is no impl the source writes, and is not listed.
func (s *Server) implLocationFor(fa *analysis.FileAnalysis, ifaceName string, fn *ast.FuncDef, openDocURI string) (protocol.Location, bool) {
	key, ok := s.implFiles(fa)[fn]
	if !ok {
		return protocol.Location{}, false
	}
	uri := s.moduleKeyToURI(key, openDocURI)
	if analysis.IsSynthesizedLine(fn.Line) {
		for _, info := range implHome(fa, key).ImplBlocks[fa.Program().ImplBlockReceiver[fn]][ifaceName] {
			if info != nil && info.Block != nil && slices.Contains(info.Block.Items, ast.Node(fn)) {
				return derivedLocation(info.Block, uri)
			}
		}
		return protocol.Location{}, false
	}
	startLine := toZeroBased(fn.Line)
	startChar := toZeroBased(fn.Col)
	endChar := startChar + uint32(len(fn.Name))
	return protocol.Location{
		URI: uri,
		Range: protocol.Range{
			Start: protocol.Position{Line: startLine, Character: startChar},
			End:   protocol.Position{Line: startLine, Character: endChar},
		},
	}, true
}

func (s *Server) implExternLocationFor(fa *analysis.FileAnalysis, ext *ast.ExternFunc, openDocURI string) (protocol.Location, bool) {
	key, ok := s.implExternFiles(fa)[ext]
	if !ok {
		return protocol.Location{}, false
	}
	uri := s.moduleKeyToURI(key, openDocURI)
	startLine := toZeroBased(ext.Line)
	startChar := toZeroBased(ext.Col)
	return protocol.Location{
		URI: uri,
		Range: protocol.Range{
			Start: protocol.Position{Line: startLine, Character: startChar},
			End:   protocol.Position{Line: startLine, Character: startChar + uint32(len(ext.Name))},
		},
	}, true
}

// shapeBlockLocations answers the `derive` entry of each block of the
// interface it, named ifaceName, that the program's own files write with
// no function: a derived Debug, Display, Equatable or Hashable, which the
// type's shape answers. The impl-method index, which lists functions, holds
// none of them. A file of the stdlib is the program's own only when it is
// the one asked about.
func (s *Server) shapeBlockLocations(fa *analysis.FileAnalysis, it *analysis.InterfaceType, ifaceName, openDocURI string) []protocol.Location {
	var locs []protocol.Location
	for _, f := range fa.Program().Files() {
		if f != fa && analysis.IsStdlibKey(f.Origin) {
			continue
		}
		uri := protocol.DocumentUri(openDocURI)
		if f != fa {
			uri = protocol.DocumentUri(pathToURI(f.FilePath))
		}
		recvs := make([]string, 0, len(f.ImplBlocks))
		for recv := range f.ImplBlocks {
			recvs = append(recvs, recv)
		}
		sort.Strings(recvs)
		for _, recv := range recvs {
			for _, info := range f.ImplBlocks[recv][ifaceName] {
				if info == nil || info.Block == nil || len(info.Block.Items) > 0 || !analysis.IsSynthesizedLine(info.Block.Line) {
					continue
				}
				if it != nil && (info.Interface != it.Name || info.Origin != it.Origin) {
					continue
				}
				if loc, ok := derivedLocation(info.Block, uri); ok {
					locs = append(locs, loc)
				}
			}
		}
	}
	return locs
}

// derivedLocation is the `derive Iface` entry that wrote blk, an impl block
// of the file at uri, as analysis.WrittenFor answers it. ok is false for a
// block no derive wrote.
func derivedLocation(blk *ast.ImplBlock, uri protocol.DocumentUri) (protocol.Location, bool) {
	at, span, derived, ok := analysis.WrittenFor(blk)
	if !ok || !derived {
		return protocol.Location{}, false
	}
	loc := makeLocation(string(uri), at, "")
	loc.Range.End.Character += uint32(span)
	return loc, true
}

// implHome is the analysis of the file with module key key, the home of an
// impl that the program fa is of holds, and fa itself when the program
// lists no file under the key.
func implHome(fa *analysis.FileAnalysis, key string) *analysis.FileAnalysis {
	if home := fa.Program().File(key); home != nil {
		return home
	}
	return fa
}

// interfaceRefPos returns the 1-based source position + name length of an
// interface type-expr (the precise entry token, e.g. `Hashable` in
// `derive Hashable`). Falls back to the supplied decorator position for an
// unexpected shape.
func interfaceRefPos(te ast.TypeExpr, fbLine, fbCol int, name string) (line, col, nameLen int) {
	switch t := te.(type) {
	case *ast.SimpleType:
		return t.Line, t.Col, len(t.Name)
	case *ast.QualifiedType:
		return interfaceRefPos(t.Member, fbLine, fbCol, name)
	case *ast.GenericType:
		return t.Line, t.Col, len(t.Name)
	}
	return fbLine, fbCol, len(name)
}

// sameDeclaration reports whether two symbols stand for one declaration:
// the same symbol, or a per-site copy of it (the checker records copies at
// call sites), which keeps the declaring AST node. A name and a position do
// not identify a declaration on their own, since two files can each declare
// a method `go` at 2:6.
func sameDeclaration(a, b *analysis.Symbol) bool {
	if a == nil || b == nil {
		return false
	}
	if a == b {
		return true
	}
	return a.Node != nil && a.Node == b.Node && a.Kind == b.Kind && a.Name == b.Name && a.Pos == b.Pos
}

// findInterfaceForMethod resolves which interface owns the given method
// symbol by walking every interface symbol in scope and checking whether
// its Members hold that method (sameDeclaration). The interface→method
// relationship isn't stored as a back-pointer on the method symbol (an
// InterfaceMethod AST node carries no parent reference), so the lookup
// goes through Members instead. The answer is the owner's symbol, whose
// declaration is the interface's identity; nil when no owner is found.
func (s *Server) findInterfaceForMethod(fa *analysis.FileAnalysis, method *analysis.Symbol) *analysis.Symbol {
	scopes := []*analysis.Scope{fa.ModuleScope}
	if p := fa.Program(); p != nil {
		// An interface this file names through its module (`a.Show.show`)
		// is in the scope of the file that declares it.
		for _, f := range p.Files() {
			if f != nil && f != fa {
				scopes = append(scopes, f.ModuleScope)
			}
		}
	}
	for _, scope := range scopes {
		if scope == nil {
			continue
		}
		for _, parent := range scope.Symbols {
			owner := parent
			if owner.Resolved != nil {
				owner = owner.Resolved
			}
			if owner.Kind != analysis.SymbolInterface {
				continue
			}
			if sameDeclaration(owner.Members[method.Name], method) {
				return owner
			}
		}
	}
	// Stdlib interfaces aren't in the user file's ModuleScope; check the
	// stdlib modules registered with the document manager.
	if s.std != nil {
		for _, modScope := range s.std.Modules {
			for _, parent := range modScope.Symbols {
				if parent.Kind != analysis.SymbolInterface {
					continue
				}
				if sameDeclaration(parent.Members[method.Name], method) {
					return parent
				}
			}
		}
	}
	return nil
}

// findInterfaceMethodSymbol returns the interface method an impl method
// implements. The interface is the one the impl's header names, read from
// the reference the checker recorded at that name, so an interface that
// only shares the name is never taken. When the header recorded none (a
// synthesized impl), the interface is looked up by name in the file's
// scope, then in the std modules in name order.
func (s *Server) findInterfaceMethodSymbol(fa *analysis.FileAnalysis, impl *analysis.Symbol) *analysis.Symbol {
	methodOf := func(iface *analysis.Symbol) *analysis.Symbol {
		for iface != nil && iface.Resolved != nil {
			iface = iface.Resolved
		}
		if iface == nil || iface.Kind != analysis.SymbolInterface {
			return nil
		}
		if method := iface.Members[impl.Name]; method != nil && method.Kind == analysis.SymbolInterfaceMethod {
			return method
		}
		return nil
	}
	if fa != nil {
		if method := methodOf(implHeaderInterface(fa, impl)); method != nil {
			return method
		}
		if fa.ModuleScope != nil {
			if method := methodOf(fa.ModuleScope.Lookup(impl.ImplInterface)); method != nil {
				return method
			}
		}
	}
	if s.std != nil {
		names := make([]string, 0, len(s.std.Modules))
		for name := range s.std.Modules {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if method := methodOf(s.std.Modules[name].LookupLocal(impl.ImplInterface)); method != nil {
				return method
			}
		}
	}
	return nil
}

// implHeaderInterface returns the symbol the checker recorded at the
// interface name of the impl header impl's function belongs to (`ToJson`
// in `impl json.ToJson for Point`), or nil.
func implHeaderInterface(fa *analysis.FileAnalysis, impl *analysis.Symbol) *analysis.Symbol {
	var te ast.TypeExpr
	switch n := impl.Node.(type) {
	case *ast.FuncDef:
		te = n.ImplIface
	case *ast.ExternFunc:
		te = n.ImplIface
	}
	if te == nil {
		return nil
	}
	line, col, _ := interfaceRefPos(te, 0, 0, "")
	if line <= 0 || analysis.IsSynthesizedLine(line) {
		return nil
	}
	return fa.References[analysis.Pos{Line: line, Col: col}]
}
