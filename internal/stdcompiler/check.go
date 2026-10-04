// Package stdcompiler is std/compiler's host implementation: the check, hover
// and check_project pipelines, answered as rt values.
//
// internal/compilerhosts converts between these functions and the VM's
// operands. `rt` links no front end and imports none of the compiler's
// packages (TestRuntimeArtifactLinksNoFrontEnd,
// TestRuntimeImportsOnlyItsAllowlist). That is why this package is here
// and not in rt: the implementation IS the analyzer.
//
// # `run` and `run_file` are next door
//
// This package does not import an execution engine. `compiler.run` and
// `run_file` EXECUTE a source, so their host is nomi/stdcompilerrun, whose
// engine is the VM that nomi/vmhost installs. What they share with this
// package is the front end: RunFrontEnd (run.go) is the parse-and-analysis
// half, so a parse or analysis failure reads the same whether it comes from
// `check` or from `run`.
//
// # WHAT THIS PACKAGE LINKS, AND WHAT rt STILL DOES NOT
//
// `hover` reaches nomi/internal/hoverdoc, which is the SAME renderer nomi/lsp
// calls (lsp/hover.go's RenderWithAnalysis) — deliberately, because that is what
// makes `hover.signature` in a Nomi test equal to what an editor shows. The
// package nomi/lsp itself is NOT in this package's import closure and must not
// become so: what a hover answer needs is a renderer over a resolved analysis,
// not a language server.
//
// None of that touches rt. rt has ZERO dependencies and reaches nothing in this
// module; `rt.Hover` and `rt.Diagnostic` are string and int64 fields with no
// behaviour. The split that must survive is DATA in rt, IMPLEMENTATION here, and
// anything renderer-shaped drifting toward rt is a defect rather than a
// simplification.
package stdcompiler

import (
	"strings"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// Diagnostics is `compiler.check`: check source as a standalone Nomi entry
// file and answer every diagnostic, which internal/compilerhosts' host
// converts to the VM's values. root is the directory project-relative imports
// resolve against; left empty, such an import reports "no such module".
//
// virtualFiles are in-memory sibling sources keyed by import path, which only
// an in-memory project has; a real check passes nil and gets std.MakeLoader.
func Diagnostics(source, root string, virtualFiles map[string]string) []rt.Diagnostic {
	nodes, parseErrs := ParseSource(source)
	if len(parseErrs) > 0 {
		return ParseDiagnostics(parseErrs)
	}
	return ProjectDiagnostics(nodes, root, Loader(virtualFiles), nil, "", false)
}

// ParseSource lexes and parses one checked source with error recovery, so a
// syntactically broken program still reports every parse error as data rather
// than stopping at the first.
func ParseSource(source string) ([]ast.Node, []parser.ParseError) {
	tokens := lexer.Lex(source)
	return parser.ParseWithRecovery(tokens)
}

// ParseDiagnostics is the parse-error half of the answer. A file with parse
// errors is never type-checked, so these are reported alone.
func ParseDiagnostics(parseErrs []parser.ParseError) []rt.Diagnostic {
	diags := make([]rt.Diagnostic, len(parseErrs))
	for i, err := range parseErrs {
		diags[i] = rt.Diagnostic{Line: int64(err.Line), Col: int64(err.Col), Message: err.Message}
	}
	return diags
}

// PrepareNodes runs the derive lowering and synthesis passes a checked tree
// goes through before analysis, and reports the errors they raise.
func PrepareNodes(nodes []ast.Node) ([]ast.Node, []analysis.TypeError) {
	lowered, lowerErrs := analysis.LowerDerives(nodes)
	extended, deriveErrs := analysis.SynthesizeDerives(lowered)
	errs := append(lowerErrs, deriveErrs...)
	return analysis.SynthesizeUniversalDebug(extended), errs
}

// ProjectDiagnostics is the whole analysis pass over one prepared entry tree,
// and it is the single place the pass ORDER lives. `compiler.check` reaches it
// with no manifest and sibling diagnostics suppressed; `compiler.check_project`
// reaches it with both.
func ProjectDiagnostics(entryNodes []ast.Node, root string, loader analysis.FileLoader, manifest *analysis.Manifest, entryPoint string, includeSiblingDiagnostics bool) []rt.Diagnostic {
	lib := std.Load()
	entryNodes, prepErrs := PrepareNodes(entryNodes)
	fa, siblingFAs, siblingNodes := analysis.BuildProjectFromEntryWithManifest(
		"",
		entryNodes,
		lib.Primitives,
		lib.Modules,
		lib.Files,
		root,
		loader,
		manifest,
		entryPoint,
	)

	errs := append([]analysis.TypeError{}, prepErrs...)
	errs = append(errs, fa.TypeErrors...)
	errs = append(errs, analysis.CheckTypes(fa, entryNodes)...)
	var sibErrs []analysis.TypeError
	var sibFAs []*analysis.FileAnalysis
	program := []analysis.ProgramFile{{FA: fa, Nodes: entryNodes}}
	for key, sibFA := range siblingFAs {
		checkErrs := analysis.CheckTypes(sibFA, siblingNodes[key])
		if includeSiblingDiagnostics {
			sibErrs = append(sibErrs, sibFA.TypeErrors...)
			sibErrs = append(sibErrs, checkErrs...)
		}
		analysis.MergeImplManifest(fa, sibFA)
		program = append(program, analysis.ProgramFile{FA: sibFA, Nodes: siblingNodes[key]})
		sibFAs = append(sibFAs, sibFA)
	}
	// Whole-file imports nothing names, once every file's uses of impl
	// blocks are known.
	implImportErrs := analysis.CheckImplImports(program)
	errs = append(errs, implImportErrs[fa]...)
	if includeSiblingDiagnostics {
		for _, sibFA := range sibFAs {
			sibErrs = append(sibErrs, implImportErrs[sibFA]...)
		}
	}
	errs = append(errs, sibErrs...)
	errs = append(errs, analysis.FinalizeCoherence(fa)...)
	errs = append(errs, analysis.AnalyzeIterSensitivity(fa, entryNodes)...)
	errs = append(errs, analysis.CheckConcurrentScope(fa, entryNodes)...)
	errs = append(errs, analysis.CheckBootScope(fa, entryNodes)...)
	errs = append(errs, analysis.CheckTaskLifetime(fa, entryNodes)...)

	diags := make([]rt.Diagnostic, len(errs))
	for i, err := range errs {
		msg := err.Message
		for _, h := range err.Hints {
			msg += "\nhelp: " + h
		}
		diags[i] = rt.Diagnostic{Line: int64(err.Line), Col: int64(err.Col), Message: msg}
	}
	return diags
}

// Loader resolves a checked source's imports: virtual files first, then the
// bundled standard library and the project tree.
func Loader(virtualFiles map[string]string) analysis.FileLoader {
	base := std.MakeLoader()
	if len(virtualFiles) == 0 {
		return base
	}
	return func(root string, modulePath []string) ([]ast.Node, error) {
		key := strings.Join(modulePath, "/")
		if src, ok := virtualFiles[key]; ok {
			nodes, _ := ParseSource(src)
			return nodes, nil
		}
		return base(root, modulePath)
	}
}
