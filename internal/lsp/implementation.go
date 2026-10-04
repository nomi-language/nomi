package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// textDocumentImplementation answers `textDocument/implementation` requests.
// Powers Zed's `editor::GoToImplementation` (and the equivalent in every
// other LSP-aware editor). The handler resolves the click position to a
// symbol, then routes through the project-wide IfaceMethodImpls index to
// list the methods of `impl Iface for T` blocks that satisfy it:
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
	doc := s.docs.Snapshot(uri)
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

	switch real.Kind {
	case analysis.SymbolInterface:
		return s.toUTF16Locations(s.implLocationsForInterface(doc.Analysis, real.Name, uri)), nil
	case analysis.SymbolInterfaceMethod:
		ifaceName := s.findInterfaceForMethod(doc.Analysis, real)
		if ifaceName == "" {
			return nil, nil
		}
		return s.toUTF16Locations(s.implLocationsForMethod(doc.Analysis, ifaceName, real.Name, uri)), nil
	}
	return nil, nil
}

// implLocationsForInterface returns one Location per method of an
// `impl Iface for T` block, across every method the interface declares.
func (s *Server) implLocationsForInterface(fa *analysis.FileAnalysis, ifaceName, openDocURI string) []protocol.Location {
	methodImpls, hasLocalImpls := fa.IfaceMethodImpls[ifaceName]
	externImpls := map[string][]*ast.ExternFunc(nil)
	_, hasLocalExternImpls := fa.IfaceMethodImplExterns[ifaceName]
	if hasLocalExternImpls {
		externImpls = fa.IfaceMethodImplExterns[ifaceName]
	}
	if !hasLocalImpls && !hasLocalExternImpls && fa.ProjectImpls != nil {
		if fa.ProjectImpls.IfaceMethodImpls != nil {
			methodImpls = fa.ProjectImpls.IfaceMethodImpls[ifaceName]
		}
		if fa.ProjectImpls.IfaceMethodImplExterns != nil {
			externImpls = fa.ProjectImpls.IfaceMethodImplExterns[ifaceName]
		}
	}
	if len(methodImpls) == 0 && len(externImpls) == 0 {
		return nil
	}
	var locs []protocol.Location
	for _, fns := range methodImpls {
		for _, fn := range fns {
			if loc, ok := s.implLocationFor(fa, ifaceName, fn, openDocURI); ok {
				locs = append(locs, loc)
			}
		}
	}
	for _, exts := range externImpls {
		for _, ext := range exts {
			if loc, ok := s.implExternLocationFor(fa, ext, openDocURI); ok {
				locs = append(locs, loc)
			}
		}
	}
	return locs
}

// implLocationsForMethod returns one Location per `impl Iface for T { fn method(...) }`
// method for the specific (interface, method) pair.
func (s *Server) implLocationsForMethod(fa *analysis.FileAnalysis, ifaceName, methodName, openDocURI string) []protocol.Location {
	methodImpls, hasLocalImpls := fa.IfaceMethodImpls[ifaceName]
	externImpls := map[string][]*ast.ExternFunc(nil)
	_, hasLocalExternImpls := fa.IfaceMethodImplExterns[ifaceName]
	if hasLocalExternImpls {
		externImpls = fa.IfaceMethodImplExterns[ifaceName]
	}
	if !hasLocalImpls && !hasLocalExternImpls && fa.ProjectImpls != nil {
		if fa.ProjectImpls.IfaceMethodImpls != nil {
			methodImpls = fa.ProjectImpls.IfaceMethodImpls[ifaceName]
		}
		if fa.ProjectImpls.IfaceMethodImplExterns != nil {
			externImpls = fa.ProjectImpls.IfaceMethodImplExterns[ifaceName]
		}
	}
	fns := methodImpls[methodName]
	exts := externImpls[methodName]
	if len(fns) == 0 && len(exts) == 0 {
		return nil
	}
	var locs []protocol.Location
	for _, fn := range fns {
		if loc, ok := s.implLocationFor(fa, ifaceName, fn, openDocURI); ok {
			locs = append(locs, loc)
		}
	}
	for _, ext := range exts {
		if loc, ok := s.implExternLocationFor(fa, ext, openDocURI); ok {
			locs = append(locs, loc)
		}
	}
	return locs
}

func (s *Server) implFiles(fa *analysis.FileAnalysis) map[*ast.FuncDef]string {
	if fa.ProjectImpls != nil && fa.ProjectImpls.ImplFiles != nil {
		return fa.ProjectImpls.ImplFiles
	}
	return fa.IfaceMethodImplFiles
}

func (s *Server) implExternFiles(fa *analysis.FileAnalysis) map[*ast.ExternFunc]string {
	if fa.ProjectImpls != nil && fa.ProjectImpls.ImplExternFiles != nil {
		return fa.ProjectImpls.ImplExternFiles
	}
	return fa.IfaceMethodImplExternFiles
}

