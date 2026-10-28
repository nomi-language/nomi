// Package highlight provides the analyzer-derived semantic tokens used to
// highlight a Nomi snippet (via internal/semtokens, the same classifier the LSP
// uses). It is exposed to the browser as nomiSemTokens (cmd/nomi-wasm); the
// shared JS highlighter (tour/src/lib/highlight.mjs) overlays these
// tokens onto the tree-sitter grammar's syntactic captures to render HTML, the
// same two-layer model Zed uses. Both the static tour and the live playground
// go through that one renderer, so there is no separate highlight definition.
package highlight

import (
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/semtokens"
	"github.com/nomi-language/nomi/internal/textpos"
	"github.com/nomi-language/nomi/internal/virtualproject"
	"github.com/nomi-language/nomi/std"
)

// Analyze runs the PROJECT analysis pipeline (parse + derive/Debug synthesis +
// BuildProjectWithCache + CheckTypes, with stdlib) on a snippet and returns the
// entry FileAnalysis — the shared input for semantic tokens AND hover
// (internal/hoverdoc). Returns nil if lib is nil. May panic on malformed input;
// callers recover (see SemanticTokens and cmd/nomi-wasm).
//
// Project analysis establishes the boot contract for scoped field checking.
// The resulting references feed both semantic tokens and hover rendering.
//
// BuildProjectWithCache rather than BuildProjectFromEntry because a tour
// snippet HAS NO PATH. The entry-path variant exists to populate
// FileAnalysis.FilePath for the internal/-access and entry-placement checks,
// which derive a module-relative path from it; inventing a path to satisfy
// them would make those checks judge a file that does not exist. This is the
// same reason document.go's header gives for the LSP per-document path.
//
// projectRoot "" and a nil loader because there is no filesystem: the tour runs
// in wasm with one buffer and no siblings. DiscoverProject returns an empty
// project for that pair (analysis/discovery.go:118), so the build walks no
// imports and touches no disk, and the entry is the whole project.
//
// No AttachStdlibProjectImpls: it is the single-file path's stdlib-only
// substitute for a project build's impl index, and its own doc says callers that went through
// BuildProject have the richer index already. No explicit BuildTypes either —
// Sweep C runs inside the project build.
func Analyze(code string, lib *std.StdLib) *analysis.FileAnalysis {
	fa, _ := AnalyzeNodes(code, lib)
	return fa
}

// AnalyzeNodes is Analyze, also answering the syntax trees the analysis was
// built from.
func AnalyzeNodes(code string, lib *std.StdLib) (*analysis.FileAnalysis, []ast.Node) {
	if lib == nil {
		return nil, nil
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(code))
	nodes, _ = analysis.LowerDerives(nodes)
	nodes, _ = analysis.SynthesizeDerives(nodes)
	nodes = analysis.SynthesizeUniversalDebug(nodes)
	fa, _, _ := analysis.BuildProjectWithCache(nodes, lib.Primitives, lib.Modules, lib.Files, "", nil)
	if fa == nil {
		return nil, nil
	}
	_ = analysis.CheckTypes(fa, nodes)
	return fa, nodes
}

// SemanticTokens analyzes a snippet and returns its analyzer-derived semantic
// tokens (the overlay layer), with columns and lengths in UTF-16 code units:
// the browser indexes the snippet as a JavaScript string. Best-effort: a
// snippet that fails to parse/analyze (e.g. a fragment, or lib == nil)
// yields none.
func SemanticTokens(code string, lib *std.StdLib) (toks []semtokens.Token) {
	defer func() {
		if recover() != nil {
			toks = nil
		}
	}()
	fa := Analyze(code, lib)
	if fa == nil {
		return nil
	}
	return semtokens.UTF16(code, semtokens.Collect(fa))
}

// ByteCol converts a 1-based column the browser counts in UTF-16 code units
// on line (1-based) of code into the 1-based byte column the analyzer
// counts. A line out of range keeps its column.
func ByteCol(code string, line, col int) int {
	lines := strings.Split(code, "\n")
	if line < 1 || line > len(lines) || col < 1 {
		return col
	}
	return textpos.ByteCol(lines[line-1], col-1) + 1
}

// Program is a multi-file snippet analyzed as one program: the tour's
// tabbed examples, whose tabs are `// FILE: <name>` sections of one source
// (internal/virtualproject). Every Nomi file is analyzed as part of the
// whole program, the manifest and message catalogs included, as the run
// builds it, so a name one tab imports from another resolves.
type Program struct {
	files map[string]ProgramFile
}

// ProgramFile is one Nomi file of a Program: its text as the tab holds it,
// its analysis and the syntax trees the analysis was built from.
type ProgramFile struct {
	Source string
	FA     *analysis.FileAnalysis
	Nodes  []ast.Node
}

// File answers the file a tab names (`cart.nomi`), or false when the
// program has no analysis of it.
func (p *Program) File(name string) (ProgramFile, bool) {
	if p == nil {
		return ProgramFile{}, false
	}
	f, ok := p.files[virtualproject.NormalizeModuleName(name)]
	return f, ok && f.FA != nil
}

