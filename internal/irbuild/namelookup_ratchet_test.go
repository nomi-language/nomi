package irbuild

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// The builder finds a declaration by the declaration the checker resolved,
// not by a name it spells again: a name looked up in this file's tables
// finds whatever this file binds it to, which is not always what the
// checker bound at the use (package doc, "Calls"). What still goes by name
// is counted here, and the counts only go down. A site that resolves no
// name is listed in notResolutions instead, with its reason.
const (
	// maxNamedLookups is how many functions of this package find a
	// declaration from a name: those named `...Named` that take a string.
	maxNamedLookups = 0
	// maxNameTableReads is how many places read a name-keyed declaration
	// table by a name: `funcs[name]`, `types[name]`, `ifaces[name]`,
	// `onces[name]`: none.
	maxNameTableReads = 0
	// maxLookupTableReads is how many places read one of lookupTables by a
	// name: the generic templates and owner blocks: none.
	maxLookupTableReads = 0
	// maxUncheckedSynthesizedBodies is how many sites lower code the
	// checker never checked (calls of markUnchecked): madeIdent, which
	// every identifier the builder writes goes through.
	maxUncheckedSynthesizedBodies = 1
	// maxAnchorTableReads is how many places read one of anchorTables by a
	// name: a std declaration's representation found by its spelling.
	maxAnchorTableReads = 0
	// maxStdIndexReads is how many places read the std index by a spelling
	// (stdIndexTables) rather than by the identity a record carries
	// (`byID`, `byIface`).
	maxStdIndexReads = 0
	// maxIfaceNameLookupCalls is how many calls find an interface's def
	// from a name (ifaceNameLookups) rather than from the checker's record
	// of an impl block's interface (implBlockRecord) or of a type
	// (ifaceOfType): none.
	maxIfaceNameLookupCalls = 0
	// maxKindImports is how many calls translate a kind of another file's
	// gen into this gen's mirrors (kindImports).
	maxKindImports = 74
	// maxMirrorCacheWrites is how many places intern a mirror of another
	// file's declaration in this gen (mirrorCaches).
	maxMirrorCacheWrites = 5
	// maxScopeSymbolLookups is how many calls look a name up in a scope
	// (scopeSymbolLookups) outside notResolutions: none. Those left find
	// the declaration a type identity (module, name) names in its
	// declaring module's own scope, which is what the identity means.
	maxScopeSymbolLookups = 0
)

// scopeSymbolLookups are the functions that resolve a name in a scope.
var scopeSymbolLookups = map[string]bool{
	"resolvedTypeSymbol":  true,
	"resolvedScopeSymbol": true,
}

// anchorTables are the name-keyed tables that bind a std or host
// declaration to its representation: a gen's std interface, generic struct
// and operator interface anchors, and a file's `host type`s and Go
// packages.
var anchorTables = map[string]bool{
	"stdIfaceByName": true,
	"genStructs":     true,
	"operAnchors":    true,
	"hostTypes":      true,
	"hostPkgs":       true,
}

// stdIndexTables are the std index's tables by spelling (`Owner.name`,
// `module.name`, `Type.name`) and a std gen's own `once`s by `Owner.name`.
var stdIndexTables = map[string]bool{
	"byType":   true,
	"byFile":   true,
	"byOnce":   true,
	"stdOnces": true,
}

// ifaceNameLookups are the functions that find an interface's def from
// its name in this gen's scope.
var ifaceNameLookups = map[string]bool{
	"ifaceNamed":        true,
	"stdIfaceNamed":     true,
	"stdProgIfaceNamed": true,
	"ifaceTemplate":     true,
	"dictBoundIface":    true,
}

// notResolution is a function whose counted sites (of every class below)
// resolve no name: how many it holds, and why none is a resolution.
type notResolution struct {
	sites int
	why   string
}

