// Signature regression tests for dedicated LSP fixtures.
//
// Inline `// :: ...` annotations in source files lock the LSP-rendered
// hover for specific identifiers. The annotation may sit either to the
// right of the targeted line (one-liner decls) or on a comment-only line
// directly above (multi-line decls that the formatter would otherwise
// reflow off the annotation). A universal coverage pass also asserts
// every named symbol in the file produces a non-trivial hover, catching
// the "missing signature" class of bugs without requiring annotations
// everywhere.
package lsp

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// signatureCoverageFiles lists the LSP fixture files under signature
// regression coverage. Add a file to this list once its annotations are
// settled and it runs clean.
var signatureCoverageFiles = []string{
	"testdata/signatures_core.nomi",
}

func TestSignatures(t *testing.T) {
	for _, path := range signatureCoverageFiles {
		t.Run(path, func(t *testing.T) {
			checkSignatureFile(t, path)
		})
	}
}

func checkSignatureFile(t *testing.T, path string) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	tokens := lexer.Lex(string(src))
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = analysis.LowerDerives(nodes)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	// AttachStdlibProjectImpls — post-stdlib-globals-retirement, the
	// single-file path needs an explicit stdlib ProjectImpls so the
	// unifier sees Int/String/Map/etc.'s stdlib impls; without it
	// generic inference through stdlib types breaks (e.g. a `(k, v)`
	// destructure on `Iter.reduce(..., Map.empty(), ...)` where K and
	// V are pinned by `Map.iter`'s `Iter<(K, V)>` impl).
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	_ = analysis.BuildTypes(fa, nodes)
	_ = analysis.CheckTypes(fa, nodes)

	annotations := scanSignatureAnnotations(string(src))
	for _, ann := range annotations {
		validateSignatureAnnotation(t, path, fa, ann)
	}
	runSignatureCoveragePass(t, path, fa)
}

// --- annotation scanner ----------------------------------------------------

// signatureAnnotation is one `// :: ...` directive in source.
type signatureAnnotation struct {
	line int    // 1-based source line
	name string // "" for whole-line form, otherwise the targeted identifier name
	want string // expected normalized hover text
}

// annotationRe matches `// ::` annotations through end of line OR until
// the next `//` (so a line can carry both `// :: ...` and `// expect: ...`).
// The captured payload is parsed for an optional `<name> => ` named-target
// prefix; otherwise the whole payload is the expected hover text.
//
// Examples:
//
//	once x = 1                                   // :: once x: Int = 1
//	io.inspect(col.size(m)) // :: size => fn size(m: Map<K, V>): Int // expect: 1
var annotationRe = regexp.MustCompile(`//\s*::\s*(.+?)\s*(?://.*)?$`)

// namedTargetRe parses an `<ident> =>` prefix off an annotation payload.
var namedTargetRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=>\s*(.+)$`)

func scanSignatureAnnotations(src string) []signatureAnnotation {
	var out []signatureAnnotation
	for i, line := range strings.Split(src, "\n") {
		m := annotationRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		payload := strings.TrimSpace(m[1])
		ann := signatureAnnotation{line: i + 1}
		if nm := namedTargetRe.FindStringSubmatch(payload); nm != nil {
			ann.name = nm[1]
			ann.want = strings.TrimSpace(nm[2])
		} else {
			ann.want = payload
		}
		out = append(out, ann)
	}
	return out
}

// --- target resolution + comparison ---------------------------------------

func validateSignatureAnnotation(t *testing.T, path string, fa *analysis.FileAnalysis, ann signatureAnnotation) {
	t.Helper()
	sym, found := resolveAnnotationTarget(fa, ann)
	if !found {
		if ann.name != "" {
			t.Errorf("%s:%d: annotation `// :: %s: %s` — target `%s` not found on this line",
				path, ann.line, ann.name, ann.want, ann.name)
		} else {
			t.Errorf("%s:%d: annotation `// :: %s` — no symbol found on this line",
				path, ann.line, ann.want)
		}
		return
	}
	got := normalizeHover(renderHover(sym))
	if got != ann.want {
		label := fmt.Sprintf("// :: %s", ann.want)
		if ann.name != "" {
			label = fmt.Sprintf("// :: %s: %s", ann.name, ann.want)
		}
		t.Errorf("%s:%d: annotation `%s`\n  target:   %s (at L%d:'C%d)\n  expected: %s\n  got:      %s",
			path, ann.line, label, sym.Name, sym.Pos.Line, sym.Pos.Col, ann.want, got)
	}
}

