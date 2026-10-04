package lsp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
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
			out = append(out, candidate{label: name, kind: protocol.CompletionItemKindModule, detail: "std/" + name, locality: 2})
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
	path := filepath.Join(append([]string{r.projectRoot()}, segs...)...) + ".nomi"
	if snap := r.s.docs.Snapshot("file://" + path); snap != nil && snap.Analysis != nil {
		return scopeMemberCandidates(snap.Analysis.ModuleScope, func(*analysis.Symbol) bool { return true })
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
	var out []candidate
	for _, d := range publicDecls(nodes) {
		out = append(out, candidate{label: d.name, kind: d.kind, locality: 2})
	}
	return out
}

type publicDecl struct {
	name string
	kind protocol.CompletionItemKind
}

// publicDecls lists a file's public top-level declarations by name.
func publicDecls(nodes []ast.Node) []publicDecl {
	var out []publicDecl
	add := func(public bool, name string, kind protocol.CompletionItemKind) {
		if public && name != "" {
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