// notResolutions are the counted sites that resolve no name, by file and
// enclosing function. TestNameLookupsOnlyGoDown counts them apart and fails
// when a function holds more or fewer sites than its entry says, so a new
// spelled lookup in one of them is seen.
var notResolutions = map[string]notResolution{
	// Indexes built from declarations, each under its own name in its own
	// unit, which the lookups below the line read by a declaration.
	"types.go declareTypeShells":             {4, "builds the module's index of its own type declarations"},
	"impl.go declareIfaces":                  {5, "builds the module's index of its own interface declarations"},
	"once.go declareOnce":                    {2, "builds the module's index of its own `once` bindings"},
	"native.go declareFunc":                  {1, "builds the module's index of its own function signatures"},
	"generictype.go collectGenericTemplates": {2, "builds the module's index of its own generic templates"},
	"ownerimpl.go registerOwnerImpl":         {2, "files the module's owner blocks under their receiver's own name, which resolveOwnerMembers reads back by its own keys"},
	"siblingimpl.go resolveOwnerMembers":     {1, "reads registerOwnerImpl's index back by its own keys, in sorted order, keyed by each receiver's declaration"},
	"hostpkg.go collectHostTypes":            {1, "builds the file's index of its own `host type`s"},
	"stdlib.go stdlibHostFunc":               {2, "builds the host registry under each binding row's own name"},
	"stdlib.go add":                          {2, "builds the std index"},
	"stdlib.go underlay":                     {2, "lays an earlier module's index under a std module's own"},
	"stdonce.go addOnce":                     {2, "builds the std index of `once`s"},
	"stdonce.go bindStdOnces":                {1, "builds a std gen's index of its own `once`s"},
	"stdedited.go filtered":                  {2, "copies the std index, filtered"},
	// A declaration's own entry, read under its own name in its own unit and
	// kept only when it is that declaration's.
	"native.go funcDecl":           {1, "the declaration's own signature, kept only when its declaration is this one"},
	"hostpkg.go hostFnDecl":        {1, "the `host fn`'s own signature, kept only when its declaration is this one"},
	"stdhost.go projectOriginHost": {1, "a declaring gen's own `host type` under its own name, kept only when that gen's scope binds the checker's type"},
	"stdonce.go stdFileOnceValue":  {1, "a std module's own file-level `once` by its own name, for a copy of the module checked from edited text, whose declarations are other nodes than the shared copy's"},
	// Names the builder makes.
	"session.go Func":                 {1, "a session's functions by linkage name"},
	"session.go Commit":               {1, "a session's functions by linkage name"},
	"session.go FuncsUnder":           {1, "a session's functions by linkage name, the ones under an impl block's prefix"},
	"perfileharness.go irModuleNamed": {1, "an IR module by its path, in the per-file lowering harness"},
	"preludefn.go preludeSpecNamed":   {1, "the builder's own prelude spec table, by a spec name the builder writes"},
	"stdinst.go listedModulesNamed":   {1, "which listed std modules a linkage path the builder made names"},
	"stdinst.go stdGenericTemplate":   {1, "a generic std template by a container kind's base name the builder holds (irContainerBaseName), a route chosen by the operand's kind that the call-route and impl-form ratchets count"},
	// Not a name.
	"composite.go internNamed":      {1, "interns an anonymous struct's kind by its field names, which are the type"},
	"sharedcomp.go internCompNamed": {1, "interns an anonymous struct's kind by its field names, which are the type"},
	"stdbundle.go restoreStdModule": {1, "an index into a slice"},
	// Checks that two indexes agree.
	"declindex.go defOf":      {1, "a check under NOMI_SUBST_VERIFY that the declaring gen's index by name holds the def its index by declaration does"},
	"declindex.go ifaceDefOf": {1, "a check under NOMI_SUBST_VERIFY that the declaring gen's index by name holds the def its index by declaration does"},
	// A type identity's declaration: the name in the declaring module's
	// own scope, which a (module, name) identity names.
	"declindex.go declNode":         {1, "the declaration a type's (Origin, Name) names, in the declaring file's module scope"},
	"ownerdecl.go qualifierDecl":    {1, "the declaration a std qualifier's identity (Qualifier.Std) names, in that module's scope"},
	"abstract.go stdDebugType":      {1, "std's Debug by its spec row's module and name"},
	"foreign.go resolvedTypeSymbol": {1, "resolvedScopeSymbol over a file's module scope, counted at its callers"},
}

// kindImports are the functions that translate a kind from the gen that
// declares it into a mirror in the asking gen.
var kindImports = map[string]bool{
	"importKind":          true,
	"importKinds":         true,
	"importNamed":         true,
	"importIfaceKind":     true,
	"importIfaceInstKind": true,
}

// mirrorCaches are the tables a gen interns its mirrors of other files'
// declarations in.
var mirrorCaches = map[string]bool{
	"foreignDefs":     true,
	"foreignIfaces":   true,
	"foreignInstDefs": true,
}

// lookupTables are the name-keyed tables beside the four declaration
// tables: generic templates and inherent blocks by owner, and the names of
// the tables a builder may not keep by a name it reads (block imports, a
// body's type scope).
var lookupTables = map[string]bool{
	"genericTemplates": true,
	"ownerImpls":       true,
	"blockImports":     true,
	"defs":             true,
	"imported":         true,
	"importedIfaces":   true,
	"templates":        true,
	"aliases":          true,
}

