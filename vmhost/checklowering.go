package vmhost

import (
	"context"
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

// LoweringFound is what CheckLowering finds: its problems, and each outside
// call the program reaches, as an editor's hover shows it.
type LoweringFound = analyzedlowering.Found

// CheckLowering is the half of Check past the front end for the file at path
// whose text is src, as an editor holds it: the program is lowered as `nomi
// run` lowers it, its typed literals are checked (checkLiterals),
// and nothing else runs. found.Problems is what Check reports beyond the
// front end: each literal that fails its check (Code InvalidLiteralCode) and
// each `Currency.custom` call its module does not list
// (UnlistedCurrencyCode), then what Program.Unsupported reports, then what
// nomi.toml's [effects] lists refuse, then the entry's hints that `nomi
// check` does not print, each literal `Currency.custom` call that is no error
// (CustomCurrencyCallCode); nil when there is none. found.Reached is each
// effect call the roots `nomi check` holds to the lists reach, then each
// one only a function they do not reach reaches, whether or not nomi.toml
// has lists (editorWalks, reachedCalls). err is the load's own failure: the
// front end's errors, which an editor reports from its own analysis, or an
// *InternalError when the compiler panicked. A stdlib file is not a program
// and answers nothing.
//
// A program the front end refuses whose errors are all in functions, impl
// functions and tests is lowered all the same, with those declarations left
// unlowered (irbuild.Program.ErrorDecls), and found.Reached is what the walks
// find in the rest, beside the front end's error: a body that checked has
// its marks and hover lines, and a call of one that did not says nothing
// of what it may do. found.Problems stays nil, and nothing is held to the
// lists: only a program that checks is.
//
// When ctx is done the check stops at its next step (after the front end,
// before each file's declarations and bodies are lowered, before the
// literals are checked and before the unsupported bodies are looked for)
// and answers ctx's error as err.
func CheckLowering(ctx context.Context, path, src string, opts ...Option) (found LoweringFound, err error) {
	if _, _, ok, refused := frontend.StdlibFile(path); ok || refused != nil {
		return found, nil
	}
	cfg := newConfig(opts)
	cfg.lowering = ctx
	var errored *Program
	cfg.errored = &errored
	fc, err := cfg.frontendConfig()
	if err != nil {
		return found, err
	}
	p, err := lower(cfg, func() (*irbuild.Program, error) { return irbuild.AnalyzeFileSource(path, src, fc) })
	if err != nil {
		if errored != nil {
			errored.sources = map[string]string{errored.prog.Entry().Path: src}
			found.Reached = errored.editorReached()
		}
		return found, err
	}
	return loweringProblems(p, src)
}

// editorReached is what an editor's hover and marks say of the effects
// code has (reachedCalls), from the walks `nomi check` holds to the lists
// and the walk from every function they do not reach (editorWalks). For a
// program lowered from the declarations that checked (config.errored),
// nil when a walk panics: an editor shows its marks or goes without.
func (p *Program) editorReached() (calls []analyzedlowering.Reached) {
	if p.prog.ErrorDecls != nil {
		defer func() {
			if recover() != nil {
				calls = nil
			}
		}()
	}
	return p.reachedCalls(p.editorWalks(p.walks(p.checkedRoots())))
}

func init() {
	analyzedlowering.SetCheck(checkLoweringAnalyzed, offeredAnalyzed)
	analyzedlowering.SetKept(keptLoweringOf)
}

// offeredAnalyzed is analyzedlowering.Offered: whether
// checkLoweringAnalyzed would lower a.
func offeredAnalyzed(path, src string, a *analyzedlowering.Analyzed) bool {
	if a != nil && a.Stored != nil {
		return true
	}
	if _, _, ok, refused := frontend.StdlibFile(path); ok || refused != nil {
		return true
	}
	cfg := newConfig(nil)
	fc, err := cfg.frontendConfig()
	if err != nil {
		return true
	}
	return analyzedProgram(path, src, a, fc) != nil
}

// checkLoweringAnalyzed is analyzedlowering.Check: CheckLowering for a
// file whose text src the language server has already analyzed as a.
func checkLoweringAnalyzed(ctx context.Context, path, src string, a *analyzedlowering.Analyzed) (found LoweringFound, ok bool, err error) {
	if _, _, ok, refused := frontend.StdlibFile(path); ok || refused != nil {
		return found, true, nil
	}
	cfg := newConfig(nil)
	cfg.lowering = ctx
	fc, err := cfg.frontendConfig()
	if err != nil {
		return found, true, err
	}
	if a != nil && a.Stored != nil {
		return checkLoweringStored(ctx, cfg, path, src, a.Stored)
	}
	prog := analyzedProgram(path, src, a, fc)
	if prog == nil {
		return found, false, nil
	}
	abs := prog.Entry().Path
	if p, ok := relowerAnalyzed(cfg, abs, src, a, prog); ok {
		found, err = loweringProblems(p, src)
		return found, true, err
	}
	p, err := lower(cfg, func() (*irbuild.Program, error) { return prog, nil })
	if err != nil {
		analyzedKept.Delete(abs)
		return found, true, err
	}
	if cfg.loweringStopped() == nil {
		keepAnalyzed(abs, src, a, prog, p)
	}
	found, err = loweringProblems(p, src)
	return found, true, err
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
// from: nomi.toml and the files that select its packages' versions, as
// analysis.ManifestTexts reads them (an editor's text when one is open),
// each other project file, and, when a file imports the
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
	if analysis.ManifestTexts(a.Root) != a.Manifest {
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
// p, whose entry's text is src. The walk from the roots `nomi check` holds
// to nomi.toml's lists runs once, with or without lists: its calls are
// held to the lists (walkedProblems), and with the walk from every
// function it does not reach they are kept for an editor's hover
// (editorWalks, reachedCalls).
func loweringProblems(p *Program, src string) (out LoweringFound, err error) {
	p.sources = map[string]string{p.prog.Entry().Path: src}
	defer func() {
		// Locating a blocker walks the IR and the AST; a panic there is a
		// compiler bug too, and a long-lived host keeps running.
		if r := recover(); r != nil {
			out, err = LoweringFound{}, &InternalError{Panic: r, Stack: debug.Stack()}
		}
	}()
	if err := p.cfg.loweringStopped(); err != nil {
		return out, err
	}
	var found frontend.Diagnostics
	if lit := p.checkLiterals(); lit != nil {
		var ds frontend.Diagnostics
		if !errors.As(lit, &ds) {
			return out, lit
		}
		found = append(found, ds...)
	}
	if err := p.cfg.loweringStopped(); err != nil {
		return out, err
	}
	if u := p.Unsupported(); u != nil {
		var ds frontend.Diagnostics
		if !errors.As(u, &ds) {
			if len(found) > 0 {
				return LoweringFound{Problems: found}, nil
			}
			return LoweringFound{Problems: u}, nil
		}
		found = append(found, ds...)
	}
	// What `nomi check` holds to nomi.toml's [effects] lists.
	walks := p.walks(p.checkedRoots())
	held, _ := p.walkedProblems(walks)
	found = append(found, held...)
	found = append(found, p.currencyCallHints()...)
	out.Reached = p.reachedCalls(p.editorWalks(walks))
	if len(found) > 0 {
		out.Problems = found
	}
	return out, nil
}
