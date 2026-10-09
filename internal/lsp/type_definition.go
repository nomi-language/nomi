package lsp

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// textDocumentTypeDefinition jumps from a value to its type's
// declaration: a binding, parameter, field or `once` to its type; a
// function or a call of one to its result type; a type name to itself.
// A generic instantiation (`Maybe<User>`) lands on the generic type. A
// type with no declaration (a function type, a tuple, a type parameter)
// answers nothing.
func (s *Server) textDocumentTypeDefinition(_ *glsp.Context, params *protocol.TypeDefinitionParams) (any, error) {
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
	name, origin, owner, ok := typeDeclKey(valueType(sym))
	if !ok {
		return nil, nil
	}
	loc := s.typeDeclLocation(doc.Analysis, uri, name, origin, owner)
	if loc == nil {
		return nil, nil
	}
	return s.toUTF16Location(loc), nil
}

// valueType is the type a type definition request on sym means: a
// function's result type, else the symbol's own type.
func valueType(sym *analysis.Symbol) analysis.Type {
	t := sym.Type
	if t == nil && sym.Resolved != nil {
		t = sym.Resolved.Type
	}
	if sym.CallType != nil {
		t = sym.CallType
	}
	if ft, ok := analysis.ResolveTypeVar(t).(*analysis.FuncType); ok {
		switch sym.Kind {
		case analysis.SymbolFunction, analysis.SymbolInterfaceMethod, analysis.SymbolEnumVariant:
			return ft.Return
		}
	}
	return t
}

// typeDeclKey names a type's declaration: its name, its declaring file's
// build key (Origin: "std/<module>", "<entry>" for the file under
// analysis, a project path key) and, for a type declared in another
// project file, that file's path. A built-in has no Origin. ok is false
// for a type that no declaration names.
func typeDeclKey(t analysis.Type) (name, origin, owner string, ok bool) {
	switch v := analysis.ResolveTypeVar(t).(type) {
	case *analysis.StructType:
		return v.Name, v.Origin, v.OwningSourceFile, true
	case *analysis.EnumType:
		return v.Name, v.Origin, v.OwningSourceFile, true
	case *analysis.DistinctType:
		return v.Name, v.Origin, v.OwningSourceFile, true
	case *analysis.InterfaceType:
		return v.Name, v.Origin, "", true
	case *analysis.PrimitiveType:
		return v.Name_, v.Origin, "", true
	case *analysis.ListType:
		return "List", "", "", true
	case *analysis.MapType:
		return "Map", "", "", true
	}
	return "", "", "", false
}

// typeDeclLocation finds the declaration of the type typeDeclKey named.
func (s *Server) typeDeclLocation(fa *analysis.FileAnalysis, uri, name, origin, owner string) *protocol.Location {
	if mod, ok := strings.CutPrefix(origin, "std/"); ok {
		return s.stdTypeLocation(name, mod)
	}
	if origin == "" {
		return s.stdTypeLocation(name, "")
	}
	if owner == "" && origin == analysis.OriginEntry {
		if sym := fa.ModuleScope.LookupLocal(name); sym != nil {
			loc := makeLocation(uri, sym.Pos, name)
			return &loc
		}
		return nil
	}
	if owner == "" {
		root := s.docs.FindProjectRoot(uriToPath(uri))
		owner = analysis.ResolveModulePath(root, strings.Split(origin, "/"))
	}
	for _, d := range s.docs.FileDecls(filepath.Clean(owner)) {
		if d.Name == name && isTypeDecl(d.Kind) {
			loc := makeLocation(pathToURI(owner), d.Pos, name)
			return &loc
		}
	}
	return nil
}

func isTypeDecl(k analysis.SymbolKind) bool {
	switch k {
	case analysis.SymbolStruct, analysis.SymbolEnum, analysis.SymbolType, analysis.SymbolTypeAlias, analysis.SymbolInterface:
		return true
	}
	return false
}

// stdTypeLocation is a stdlib type's declaration in the materialized
// stdlib, the files go-to-definition opens. With no module (a built-in
// such as Int or List), it is the module that declares the name.
func (s *Server) stdTypeLocation(name, mod string) *protocol.Location {
	if s.std == nil {
		return nil
	}
	mods := []string{mod}
	if mod == "" {
		mods = mods[:0]
		for m := range s.std.Nodes {
			mods = append(mods, m)
		}
		sort.Strings(mods)
	}
	for _, m := range mods {
		for _, d := range analysis.TopLevelDecls(s.std.Nodes[m]) {
			if d.Name == name && isTypeDecl(d.Kind) && !analysis.IsSynthesizedLine(d.Pos.Line) {
				loc := makeLocation(s.stdFileURI(m), d.Pos, name)
				return &loc
			}
		}
	}
	return nil
}
