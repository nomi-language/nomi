package stdcompiler

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// HoverMarker is the cursor mark `compiler.hover` reads a position from.
//
// It is one rune that no Nomi program contains, which is what lets a fixture say
// WHERE it means without pinning a line and a column that every later edit to
// the fixture would move.
const HoverMarker = "ˇ"

// HoverContent is `compiler.hover`: render the editor's hover content for the
// single marked position in source. internal/compilerhosts' host wraps it in
// the `Result<Hover, String>` std declares.
//
// # Why the error side is a String and not a diagnostic list
//
// `hover` answers content for a position, so every way of not having content is
// one message: no marker, two markers, the source does not parse, the source
// does not check, the position names no token, the renderer has nothing to say.
// HoverContent builds that joined message in one place.
//
// Every returned error is a Nomi-VISIBLE `Err` payload rather than a host
// failure: `hover` has no fallible-host case, so a caller turns each of these
// into the error side of the Result verbatim.
//
// virtualFiles are in-memory sibling sources keyed by import path, which only an
// in-memory project has; a real check passes nil and gets std.MakeLoader.
func HoverContent(source, root string, virtualFiles map[string]string) (rt.Hover, error) {
	clean, pos, err := StripSingleHoverMarker(source)
	if err != nil {
		return rt.Hover{}, err
	}

	nodes, parseErrs := ParseSource(clean)
	if len(parseErrs) > 0 {
		return rt.Hover{}, fmt.Errorf("%s", JoinParseErrors(parseErrs))
	}

	fa, typeErrs := hoverAnalysis(nodes, root, Loader(virtualFiles))
	if len(typeErrs) > 0 {
		return rt.Hover{}, fmt.Errorf("%s", JoinTypeErrors(typeErrs))
	}

	sym, _, _, ok := fa.TokenAt(pos)
	if !ok {
		return rt.Hover{}, fmt.Errorf("no hover target at line %d, col %d", pos.Line, pos.Col)
	}
	markdown := hoverdoc.RenderWithAnalysis(sym, fa)
	if markdown == "" {
		return rt.Hover{}, fmt.Errorf("no hover content at line %d, col %d", pos.Line, pos.Col)
	}
	return rt.Hover{Signature: NormalizeHoverMarkdown(markdown), Markdown: markdown}, nil
}

// hoverAnalysis resolves one hovered source, and it is the single place THIS
// pass order lives.
//
// It is not ProjectDiagnostics' order and must not be folded into it. `check`
// answers a diagnostic list for an ENTRY FILE of a project — siblings, manifest,
// coherence across the whole graph. `hover` answers content for a position in
// ONE file, and what it needs from analysis is a resolved token table, so it
// builds a single file against the stdlib and reports every error that would
// make the table untrustworthy. Sharing one order between them would either give
// `hover` a project it does not have or give `check` a file it is not.
//
// A non-empty error slice means the table is not trustworthy, so the caller
// reports the errors instead of a position that was resolved against a broken
// program.
func hoverAnalysis(nodes []ast.Node, root string, loader analysis.FileLoader) (*analysis.FileAnalysis, []analysis.TypeError) {
	nodes, lowerErrs := analysis.LowerDerives(nodes)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, root, loader)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)

	errs := append([]analysis.TypeError{}, lowerErrs...)
	errs = append(errs, fa.TypeErrors...)
	errs = append(errs, analysis.BuildTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	errs = append(errs, analysis.AnalyzeIterSensitivity(fa, nodes)...)
	errs = append(errs, analysis.CheckConcurrentScope(fa, nodes)...)
	errs = append(errs, analysis.CheckBootScope(fa, nodes)...)
	errs = append(errs, analysis.CheckTaskLifetime(fa, nodes)...)
	errs = append(errs, analysis.FinalizeCoherence(fa)...)
	return fa, errs
}

// StripSingleHoverMarker removes the one marker from source and answers the
// 1-based position it stood at.
//
// EXACTLY one marker: none and several are both errors, because a fixture that
// marked two positions would silently get the first and a fixture that marked
// none would silently get line 1 column 1.
//
// The column is counted in BYTES from the start of the marker's line, which is
// what analysis.Pos means everywhere else in this compiler — the lexer counts
// the same way — so a fixture with multibyte text before the marker on its line
// resolves to the token the marker is inside rather than to one nearby.
func StripSingleHoverMarker(source string) (string, analysis.Pos, error) {
	switch strings.Count(source, HoverMarker) {
	case 1:
	case 0:
		return "", analysis.Pos{}, fmt.Errorf("compiler.hover source must contain one %q marker, found none", HoverMarker)
	default:
		return "", analysis.Pos{}, fmt.Errorf("compiler.hover source must contain one %q marker, found multiple", HoverMarker)
	}
	idx := strings.Index(source, HoverMarker)
	prefix := source[:idx]
	line := strings.Count(prefix, "\n") + 1
	col := len(prefix) + 1
	if lineStart := strings.LastIndex(prefix, "\n"); lineStart >= 0 {
		col = len(prefix[lineStart+1:]) + 1
	}
	return source[:idx] + source[idx+len(HoverMarker):], analysis.Pos{Line: line, Col: col}, nil
}

// JoinParseErrors is the parse-error half of hover's failure message: one
// `line L, col C: message` per error, newline-separated.
func JoinParseErrors(errs []parser.ParseError) string {
	msgs := make([]string, len(errs))
	for i, err := range errs {
		msgs[i] = fmt.Sprintf("line %d, col %d: %s", err.Line, err.Col, err.Message)
	}
	return strings.Join(msgs, "\n")
}

// JoinTypeErrors is the analysis half, using each error's own rendering.
func JoinTypeErrors(errs []analysis.TypeError) string {
	msgs := make([]string, len(errs))
	for i, err := range errs {
		msgs[i] = err.Error()
	}
	return strings.Join(msgs, "\n")
}

// NormalizeHoverMarkdown reduces a rendered hover body to its first signature
// line as plain text.
//
// The renderer's answer is a fenced code block followed by documentation, so:
// keep the part before the first blank line, drop the fences and the empty
// lines, take the first line that is left, and collapse its internal whitespace
// to single spaces. That last step is what makes `signature ==
// "answer: Int"` a stable assertion — the renderer is free to align or indent
// inside the fence.
//
// An empty answer means the body had no signature line at all, which a caller
// reports as having no content rather than as an empty signature.
func NormalizeHoverMarkdown(hover string) string {
	if idx := strings.Index(hover, "\n\n"); idx >= 0 {
		hover = hover[:idx]
	}
	var sigLines []string
	for _, line := range strings.Split(hover, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "```") {
			continue
		}
		sigLines = append(sigLines, trimmed)
	}
	if len(sigLines) == 0 {
		return ""
	}
	return strings.Join(strings.Fields(sigLines[0]), " ")
}
