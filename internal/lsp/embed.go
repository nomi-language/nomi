package lsp

import (
	"fmt"
	"path/filepath"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/hoverdoc"
)

// An `embed` declaration's patterns, in the editor: a hover on one says
// what it matched, and go to definition opens it, the file or the folder
// it names (several locations for a glob). What each matched comes from
// the analysis (analysis.EmbedOf), which found the files without reading
// them.

// embedPatternAt is the `embed` whose pattern is at pos, and the pattern's
// index, or false.
func embedPatternAt(nodes []ast.Node, pos analysis.Pos) (*analysis.EmbedInfo, int, bool) {
	for _, n := range nodes {
		o, ok := n.(*ast.OnceBinding)
		if !ok {
			continue
		}
		info := analysis.EmbedOf(o)
		if info == nil {
			continue
		}
		for i, p := range info.Decl.Patterns {
			if p.Line == pos.Line && pos.Col >= p.Col && pos.Col < p.Col+p.Width {
				return info, i, true
			}
		}
	}
	return nil, 0, false
}

// embedPatternHover is the hover over pattern i of info: the files and
// folders it matched, or that it matched nothing.
func embedPatternHover(content string, info *analysis.EmbedInfo, i int) *protocol.Hover {
	p := info.Decl.Patterns[i]
	var b strings.Builder
	matches := info.Matches[i]
	if len(matches) == 0 {
		fmt.Fprintf(&b, "`%s` matches no file", p.Text)
	} else {
		var files []analysis.EmbedFileInfo
		for _, f := range info.Files {
			for _, m := range matches {
				if f.Path == m || strings.HasPrefix(f.Path, m+"/") {
					files = append(files, f)
					break
				}
			}
		}
		sub := &analysis.EmbedInfo{Decl: info.Decl, Files: files}
		fmt.Fprintf(&b, "`%s` embeds %s", p.Text, hoverdoc.EmbedSummary(sub))
	}
	return tokenHover(content, b.String(), analysis.Pos{Line: p.Line, Col: p.Col}, p.Width)
}

// embedPatternLocations are the files and folders pattern i of info
// matched, as locations to open; none for an in-memory program.
func embedPatternLocations(info *analysis.EmbedInfo, i int) []protocol.Location {
	if !filepath.IsAbs(info.Root) {
		return nil
	}
	var out []protocol.Location
	for _, m := range info.Matches[i] {
		out = append(out, protocol.Location{
			URI:   protocol.DocumentUri(fileuri.FromPath(filepath.Join(info.Root, filepath.FromSlash(m)))),
			Range: protocol.Range{},
		})
	}
	return out
}