// AnalyzeProgram analyzes a source carrying `// FILE:` markers as the
// in-memory program the run loads (cmd/nomi-wasm's nomiRun): the entry, its
// virtual siblings, the staged nomi.toml and catalogs. A program with errors still answers its files' analyses.
// A file the program did not reach or could not build (the entry does not
// parse) is analyzed alone, as a single-file snippet is, so a tab still
// answers something while its text is broken. It answers nil only when lib
// is nil.
//
// A source that names modules is written under dir and analyzed from
// there, as the run loads it (vmhost.LoadMarked); see analyzeFiles.
func AnalyzeProgram(marked, dir string, lib *std.StdLib) *Program {
	if lib == nil {
		return nil
	}
	prog := &Program{files: map[string]ProgramFile{}}
	project, err := virtualproject.FromMarkedSource(marked)
	if err != nil {
		return prog
	}
	if project.Files != nil {
		analyzeFiles(prog, project, dir, lib)
		return prog
	}
	sources := map[string]string{project.EntryName: project.EntrySource}
	for name, text := range project.VirtualFiles {
		if !strings.HasSuffix(name, ".toml") {
			sources[name] = text
		}
	}
	if checked := analyzeProject(project); checked != nil {
		prog.files[project.EntryName] = ProgramFile{Source: project.EntrySource, FA: checked.FA, Nodes: checked.Nodes}
		for _, f := range checked.Files {
			if src, ok := sources[f.Key]; ok {
				prog.files[f.Key] = ProgramFile{Source: src, FA: f.FA, Nodes: f.Nodes}
			}
		}
	}
	for name, src := range sources {
		if _, ok := prog.files[name]; ok {
			continue
		}
		fa, nodes := analyzeAlone(src, lib)
		prog.files[name] = ProgramFile{Source: src, FA: fa, Nodes: nodes}
	}
	return prog
}

// analyzeProject runs the front end over project, answering every file's
// analysis whether or not the program has errors, or nil when the entry
// did not parse or the front end panicked.
func analyzeProject(project virtualproject.Project) (p *frontend.Project) {
	defer func() {
		if recover() != nil {
			p = nil
		}
	}()
	c := frontend.New(frontend.Config{
		VirtualFiles:    project.VirtualFiles,
		VirtualManifest: project.Manifest,
	})
	p, _ = c.AnalyzeSource(project.EntryName, project.EntrySource, frontend.Mode{Tests: true})
	return p
}

// analyzeFiles fills prog from a project that names modules, written
// under dir: the entry's program, then each Nomi file it did not reach
// (a file of another module) as the entry of its own
// module's program, and a file neither answers alone. Each is keyed by its
// path from the root, as its tab names it.
func analyzeFiles(prog *Program, project virtualproject.Project, dir string, lib *std.StdLib) {
	root, err := project.Write(dir)
	if err == nil {
		analyzeFile := func(name string) (p *frontend.Project) {
			defer func() {
				if recover() != nil {
					p = nil
				}
			}()
			c := frontend.New(frontend.Config{})
			p, _ = c.AnalyzeFileSource(filepath.Join(root, filepath.FromSlash(name)), project.Files[name], frontend.Mode{Tests: true})
			return p
		}
		keep := func(p *frontend.Project, name string) {
			prog.files[virtualproject.NormalizeModuleName(name)] = ProgramFile{Source: project.Files[name], FA: p.FA, Nodes: p.Nodes}
			for _, f := range p.Files {
				rel, err := filepath.Rel(root, f.Path)
				if f.Path == "" || err != nil {
					continue
				}
				rel = filepath.ToSlash(rel)
				key := virtualproject.NormalizeModuleName(rel)
				if _, done := prog.files[key]; done {
					continue
				}
				if src, ok := project.Files[rel]; ok && f.FA != nil {
					prog.files[key] = ProgramFile{Source: src, FA: f.FA, Nodes: f.Nodes}
				}
			}
		}
		if p := analyzeFile(project.EntryName); p != nil {
			keep(p, project.EntryName)
		}
		for name := range project.Files {
			if _, done := prog.files[virtualproject.NormalizeModuleName(name)]; done || !strings.HasSuffix(name, ".nomi") {
				continue
			}
			if p := analyzeFile(name); p != nil {
				keep(p, name)
			}
		}
	}
	for name, src := range project.Files {
		key := virtualproject.NormalizeModuleName(name)
		if _, done := prog.files[key]; done || !strings.HasSuffix(name, ".nomi") {
			continue
		}
		fa, nodes := analyzeAlone(src, lib)
		prog.files[key] = ProgramFile{Source: src, FA: fa, Nodes: nodes}
	}
}

// analyzeAlone is AnalyzeNodes, answering no analysis when it panics.
func analyzeAlone(src string, lib *std.StdLib) (fa *analysis.FileAnalysis, nodes []ast.Node) {
	defer func() {
		if recover() != nil {
			fa, nodes = nil, nil
		}
	}()
	return AnalyzeNodes(src, lib)
}

// HoverIn answers the hover markdown at (line, col) in the file name names,
// both 1-based and col in UTF-16 code units, or "" when there is nothing
// to show.
func (p *Program) HoverIn(name string, line, col int) (md string) {
	f, ok := p.File(name)
	if !ok {
		return ""
	}
	defer func() {
		if recover() != nil {
			md = ""
		}
	}()
	return hoverdoc.At(f.FA, f.Nodes, analysis.Pos{Line: line, Col: ByteCol(f.Source, line, col)})
}

// SemanticTokensIn answers the semantic tokens of the file name names, as
// SemanticTokens answers them for a snippet.
func (p *Program) SemanticTokensIn(name string) (toks []semtokens.Token) {
	f, ok := p.File(name)
	if !ok {
		return nil
	}
	defer func() {
		if recover() != nil {
			toks = nil
		}
	}()
	return semtokens.UTF16(f.Source, semtokens.Collect(f.FA))
}
