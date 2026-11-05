package lsp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// projectRoot is the root the document's imports resolve from.
func (r *completionRequest) projectRoot() string {
	return r.s.docs.FindProjectRoot(uriToPath(r.doc.URI))
}

// importPathCandidates offers the next segment of an import path: `std` and
// the project's top-level files and directories first, then the standard
// library's files after `std/`, or the files and directories of a project
// directory.
func (r *completionRequest) importPathCandidates() []candidate {
	stmt, seg := r.ctx.importStmt, r.ctx.importSeg
	var segs []string
	for i := 0; i < seg && i < len(stmt.ModulePath); i++ {
		segs = append(segs, ast.ImportNodeName(stmt.ModulePath[i]))
	}
	var out []candidate
	if seg == 0 {
		out = append(out, candidate{label: "std", kind: protocol.CompletionItemKindModule, detail: "the standard library", locality: 2})
	}
	if len(segs) == 1 && segs[0] == "std" {
		if r.s.std == nil {
			return nil
		}
		for name := range r.s.std.Modules {
			if name == "prelude" {
				continue
			}
			out = append(out, candidate{label: name, kind: protocol.CompletionItemKindModule, detail: "std::" + name, locality: 2})
		}
		return out
	}
	if len(segs) > 0 && segs[0] == "std" {
		return out
	}
	self := uriToPath(r.doc.URI)
	dir := filepath.Join(append([]string{r.projectRoot()}, segs...)...)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := strings.Join(append(append([]string(nil), segs...), strings.TrimSuffix(name, ".nomi")), "/")
		switch {
		case e.IsDir():
			out = append(out, candidate{label: name, kind: protocol.CompletionItemKindFolder, detail: path + "/", locality: 1})
		case strings.HasSuffix(name, ".nomi") && filepath.Join(dir, name) != self:
			out = append(out, candidate{label: strings.TrimSuffix(name, ".nomi"), kind: protocol.CompletionItemKindFile, detail: path, locality: 1})
		}
	}
	return out
}

// importNameCandidates offers the public names of the imported file that
// its selector list does not name yet, and `self` when it is not there.
func (r *completionRequest) importNameCandidates() []candidate {
	stmt := r.ctx.importStmt
	var segs []string
	for _, n := range stmt.ModulePath {
		segs = append(segs, ast.ImportNodeName(n))
	}
	listed := map[string]bool{}
	for _, n := range stmt.Names {
		if name := ast.ImportNodeName(n); !strings.Contains(name, completionSentinel) {
			listed[name] = true
		}
	}
	var out []candidate
	if !stmt.IncludeParent {
		out = append(out, candidate{label: "self", kind: protocol.CompletionItemKindKeyword, detail: "the file itself", locality: 2})
	}
	for _, c := range r.filePublicNames(segs) {
		if !listed[c.label] {
			out = append(out, c)
		}
	}
	return out
}

// filePublicNames lists the public top-level names of the file a module path
// names: from the standard library's analyzed scopes, from a project file's
// analyzed snapshot when the workspace tracks it, and otherwise from a parse
// of the file.
func (r *completionRequest) filePublicNames(segs []string) []candidate {
	if len(segs) == 0 {
		return nil
	}
	if segs[0] == "std" {
		if len(segs) == 2 && r.s.std != nil {
			return scopeMemberCandidates(r.s.std.Modules[segs[1]], func(*analysis.Symbol) bool { return true })
		}
		return nil
	}
	return r.filePublicNamesAt(filepath.Join(append([]string{r.projectRoot()}, segs...)...) + ".nomi")
}