// implLocationFor builds a Location for an impl FuncDef. The path
// stored in IfaceMethodImplFiles is the import-form module key (e.g.
// "foo" or "sub/log") — we resolve it to an absolute file path via the
// open document's project root. If the key is empty (the project entry
// file has no module-path key by design), we fall back to the open
// document's URI since "entry file" means "the file the editor has open
// and asked about."
//
// A derive SYNTHESIZED impl (`derive Iface`) has no hand-written
// method — its FuncDef sits in the synth-band (Line >= synthLineBase, ~2^30),
// a fake position the editor can't navigate to. For those we remap to the
// real `derive Iface` conformance entry instead (see
// derivedConformanceLocation); if that can't be recovered we drop the target
// (ok=false) rather than emit an un-navigable ghost.
func (s *Server) implLocationFor(fa *analysis.FileAnalysis, ifaceName string, fn *ast.FuncDef, openDocURI string) (protocol.Location, bool) {
	key, ok := s.implFiles(fa)[fn]
	if !ok {
		return protocol.Location{}, false
	}
	uri := s.moduleKeyToURI(key, openDocURI)
	if analysis.IsSynthesizedLine(fn.Line) {
		return s.derivedConformanceLocation(fa, ifaceName, fn, uri)
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

// derivedConformanceLocation maps a synthesized derive-impl method back to the
// real `derive Iface` conformance entry that produced it. The
// lowering stamps a synthetic `@derive Iface` decorator on the receiver type's
// declaration carrying the conformance entry's real source position (its arg is
// the entry's interface type-expr); we recover the receiver type via
// ImplBlockReceiver, then read that decorator. Returns ok=false when any link
// is missing, so the caller drops the (un-navigable) synth target.
func (s *Server) derivedConformanceLocation(fa *analysis.FileAnalysis, ifaceName string, fn *ast.FuncDef, uri protocol.DocumentUri) (protocol.Location, bool) {
	recv := fa.ImplBlockReceiver[fn]
	if recv == "" {
		return protocol.Location{}, false
	}
	sym := fa.ModuleScope.LookupLocal(recv)
	if sym == nil {
		return protocol.Location{}, false
	}
	for _, dec := range typeDeclDecorators(sym.Node) {
		if dec.Name != "derive" || len(dec.Args) == 0 {
			continue
		}
		arg, ok := dec.Args[0].(ast.TypeExpr)
		if !ok || analysis.TypeExprBaseName(arg) != ifaceName {
			continue
		}
		line, col, nameLen := interfaceRefPos(arg, dec.Line, dec.Col, ifaceName)
		startLine := toZeroBased(line)
		startChar := toZeroBased(col)
		return protocol.Location{
			URI: uri,
			Range: protocol.Range{
				Start: protocol.Position{Line: startLine, Character: startChar},
				End:   protocol.Position{Line: startLine, Character: startChar + uint32(nameLen)},
			},
		}, true
	}
	return protocol.Location{}, false
}

// typeDeclDecorators returns the decorator list of a type declaration node
// (struct / enum / type / host type), or nil for anything else.
func typeDeclDecorators(n ast.Node) []ast.Decorator {
	switch d := n.(type) {
	case *ast.StructDef:
		return d.Decorators
	case *ast.EnumDef:
		return d.Decorators
	case *ast.TypeDef:
		return d.Decorators
	case *ast.ExternType:
		return d.Decorators
	}
	return nil
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

// findInterfaceForMethod resolves which interface owns the given method
// symbol by walking every interface symbol in scope and checking its
// Members map for a method whose Pos matches. The interface→method
// relationship isn't stored as a back-pointer on the method symbol (an
// InterfaceMethod AST node carries no parent reference), so the lookup
// goes through Members instead. Returns "" when no owner is found.
func (s *Server) findInterfaceForMethod(fa *analysis.FileAnalysis, method *analysis.Symbol) string {
	for _, parent := range fa.ModuleScope.Symbols {
		owner := parent
		if owner.Resolved != nil {
			owner = owner.Resolved
		}
		if owner.Kind != analysis.SymbolInterface {
			continue
		}
		for _, m := range owner.Members {
			if m.Pos == method.Pos {
				return owner.Name
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
				for _, m := range parent.Members {
					if m.Pos == method.Pos {
						return parent.Name
					}
				}
			}
		}
	}
	return ""
}

func (s *Server) findInterfaceMethodSymbol(fa *analysis.FileAnalysis, ifaceName, methodName string) *analysis.Symbol {
	if fa != nil && fa.ModuleScope != nil {
		if iface := fa.ModuleScope.Lookup(ifaceName); iface != nil {
			if iface.Resolved != nil {
				iface = iface.Resolved
			}
			if iface.Kind == analysis.SymbolInterface {
				if method := iface.Members[methodName]; method != nil && method.Kind == analysis.SymbolInterfaceMethod {
					return method
				}
			}
		}
	}
	if s.std != nil {
		for _, modScope := range s.std.Modules {
			iface := modScope.LookupLocal(ifaceName)
			if iface == nil || iface.Kind != analysis.SymbolInterface {
				continue
			}
			if method := iface.Members[methodName]; method != nil && method.Kind == analysis.SymbolInterfaceMethod {
				return method
			}
		}
	}
	return nil
}
