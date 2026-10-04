package stdcompiler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// RunFrontEnd is the front half of `compiler.run`: parse, derive lowering,
// project analysis and every whole-program check, in the order `nomi run`
// applies them. It answers the prepared tree and its analysis, or the error a
// `compiler.run` caller reads as `Err(message)`.
//
// The error text is part of the contract. A parse failure is the joined parse
// errors and an analysis failure is `analysis errors:` followed by one
// indented line per error. nomi/stdcompilerrun calls this first, and its
// engine, the VM, lowers the source again once this has accepted it.
func RunFrontEnd(source, root string, virtualFiles map[string]string) ([]ast.Node, *analysis.FileAnalysis, error) {
	nodes, parseErrs := ParseSource(source)
	if len(parseErrs) > 0 {
		return nil, nil, errors.New(JoinParseErrors(parseErrs))
	}
	nodes, lowerErrs := analysis.LowerDerives(nodes)
	if len(lowerErrs) > 0 {
		return nil, nil, errors.New(JoinTypeErrors(lowerErrs))
	}
	nodes, _ = analysis.SynthesizeDerives(nodes)
	nodes = analysis.SynthesizeUniversalDebug(nodes)

	lib := std.Load()
	fa, siblingFAs, siblingNodes := analysis.BuildProjectFromEntryWithManifest(
		"",
		nodes,
		lib.Primitives,
		lib.Modules,
		lib.Files,
		root,
		Loader(virtualFiles),
		nil,
		"",
	)
	var errs []analysis.TypeError
	errs = append(errs, fa.TypeErrors...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	program := []analysis.ProgramFile{{FA: fa, Nodes: nodes}}
	for key, sibFA := range siblingFAs {
		_ = analysis.CheckTypes(sibFA, siblingNodes[key])
		analysis.MergeImplManifest(fa, sibFA)
		program = append(program, analysis.ProgramFile{FA: sibFA, Nodes: siblingNodes[key]})
	}
	errs = append(errs, analysis.CheckImplImports(program)[fa]...)
	errs = append(errs, analysis.FinalizeCoherence(fa)...)
	errs = append(errs, analysis.AnalyzeIterSensitivity(fa, nodes)...)
	errs = append(errs, analysis.CheckConcurrentScope(fa, nodes)...)
	errs = append(errs, analysis.CheckBootScope(fa, nodes)...)
	errs = append(errs, analysis.CheckTaskLifetime(fa, nodes)...)
	if len(errs) > 0 {
		return nil, nil, fmt.Errorf("analysis errors:\n  %s", JoinTypeErrors(errs))
	}
	analysis.MarkTailCalls(nodes)
	return nodes, fa, nil
}

// EntryFileSource reads the entry file `compiler.run_file` names, or reports
// why it will not.
//
// The four rejections are a containment rule: an entry point must name a
// module UNDER the current project root, so an absolute path, a `..` escape, a
// bare `.` and a `.nomi` suffix (which would make `"main.nomi"` and `"main"`
// two spellings of one module) are each refused with their own message. Each
// becomes the `Err(message)` a Nomi program reads. A virtual file wins over
// the real one, which is what lets an in-memory project test a `run_file`
// without touching disk.
func EntryFileSource(entryPoint, projectRoot string, virtualFiles map[string]string) (string, error) {
	if entryPoint == "" {
		return "", fmt.Errorf("compiler.run_file entry point must not be empty")
	}
	if strings.HasSuffix(entryPoint, ".nomi") {
		return "", fmt.Errorf("compiler.run_file entry point must omit .nomi")
	}
	clean := filepath.ToSlash(filepath.Clean(entryPoint))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(entryPoint) {
		return "", fmt.Errorf("compiler.run_file entry point must stay under the current project root")
	}
	if src, ok := virtualFiles[clean]; ok {
		return src, nil
	}
	if projectRoot == "" {
		return "", fmt.Errorf("compiler.run_file requires a project root")
	}
	data, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(clean)+".nomi"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
