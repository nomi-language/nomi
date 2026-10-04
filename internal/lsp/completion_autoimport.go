package lsp

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// importSource is one file a name can be imported from: a standard-library
// file or a project file, with its module path and its members: the member
// scope of an analyzed file, or the index entry's declarations of a closed
// project file.
type importSource struct {
	path  string // `std/sets`, `server`, `lib/text`
	name  string // the file object's name: the path's last segment
	scope *analysis.Scope
	decls []analysis.IndexedDecl
}

// importSources lists the standard library's files and the project's
// open and indexed files other than the document itself, by module path.
func (r *completionRequest) importSources() []importSource {
	var out []importSource
	if r.s.std != nil {
		for name, scope := range r.s.std.Modules {
			if name != "prelude" {
				out = append(out, importSource{path: "std/" + name, name: name, scope: scope})
			}
		}
	}
	root := r.projectRoot()
	modulePath := func(uri string) (string, bool) {
		rel, err := filepath.Rel(root, uriToPath(uri))
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", false
		}
		return filepath.ToSlash(strings.TrimSuffix(rel, ".nomi")), true
	}
	for _, uri := range r.s.docs.OpenURIs() {
		if uri == r.doc.URI {
			continue
		}
		snap := r.s.docs.Snapshot(uri)
		path, ok := modulePath(uri)
		if snap == nil || snap.Analysis == nil || !ok {
			continue
		}
		out = append(out, importSource{path: path, name: filepath.Base(path), scope: snap.Analysis.ModuleScope})
	}
	for _, entry := range r.s.docs.Indexed(root) {
		if entry.URI == r.doc.URI {
			continue
		}
		if path, ok := modulePath(entry.URI); ok {
			out = append(out, importSource{path: path, name: filepath.Base(path), decls: entry.Decls})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// autoImportCandidates offers names the document can reach by adding an
// import: a file object (`io`, `server`), a public type (`Set` from
// `std/sets`), and a public function spelled through its file object
// (`io.print`), which is how Nomi calls a file's functions. Accepting one
// adds the import, merged into an existing import of the same file when
// there is one. A name already visible at the cursor is never offered with
// an import, so no prelude name is ever imported again. A function of a file
// the document already imports is offered too, with no edit.
//
// The sources are offered only once something is typed, and the list is
// then marked incomplete so the client asks again as the word grows.
func (r *completionRequest) autoImportCandidates(types, values bool) []candidate {
	r.incomplete = true
	if r.ctx.prefix == "" {
		return nil
	}
	var out []candidate
	for _, src := range r.importSources() {
		bound := realSymbol(r.scope.Lookup(src.name))
		imported := bound != nil && bound.Kind == analysis.SymbolModule &&
			(bound.ModuleScope == src.scope || importedPath(bound) == src.path)
		if bound == nil && values {
			out = append(out, candidate{
				label:      src.name,
				kind:       protocol.CompletionItemKindModule,
				detail:     "import " + src.path,
				locality:   3,
				importFrom: &analysis.MissingImport{ModulePath: src.path},
			})
		}
		out = append(out, r.indexedImportCandidates(src, types, values, bound, imported)...)
		if src.scope == nil {
			continue
		}
		for name, sym := range src.scope.Symbols {
			if !sym.Public || sym.Resolved != nil {
				continue
			}
			switch {
			case types && typeSymbol(sym):
				if r.scope.Lookup(name) != nil {
					continue
				}
				out = append(out, candidate{
					label:      name,
					kind:       symbolKindToCompletionKind(sym.Kind),
					sym:        sym,
					locality:   3,
					importFrom: &analysis.MissingImport{ModulePath: src.path, Member: name},
				})
			case values && sym.Kind == analysis.SymbolFunction && valueSymbol(sym):
				if bound != nil && !imported {
					continue // the file's name means something else here
				}
				c := candidate{
					label:    src.name + "." + name,
					kind:     protocol.CompletionItemKindFunction,
					sym:      sym,
					filter:   name,
					locality: 2,
				}
				if !imported {
					c.locality = 3
					c.importFrom = &analysis.MissingImport{ModulePath: src.path}
				}
				out = append(out, c)
			}
		}
	}
	return out
}

// indexedImportCandidates offers a closed project file's public types and
// functions from its index entry, as autoImportCandidates offers an analyzed
// file's from its scope. A function carries its signature as detail and its
// required parameters for the call snippet.
func (r *completionRequest) indexedImportCandidates(src importSource, types, values bool, bound *analysis.Symbol, imported bool) []candidate {
	var out []candidate
	for _, d := range src.decls {
		if !d.Public {
			continue
		}
		switch {
		case types && declIsType(d.Kind):
			if r.scope.Lookup(d.Name) != nil {
				continue
			}
			out = append(out, candidate{
				label:      d.Name,
				kind:       symbolKindToCompletionKind(d.Kind),
				doc:        d.Doc,
				locality:   3,
				importFrom: &analysis.MissingImport{ModulePath: src.path, Member: d.Name},
			})
		case values && d.Kind == analysis.SymbolFunction && !strings.HasPrefix(d.Name, "__"):
			if bound != nil && !imported {
				continue // the file's name means something else here
			}
			c := candidate{
				label:      src.name + "." + d.Name,
				kind:       protocol.CompletionItemKindFunction,
				detail:     hoverdoc.RenderFuncSig(d.Name, d.TypeParams, d.Params, d.Return),
				doc:        d.Doc,
				filter:     d.Name,
				locality:   2,
				callParams: requiredParamNames(d.Params),
				callable:   true,
			}
			if !imported {
				c.locality = 3
				c.importFrom = &analysis.MissingImport{ModulePath: src.path}
			}
			out = append(out, c)
		}
	}
	return out
}

func declIsType(k analysis.SymbolKind) bool {
	switch k {
	case analysis.SymbolStruct, analysis.SymbolEnum, analysis.SymbolType,
		analysis.SymbolTypeAlias, analysis.SymbolInterface:
		return true
	}
	return false
}

// importedPath is the module path a file object's import statement names.
func importedPath(sym *analysis.Symbol) string {
	stmt, ok := sym.Node.(*ast.ImportStmt)
	if !ok {
		return ""
	}
	segs := make([]string, len(stmt.ModulePath))
	for i, n := range stmt.ModulePath {
		segs[i] = ast.ImportNodeName(n)
	}
	return strings.Join(segs, "/")
}

// textNodes is the top-level tree of the latest text: the snapshot's own
// when its analysis is of that text, else a parse of it, so an edit's
// ranges are computed against the text the client holds.
func (r *completionRequest) textNodes() []ast.Node {
	if r.positions.identity {
		return r.doc.Nodes
	}
	if !r.reparsed {
		r.parsed, _, _ = parser.ParseResilient(lexer.Lex(r.doc.Content))
		r.reparsed = true
	}
	return r.parsed
}

// importEdit is the edit that adds c's import, rendered as organize-imports
// renders imports, in UTF-16 columns.
func (r *completionRequest) importEdit(m analysis.MissingImport) (protocol.TextEdit, bool) {
	edit, ok := addImportEdit(r.doc.Content, r.lines.starts, r.textNodes(), m)
	if !ok {
		return protocol.TextEdit{}, false
	}
	edit.Range = r.lines.utf16Range(edit.Range)
	return edit, true
}
