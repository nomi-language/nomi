package vmhost

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
)

// CheckLowering is the half of Check past the front end for the file at path
// whose text is src, as an editor holds it: the program is lowered as `nomi
// run` lowers it, its backtick typed literals are checked (checkLiterals),
// and nothing else runs. problems is what Check reports beyond the front
// end: each literal that fails its check (Code InvalidLiteralCode), then what
// Program.Unsupported reports; nil when there is none. err is the load's own
// failure: the front end's errors, which an editor reports from its own
// analysis, or an *InternalError when the compiler panicked. A stdlib file is
// not a program and answers nil, nil.
func CheckLowering(path, src string, opts ...Option) (problems, err error) {
	if _, _, ok := frontend.StdlibFile(path); ok {
		return nil, nil
	}
	cfg := newConfig(opts)
	fc, err := cfg.frontendConfig()
	if err != nil {
		return nil, err
	}
	p, err := lower(cfg, func() (*irbuild.Program, error) { return irbuild.AnalyzeFileSource(path, src, fc) })
	if err != nil {
		return nil, err
	}
	return loweringProblems(p, src)
}

func init() {
	analyzedlowering.SetCheck(checkLoweringAnalyzed)
}

// checkLoweringAnalyzed is analyzedlowering.Check: CheckLowering for a
// file whose text src the language server has already analyzed as a.
func checkLoweringAnalyzed(path, src string, a *analyzedlowering.Analyzed) (problems error, ok bool, err error) {
	if _, _, ok := frontend.StdlibFile(path); ok {
		return nil, true, nil
	}
	cfg := newConfig(nil)
	fc, err := cfg.frontendConfig()
	if err != nil {
		return nil, true, err
	}
	fc.SourceBoundProvided = true
	prog := analyzedProgram(path, src, a, fc)
	if prog == nil {
		return nil, false, nil
	}
	p, err := lower(cfg, func() (*irbuild.Program, error) { return prog, nil })
	if err != nil {
		return nil, true, err
	}
	problems, err = loweringProblems(p, src)
	return problems, true, err
}

// analyzedProgram is the program irbuild.AnalyzeFileSource would answer
// for the file at path, built from a, or nil when a is not that program.
//
// a must agree with what the front end would do with the same text
// (frontend.Checker.CheckFileSource): it reports no error, since any error
// stops the front end; its root is the one the front end finds; a file
// with no test keeps no attached test, since the front end strips them
// outside test mode; and each `host` declaration has something to answer
// it (frontend.Checker.ValidateHosts).
//
// Every file the front end would read must hold the text a was built
// from: nomi.toml, each other project file, and, when a file imports the
// entry back, the entry itself, which the front end reads from disk for
// that import.
func analyzedProgram(path, src string, a *analyzedlowering.Analyzed, fc frontend.Config) *irbuild.Program {
	if a == nil || a.FA == nil || len(a.FA.TypeErrors) > 0 {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil || analysis.ProjectRoot(abs, "") != a.Root || analysis.IsForeignStdlibDir(a.Root) {
		return nil
	}
	hasTests := frontend.DeclaresTests(a.Nodes)
	if !hasTests && frontend.NodesContainTests(a.Nodes) {
		return nil
	}
	if a.ReachesEntry && !onDisk(abs, src) {
		return nil
	}
	if !onDisk(filepath.Join(a.Root, "nomi.toml"), a.Manifest) {
		return nil
	}
	modules := map[string][]ast.Node{"": a.Nodes}
	for _, f := range a.Files {
		if !onDisk(f.Path, f.Text) {
			return nil
		}
		modules[f.Key] = f.Nodes
	}
	if frontend.New(fc).ValidateHosts(modules) != nil {
		return nil
	}
	name := strings.TrimSuffix(filepath.Base(abs), ".nomi")
	mods := []irbuild.Module{{Path: abs, Name: name, Nodes: a.Nodes, FA: a.FA}}
	for _, f := range a.Files {
		mods = append(mods, irbuild.Module{Path: f.Path, Name: f.Key, Nodes: f.Nodes, FA: f.FA})
	}
	return &irbuild.Program{Modules: mods, HasTests: hasTests, Root: a.Root}
}

// onDisk reports whether the file at path holds text, "" standing for no
// file at all.
func onDisk(path, text string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return text == "" && errors.Is(err, fs.ErrNotExist)
	}
	return string(data) == text
}

// loweringProblems is what CheckLowering reports for the lowered program
// p, whose entry's text is src.
func loweringProblems(p *Program, src string) (problems, err error) {
	p.sources = map[string]string{p.prog.Entry().Path: src}
	defer func() {
		// Locating a blocker walks the IR and the AST; a panic there is a
		// compiler bug too, and a long-lived host keeps running.
		if r := recover(); r != nil {
			problems, err = nil, &InternalError{Panic: r, Stack: debug.Stack()}
		}
	}()
	var found frontend.Diagnostics
	if lit := p.checkLiterals(); lit != nil {
		var ds frontend.Diagnostics
		if !errors.As(lit, &ds) {
			return nil, lit
		}
		found = append(found, ds...)
	}
	if u := p.Unsupported(); u != nil {
		var ds frontend.Diagnostics
		if !errors.As(u, &ds) {
			if len(found) > 0 {
				return found, nil
			}
			return u, nil
		}
		found = append(found, ds...)
	}
	if len(found) == 0 {
		return nil, nil
	}
	return found, nil
}
