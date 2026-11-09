package analysis

import (
	"reflect"
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
)

// Rebuilding an open document's bodies in a program opened from its record.
//
// An open document of a program opened from its record is analyzed by its
// own check against the other files' interfaces (FileCheck), which builds
// the declarations of every file it reads: the entry's reads the whole
// program's, about 0.28 s at 50k lines. An edit that leaves the document's
// interface as it was (interfaceFingerprint, and every unannotated `once`
// value) rebuilds the bodies of the installed check in place instead, as
// a kept program's recheck does (rebuildEditedBodies): the kept
// declaration nodes take the new bodies and positions (graftEdit), every
// position the declarations hold moves with the edit (shiftDeclarations,
// over the files the check read), and the file's bodies are walked again.
// The check's own diagnostics are then worked out again as Check works
// them out. A rebuild rewrites an installed analysis, so it takes the view
// lock exclusively before it writes, and the build holds it until its
// answer is installed (storedProgram.held).
//
// The installed analyses of the other open documents stay as they are when
// no file their checks read changed how its interface is written
// (storedProgram.current): a body edit that moves no declaration of its
// file analyzes no other document again.

// storedInstall is the check an open document's installed analysis was
// made from, the build installed from it, and the program's state then.
type storedInstall struct {
	c   *FileCheck
	b   builtAnalysis
	gen int
}

// current reports whether inst, the installed analysis of the document
// with module key key, is still what a check of it answers: neither its
// text nor how any file its check read writes its interface changed since
// it was made.
func (sp *storedProgram) current(inst *storedInstall, key string) bool {
	if sp.textAt[key] > inst.gen || sp.changedAt[key] > inst.gen {
		return false
	}
	for k := range inst.c.Files {
		if sp.changedAt[k] > inst.gen {
			return false
		}
	}
	return true
}

// rebuildStoredBodies rebuilds in place the bodies of c, the installed
// check of the open document with module key key, whose text was oldText
// and is newText, and answers "" when it did, or why it did not. It
// answers written true once it has rewritten c, whatever it answers then:
// c is then the new text's, and the caller holds the view lock until the
// document's next analysis is installed.
func (dm *DocumentManager) rebuildStoredBodies(sp *storedProgram, key, oldText string, pf *parsedFile, c *FileCheck) (why string, written bool) {
	fa, nodes := c.FA, c.Nodes
	newText := pf.text
	switch {
	case fa == nil || fa.keptDecls() == nil:
		return "the file's declarations were not kept", false
	case len(c.Bodies) > 0:
		return "the check read another file's bodies", false
	case len(pf.errs) > 0 || len(pf.damaged) > 0:
		return "the new text has a syntax error", false
	}
	if sp.fingerprints[key] == "" {
		old := parseFile(oldText)
		if len(old.errs) > 0 || len(old.damaged) > 0 {
			return "the old text has a syntax error", false
		}
		if sp.fingerprints == nil {
			sp.fingerprints = map[string]string{}
		}
		sp.fingerprints[key] = old.fingerprint
	}
	if sp.fingerprints[key] != pf.fingerprint {
		return "the file's interface changed", false
	}
	entry := key == sp.rec.Entry
	manifest, _ := LoadManifest(sp.rec.Root)
	site := dm.expansionSite(sp.rec.Root, key, manifest, dm.readInput)
	prepared, frontEnd := preparedNodes(pf.nodes, entry, nodes, site, key, func(k string) ([]ast.Node, bool) {
		n, ok := c.FileNodes[k]
		return n, ok
	})
	if len(prepared) != len(nodes) {
		return "the prepared declarations differ in number", false
	}
	g := &grafter{kept: map[ast.Node]bool{}}
	for i := range nodes {
		g.value(reflect.ValueOf(&nodes[i]).Elem(), reflect.ValueOf(&prepared[i]).Elem())
	}
	if g.reason == "" {
		g.placeMoves(oldText, newText)
	}
	if g.reason != "" {
		return g.reason, false
	}
	oldFuncs, oldImports := bodyContents(g.oldBodies)
	newFuncs, newImports := bodyContents(g.newBodies)
	inOrder := slices.Equal(oldImports, newImports)
	slices.Sort(oldImports)
	slices.Sort(newImports)
	if !slices.Equal(oldImports, newImports) {
		return "a body's imports changed", false
	}
	if reason := shiftableKept(fa, g.shift); reason != "" {
		return reason, false
	}
	prog := fa.Program()
	if g.shift.moves() && len(prog.Errors) > 0 && prog.programErrors == nil {
		return "the check has errors of its own and positions move", false
	}
	if sp.held == nil {
		if !dm.lockView() {
			return "readers held the view", false
		}
		sp.held = dm.viewMu.Unlock
	}

	// Decided: c becomes the new text's.
	prog.revision++
	g.commit()
	if g.shift.moves() {
		files := map[string]*FileAnalysis{key: fa}
		for k, f := range c.Files {
			files[k] = f
		}
		entryKey := ""
		if entry {
			entryKey = key
		}
		shiftDeclarations(&CheckedProgram{Files: files, Entry: entryKey, checks: &programChecks{}}, fa, g.shift, g.kept)
	}
	if prog.ScopedFunctionFiles != nil {
		for _, fn := range oldFuncs {
			delete(prog.ScopedFunctionFiles, fn)
		}
		for _, fn := range newFuncs {
			prog.ScopedFunctionFiles[fn] = fa
		}
	}
	if prog.results != nil {
		for _, ii := range fa.ImplImports {
			delete(prog.results.implImportsUnused, ii)
		}
	}
	if g.shift.moves() && prog.programErrors != nil {
		prog.Errors = prog.programErrors()
	}
	generation := fa.generation
	fa.walkBodiesAgain(nodes)
	if inOrder {
		fa.carryImports(generation)
	}
	if prog.Onces.reinfer(map[*FileAnalysis]bool{fa: true}) {
		return "an unannotated once's type may have changed", true
	}
	// The check's own diagnostics, as Check works them out.
	c.Build = slices.Clone(fa.TypeErrors)
	c.Check, c.EntryChecks, c.FrontEnd = nil, nil, nil
	if entry {
		c.Check = CheckTypes(fa, nodes)
		c.EntryChecks = AnalyzeIterSensitivity(fa, nodes)
		c.EntryChecks = append(c.EntryChecks, CheckConcurrentScope(fa, nodes)...)
		c.EntryChecks = append(c.EntryChecks, CheckTaskLifetime(fa, nodes)...)
		c.FrontEnd = frontEnd
	} else {
		c.Check = checkSibling(fa, nodes)
	}
	MarkTailCalls(nodes)
	return "", true
}
