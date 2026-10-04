package lsp

import (
	"os"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// textDocumentDocumentLink links each import's path (`std/iter`,
// `models/users`, the `game` of `game.Game`) to the file it names: a
// stdlib module to the materialized stdlib file go-to-definition opens, a
// project file to itself when it exists. A Go import or a path to a
// missing file gets no link.
func (s *Server) textDocumentDocumentLink(_ *glsp.Context, params *protocol.DocumentLinkParams) ([]protocol.DocumentLink, error) {
	uri := string(params.TextDocument.URI)
	doc := s.docs.Snapshot(uri)
	if doc == nil {
		return nil, nil
	}
	links := s.documentLinks(uri, doc.Nodes)
	lines := s.lines.get(uri, doc.Content)
	for i := range links {
		links[i].Range = lines.utf16Range(links[i].Range)
	}
	return links, nil
}

// documentLinks returns the byte-column links of a file's imports.
func (s *Server) documentLinks(uri string, nodes []ast.Node) []protocol.DocumentLink {
	imports := importEntries(nodes)
	if len(imports) == 0 {
		return nil
	}
	path := uriToPath(uri)
	root := s.docs.FindProjectRoot(path)
	moduleName := ""
	if m, err := analysis.LoadManifest(root); err == nil && m != nil {
		moduleName = m.Name
	}
	var out []protocol.DocumentLink
	for _, imp := range imports {
		target, n := s.importLinkTarget(imp, root, moduleName)
		if target == "" {
			continue
		}
		start, end := importPathRange(imp, n)
		t := protocol.DocumentUri(target)
		out = append(out, protocol.DocumentLink{Range: protocol.Range{Start: start, End: end}, Target: &t})
	}
	return out
}

// importLinkTarget is the URI of the file an import names and the number
// of path segments that name it, or "".
func (s *Server) importLinkTarget(imp *ast.ImportStmt, root, moduleName string) (string, int) {
	if imp.Extern || len(imp.ModulePath) == 0 {
		return "", 0
	}
	if ast.ImportNodeName(imp.ModulePath[0]) == "std" {
		if s.std == nil {
			return "", 0
		}
		// The module is the last segment that names one: in
		// `std/maybe.Maybe.{None}` the path is std/maybe.
		for i := len(imp.ModulePath) - 1; i >= 1; i-- {
			if name := ast.ImportNodeName(imp.ModulePath[i]); name != "" {
				if _, ok := s.std.Modules[name]; ok {
					return s.std.FileURI(name), i + 1
				}
			}
		}
		return "", 0
	}
	file, _, n, ok := importTarget(imp, root, moduleName)
	if !ok {
		return "", 0
	}
	if info, err := os.Stat(file); err != nil || info.IsDir() {
		return "", 0
	}
	return pathToURI(file), n
}