// TestNameLookupsOnlyGoDown counts the builder's lookups of a declaration
// by a spelled name and fails when a count rises, or falls without its
// maximum following it.
func TestNameLookupsOnlyGoDown(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var named, reads, lookupReads, unchecked []string
	var anchorReads, stdIndexReads, imports, mirrorWrites, ifaceLookups, scopeLookups []string
	exempt := map[string]int{}
	tables := map[string]bool{"funcs": true, "types": true, "ifaces": true, "onces": true}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		fn := ""
		// counted appends a resolution site, or counts it apart when its
		// function is one notResolutions lists.
		counted := func(list *[]string, site string) {
			if _, listed := notResolutions[name+" "+fn]; listed {
				exempt[name+" "+fn]++
				return
			}
			*list = append(*list, site)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				fn = n.Name.Name
				if strings.HasSuffix(n.Name.Name, "Named") && takesString(n.Type) {
					counted(&named, name+": "+n.Name.Name)
				}
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok {
					switch {
					case sel.Sel.Name == "markUnchecked":
						unchecked = append(unchecked, fset.Position(n.Pos()).String())
					case kindImports[sel.Sel.Name]:
						imports = append(imports, fset.Position(n.Pos()).String()+" "+sel.Sel.Name)
					case ifaceNameLookups[sel.Sel.Name]:
						counted(&ifaceLookups, fset.Position(n.Pos()).String()+" "+sel.Sel.Name)
					}
				}
				if id, ok := n.Fun.(*ast.Ident); ok && scopeSymbolLookups[id.Name] {
					counted(&scopeLookups, fset.Position(n.Pos()).String()+" "+id.Name)
				}
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if ix, ok := lhs.(*ast.IndexExpr); ok {
						if sel, ok := ix.X.(*ast.SelectorExpr); ok && mirrorCaches[sel.Sel.Name] {
							mirrorWrites = append(mirrorWrites, fset.Position(ix.Pos()).String()+" "+sel.Sel.Name)
						}
					}
				}
			case *ast.IndexExpr:
				if sel, ok := n.X.(*ast.SelectorExpr); ok {
					at := fset.Position(n.Pos()).String() + " " + sel.Sel.Name
					switch {
					case tables[sel.Sel.Name]:
						counted(&reads, fset.Position(n.Pos()).String())
					case lookupTables[sel.Sel.Name]:
						counted(&lookupReads, at)
					case anchorTables[sel.Sel.Name]:
						counted(&anchorReads, at)
					case stdIndexTables[sel.Sel.Name]:
						counted(&stdIndexReads, at)
					}
				}
			}
			return true
		})
	}
	check := func(what string, got []string, max int, constant string) {
		switch {
		case len(got) > max:
			t.Errorf("%d %s, and at most %d may be: find the declaration by the one the checker resolved (the call's analysis.Callee, a reference's symbol), not by its name:\n  %s",
				len(got), what, max, strings.Join(got, "\n  "))
		case len(got) < max:
			t.Errorf("%d %s: lower %s to %d", len(got), what, constant, len(got))
		}
	}
	for key, nr := range notResolutions {
		if exempt[key] != nr.sites {
			t.Errorf("%s holds %d counted sites and notResolutions lists %d (%s): a site it gains resolves a name, and is counted, unless it is not one either",
				key, exempt[key], nr.sites, nr.why)
		}
	}
	check("functions find a declaration from a name", named, maxNamedLookups, "maxNamedLookups")
	check("reads of a name-keyed declaration table", reads, maxNameTableReads, "maxNameTableReads")
	check("reads of a name-keyed lookup table (lookupTables)", lookupReads, maxLookupTableReads, "maxLookupTableReads")
	check("sites lower code the checker never checked (markUnchecked)", unchecked, maxUncheckedSynthesizedBodies, "maxUncheckedSynthesizedBodies")
	check("reads of a std or host anchor by name (anchorTables)", anchorReads, maxAnchorTableReads, "maxAnchorTableReads")
	check("reads of the std index by spelling (stdIndexTables)", stdIndexReads, maxStdIndexReads, "maxStdIndexReads")
	check("calls that import another gen's kind (kindImports)", imports, maxKindImports, "maxKindImports")
	check("calls that find an interface by name (ifaceNameLookups)", ifaceLookups, maxIfaceNameLookupCalls, "maxIfaceNameLookupCalls")
	check("writes of a mirror cache (mirrorCaches)", mirrorWrites, maxMirrorCacheWrites, "maxMirrorCacheWrites")
	check("calls that resolve a name in a scope (scopeSymbolLookups)", scopeLookups, maxScopeSymbolLookups, "maxScopeSymbolLookups")
}

func takesString(ft *ast.FuncType) bool {
	if ft.Params == nil {
		return false
	}
	for _, p := range ft.Params.List {
		if id, ok := p.Type.(*ast.Ident); ok && id.Name == "string" {
			return true
		}
	}
	return false
}