// filePublicNamesAt is filePublicNames of the project file at path.
func (r *completionRequest) filePublicNamesAt(path string) []candidate {
	if snap := r.s.docs.Snapshot(fileuri.FromPath(path)); snap != nil && snap.Analysis != nil {
		return scopeMemberCandidates(snap.Analysis.ModuleScope, func(*analysis.Symbol) bool { return true })
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
	var out []candidate
	for _, d := range publicDecls(nodes, r.reads(false, path)) {
		out = append(out, candidate{label: d.name, kind: d.kind, locality: 2})
	}
	return out
}

type publicDecl struct {
	name string
	kind protocol.CompletionItemKind
}

// publicDecls lists a file's public top-level declarations by name, and its
// private ones too when private is set: the requesting document is the
// file's partner test (analysis.IsPartnerTestPath).
func publicDecls(nodes []ast.Node, private bool) []publicDecl {
	var out []publicDecl
	add := func(public bool, name string, kind protocol.CompletionItemKind) {
		if (public || private) && name != "" {
			out = append(out, publicDecl{name, kind})
		}
	}
	for _, n := range nodes {
		switch d := n.(type) {
		case *ast.FuncDef:
			add(d.Public, d.Name, protocol.CompletionItemKindFunction)
		case *ast.ExternFunc:
			add(d.Public, d.Name, protocol.CompletionItemKindFunction)
		case *ast.StructDef:
			add(d.Public, d.Name, protocol.CompletionItemKindStruct)
		case *ast.EnumDef:
			add(d.Public, d.Name, protocol.CompletionItemKindEnum)
		case *ast.TypeDef:
			add(d.Public, d.Name, protocol.CompletionItemKindClass)
		case *ast.ExternType:
			add(d.Public, d.Name, protocol.CompletionItemKindClass)
		case *ast.TypeAlias:
			add(d.Public, d.Name, protocol.CompletionItemKindClass)
		case *ast.InterfaceDef:
			add(d.Public, d.Name, protocol.CompletionItemKindInterface)
		case *ast.OnceBinding:
			add(d.Public, d.Name, protocol.CompletionItemKindConstant)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// pathSegmentCandidates offers the next segment of a path at a use: the
// files and the directories under the directory the path's first segment,
// a directory import's name, binds, below the segments written after it
// (`admin::` offers views/admin's files, `admin::reports::` those of
// views/admin/reports).
func (r *completionRequest) pathSegmentCandidates() []candidate {
	segs := r.ctx.pathSegs
	head := r.scope.Lookup(segs[0])
	if head == nil || head.Kind != analysis.SymbolDirectory || head.Directory == nil {
		return nil
	}
	listing := head.Directory.Listing
	if len(segs) > 1 {
		listing = nil
		if dir := head.Directory.Listing; dir != nil && dir.Dir != "" {
			listing = analysis.DiskDirectory(dir.Dir, segs[1:])
		}
	}
	if listing == nil {
		return nil
	}
	spelled := strings.Join(segs, ast.PathSeparator) + ast.PathSeparator
	self := uriToPath(r.doc.URI)
	var out []candidate
	for _, f := range listing.Files {
		if listing.Dir != "" && filepath.Join(listing.Dir, f+".nomi") == self {
			continue
		}
		out = append(out, candidate{label: f, kind: protocol.CompletionItemKindFile, detail: spelled + f, locality: 1})
	}
	for _, d := range listing.Dirs {
		out = append(out, candidate{label: d, kind: protocol.CompletionItemKindFolder, detail: spelled + d + ast.PathSeparator, locality: 1})
	}
	return out
}

// reachedFileMembers lists the public names of the file a path at a use
// names below a directory import's name, when the analysis has not reached
// it yet: the path was typed after the document was last analyzed.
func (r *completionRequest) reachedFileMembers(fp *ast.FilePath) []candidate {
	if len(fp.Segments) < 2 {
		return nil
	}
	head := r.scope.Lookup(fp.Segments[0].Name)
	if head == nil || head.Kind != analysis.SymbolDirectory || head.Directory == nil {
		return nil
	}
	var rest []string
	for _, seg := range fp.Segments[1:] {
		rest = append(rest, seg.Name)
	}
	d := head.Directory
	if len(d.Path) == 1 && d.Path[0] == "std" {
		return r.filePublicNames(append([]string{"std"}, rest...))
	}
	if d.Listing == nil || d.Listing.Dir == "" {
		return nil
	}
	return r.filePublicNamesAt(filepath.Join(append([]string{d.Listing.Dir}, rest...)...) + ".nomi")
}