// resolveAnnotationTarget finds the Symbol the annotation points at. The
// annotation may sit either to the right of the declaration (same line) or
// on a comment-only line above it; this lets `// ::` survive the formatter
// reflowing multi-line decls.
//
//	whole-line: leftmost Symbol in fa.Definitions on the search line.
//	named:      leftmost Symbol in fa.Definitions or fa.References on
//	            the search line whose Name matches.
//
// We try the annotation's own line first, then walk forward up to 3 lines
// to find the next line carrying any symbol. Skipping blank/comment-only
// lines is implicit — a line with no symbols simply moves us forward.
func resolveAnnotationTarget(fa *analysis.FileAnalysis, ann signatureAnnotation) (*analysis.Symbol, bool) {
	for offset := 0; offset <= 3; offset++ {
		if sym, ok := lookupAnnotationOnLine(fa, ann, ann.line+offset); ok {
			return sym, true
		}
	}
	return nil, false
}

func lookupAnnotationOnLine(fa *analysis.FileAnalysis, ann signatureAnnotation, line int) (*analysis.Symbol, bool) {
	type cand struct {
		col int
		sym *analysis.Symbol
	}
	var cands []cand

	if ann.name == "" {
		for pos, sym := range fa.Definitions {
			if pos.Line == line {
				cands = append(cands, cand{pos.Col, sym})
			}
		}
	} else {
		for pos, sym := range fa.Definitions {
			if pos.Line == line && sym.Name == ann.name {
				cands = append(cands, cand{pos.Col, sym})
			}
		}
		for pos, sym := range fa.References {
			if pos.Line == line && sym.Name == ann.name {
				cands = append(cands, cand{pos.Col, sym})
			}
		}
	}
	if len(cands) == 0 {
		return nil, false
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].col < cands[j].col })
	return cands[0].sym, true
}

// normalizeHover strips markdown fences, collapses whitespace, drops
// everything after the first blank line (the doc-comment section),
// and keeps only the first signature line for multi-line hovers.
func normalizeHover(hover string) string {
	// Drop doc comments (everything after the first blank line).
	if idx := strings.Index(hover, "\n\n"); idx >= 0 {
		hover = hover[:idx]
	}
	// Strip fence lines.
	var sigLines []string
	for _, ln := range strings.Split(hover, "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" || strings.HasPrefix(trimmed, "```") {
			continue
		}
		sigLines = append(sigLines, trimmed)
	}
	if len(sigLines) == 0 {
		return ""
	}
	first := sigLines[0]
	// Collapse internal whitespace runs.
	first = strings.Join(strings.Fields(first), " ")
	return first
}

// --- universal coverage pass ----------------------------------------------

func runSignatureCoveragePass(t *testing.T, path string, fa *analysis.FileAnalysis) {
	t.Helper()
	type entry struct {
		pos analysis.Pos
		sym *analysis.Symbol
	}
	var entries []entry
	for pos, sym := range fa.Definitions {
		entries = append(entries, entry{pos, sym})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].pos.Line != entries[j].pos.Line {
			return entries[i].pos.Line < entries[j].pos.Line
		}
		return entries[i].pos.Col < entries[j].pos.Col
	})
	for _, e := range entries {
		// Skip cross-file imported symbols (they have a SourceFile pointing
		// elsewhere). Only validate symbols defined in the file under test.
		if e.sym.SourceFile != "" {
			continue
		}
		// Skip symbols introduced by @derive synthesis (their positions
		// fall in the synthLineBase band). The user never sees these in
		// source — they're emit-only AST nodes for impl bodies.
		if analysis.IsSynthesizedLine(e.pos.Line) {
			continue
		}
		if e.sym.Kind == analysis.SymbolBinding {
			continue
		}
		got := normalizeHover(renderHover(e.sym))
		if got == "" || got == e.sym.Name {
			t.Errorf("%s:%d: %s (%s) — hover renders %q (expected non-trivial signature)",
				path, e.pos.Line, e.sym.Name, symbolKindString(e.sym.Kind), got)
		}
	}
}

func symbolKindString(k analysis.SymbolKind) string {
	switch k {
	case analysis.SymbolFunction:
		return "fn"
	case analysis.SymbolStruct:
		return "struct"
	case analysis.SymbolEnum:
		return "enum"
	case analysis.SymbolEnumVariant:
		return "variant"
	case analysis.SymbolType:
		return "type"
	case analysis.SymbolTypeAlias:
		return "typealias"
	case analysis.SymbolInterface:
		return "interface"
	case analysis.SymbolParam:
		return "param"
	case analysis.SymbolBinding:
		return "binding"
	case analysis.SymbolField:
		return "field"
	case analysis.SymbolModule:
		return "module"
	case analysis.SymbolOnce:
		return "once"
	case analysis.SymbolInterfaceMethod:
		return "iface_method"
	}
	return "unknown"
}
