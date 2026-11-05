package frontend

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/syntheticextern"
)

// A program of in-memory files that grows one file at a time, as a REPL
// session's does: each new file is checked against the interfaces of the
// files before it (analysis.ProgramInterfaces.AddFile), with no check of
// the whole program, and becomes the program's entry while it is checked.

// SessionSource describes the program whose files' texts text answers, by
// module key, for checking its files against interfaces one at a time
// (analysis.NewStoredInterfaces, then AddFile and SetEntry per file). Its
// root is the configuration's (ProjectRoot), "" for none.
func (c *Checker) SessionSource(text func(key string) (string, bool)) *analysis.SourceProgram {
	loader := c.Loader()
	return &analysis.SourceProgram{
		Root:     c.root,
		Manifest: c.manifest(),
		Source: func(key string) ([]ast.Node, bool) {
			src, ok := text(key)
			if !ok {
				return nil, false
			}
			nodes, err := parser.Parse(lexer.Lex(src))
			if err != nil {
				return nil, false
			}
			if nodes, err = syntheticextern.Inject(nodes, c.cfg.SyntheticExterns, true); err != nil {
				return nil, false
			}
			return analysis.ExpandDeclarations(nodes, c.site(key)), true
		},
		PrepareEntry: func(nodes []ast.Node) ([]ast.Node, []analysis.TypeError) {
			// A file may derive for a type it imports from another.
			nodes, errs := analysis.LowerDerivesWith(nodes, analysis.ImportedTypeDecl(nodes, func(key string) ([]ast.Node, bool) {
				src, ok := text(key)
				if !ok {
					return nil, false
				}
				decls, err := parser.Parse(lexer.Lex(src))
				return decls, err == nil
			}))
			StripAttachedTests(nodes)
			errs = append(errs, analysis.CheckDuplicateTestNames(nodes)...)
			nodes, _ = analysis.SynthesizeDerives(nodes)
			return analysis.SynthesizeUniversalDebug(nodes), errs
		},
		Loader:     loader,
		Primitives: c.lib.Primitives,
		Modules:    c.lib.Modules,
		StdlibFAs:  c.lib.Files,
		Session:    true,
	}
}

// CheckSessionFile checks the file with key, whose text is src and which
// pi has (AddFile), against the interfaces of the files before it, as the
// program's entry, and answers its check and its diagnostics as a check
// of the program from source reports the entry's: the errors before
// analysis, its build's, the program's duplicate and orphan impls, its
// bodies', its application-field roots, its keyword declarations'
// agreement, coherence, its iteration, concurrency, boot-scope and
// task-lifetime checks, and its host functions. name is what a diagnostic
// names the file by. The program's other reductions over every file's
// bodies are not run: each earlier file passed them when it was checked.
//
// bootFile is the key of the earlier file whose boot the file runs under
// when it declares none, "" for none: the file imports it.
func (c *Checker) CheckSessionFile(pi *analysis.ProgramInterfaces, key, name, src, bootFile string) (*analysis.FileCheck, error) {
	c.entryModRel, c.placeEntry, c.entryMode = key, false, Mode{}
	c.entrySrc, c.diagPath, c.prepared = src, name, true
	c.siblingSyntax = nil
	filesParsed.Add(1)
	if _, err := parser.Parse(lexer.Lex(src)); err != nil {
		return nil, located(name, src, err)
	}
	pi.SetEntry(key, name)
	check := pi.Check(key)
	fa, nodes := check.FA, check.Nodes
	errs := c.kept(check.FrontEnd)
	errs = append(errs, c.buildErrors(fa)...)
	errs = append(errs, c.kept(fa.Program().Errors)...)
	errs = append(errs, c.kept(check.Check)...)
	var boot *ast.FuncDef
	for _, n := range check.FileNodes[bootFile] {
		if fn, ok := n.(*ast.FuncDef); ok && fn.Name == "boot" && !fn.ImplFunction {
			boot = fn
		}
	}
	errs = append(errs, c.kept(analysis.CheckAppRootsUnder(fa, nodes, boot, check.Files[bootFile]))...)
	ext := analysis.CheckProgramDeclarations(map[string][]ast.Node{key: nodes})
	errs = append(errs, ext[key]...)
	errs = append(errs, ext[""]...)
	errs = append(errs, analysis.FinalizeCoherence(fa)...)
	errs = append(errs, check.EntryChecks...)
	errs = append(errs, analysis.CheckBootScope(fa, nodes)...)
	analysis.MarkTailCalls(nodes)
	if ds := stdDefectDiagnostics(c.lib); ds != nil {
		return check, ds
	}
	if len(c.siblingSyntax) > 0 {
		var syntax Diagnostics
		for path, perrs := range c.siblingSyntax {
			syntax = append(syntax, parseDiagnostics(path, c.sourceOf(path), perrs)...)
		}
		return check, syntax
	}
	if refused := analysis.ReservedNameRefusals(errs); len(refused) > 0 {
		return check, c.entryDiagnostics(fa, refused)
	}
	if len(errs) > 0 {
		return check, c.entryDiagnostics(fa, errs)
	}
	if err := c.ValidateHosts(map[string][]ast.Node{"": nodes}); err != nil {
		return check, err
	}
	return check, nil
}
