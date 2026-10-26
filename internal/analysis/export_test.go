package analysis

import (
	"fmt"
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// RebuildBodiesForTest throws away the body part of the analysis of p's file
// with module key and rebuilds it against the file's kept declarations and
// the other files' analyses (rebuildBodies). It then runs again what reads
// that file's bodies: the program's reductions over every file (application
// field roots, whole-file imports, coherence), the entry's own checks when
// the file is the entry, and tail-call marking on the file, and assembles
// p.Errors again. It writes p in place, and answers false for a file the
// program does not hold or did not keep.
func RebuildBodiesForTest(p *CheckedProgram, key string) bool {
	c := p.checks
	fa, nodes := c.fileOf(p, key)
	if fa == nil {
		return false
	}
	check, ok := fa.rebuildBodies(nodes)
	if !ok {
		return false
	}
	c.build[key] = slices.Clone(fa.TypeErrors)
	c.check[key] = check
	c.reduce(p)
	if key == p.Entry {
		c.checkEntry(p)
	}
	MarkTailCalls(nodes)
	c.assemble(p)
	return true
}

// NodesForTest answers every node of p's syntax trees.
func NodesForTest(p *CheckedProgram) map[ast.Node]bool {
	nodes := map[ast.Node]bool{}
	for _, ns := range p.Nodes {
		for _, n := range ns {
			WalkNodes(n, func(m ast.Node) { nodes[m] = true })
		}
	}
	return nodes
}

// StaleForTest describes the first thing p's analyses hold of an analysis
// that is no longer p's, or answers "": a file whose Program is not the
// entry's, or a symbol or dispatch a file's facts name whose node is one of
// was, the nodes of the program p was rechecked from, and not one of p's
// own (a declaration of a file that was checked afresh, held by one that was
// kept). A dump renders symbols by name and position, so it cannot show
// this.
func StaleForTest(p *CheckedProgram, was map[ast.Node]bool) string {
	entry := p.Files[p.Entry]
	if entry == nil {
		return ""
	}
	now := NodesForTest(p)
	// The prelude's imports are one set of nodes every build prepends, to an
	// entry's nodes too, which p.Nodes leaves out.
	stale := func(n ast.Node) bool { return n != nil && was[n] && !now[n] && !isPreludeInjectLine(n.LineNum()) }
	for key, fa := range p.Files {
		if fa.kept != nil && fa.Program() != entry.Program() {
			return fmt.Sprintf("file %s holds another program than the entry's", key)
		}
		for _, m := range []map[Pos]*Symbol{fa.References, fa.Definitions, fa.PunnedFieldLabels} {
			for pos, sym := range m {
				for s, hops := sym, 0; s != nil && hops < 64; s, hops = s.Resolved, hops+1 {
					if stale(s.Node) || s.DispatchImpl != nil && stale(s.DispatchImpl) {
						return fmt.Sprintf("file %s at %d:%d names %s, whose node is the program's before the recheck", key, pos.Line, pos.Col, s.Name)
					}
				}
			}
		}
	}
	return ""
}

// BuiltForTest is what one document's build gives the language server: its
// diagnostics are those it publishes, without the errors about another file.
type BuiltForTest struct {
	TypeErrors             []TypeError
	Syntax, Damaged, Nodes int
	Program                bool
	ProgramFiles           int
	ReachesEntry           bool
	// MemberProgramFiles counts the files of the program offered to a
	// document an entry reaches, 0 when none is.
	MemberProgramFiles int
}

// BuildForTest builds the open document uri's text content: through the
// program kept for its root (buildOpen) when kept is set, and on its own
// (build) otherwise.
func BuildForTest(dm *DocumentManager, uri, content string, kept bool) BuiltForTest {
	dm.lockBuild(true)
	defer dm.analyzeMu.Unlock()
	var b builtAnalysis
	if kept {
		var release func()
		b, release = dm.buildOpen(uri, content)
		release()
	} else {
		b = dm.build(uri, content)
	}
	out := BuiltForTest{Syntax: len(b.errs), Damaged: len(b.damaged), Nodes: len(b.nodes)}
	if b.fa != nil {
		out.TypeErrors = ErrorsInFile(fileuri.ToPath(uri), b.fa.TypeErrors)
	}
	out.setProgram(b.program)
	return out
}

// setProgram records p, a document's offered program. A program offered
// to a document an entry reaches (memberProgramOf) is counted apart:
// analyzing the document on its own (build) never offers one.
func (out *BuiltForTest) setProgram(p *EntryProgram) {
	switch {
	case p == nil:
	case p.member:
		out.MemberProgramFiles = len(p.Files) + 1
	default:
		out.Program, out.ProgramFiles, out.ReachesEntry = true, len(p.Files), p.ReachesEntry
	}
}

// InstalledForTest is BuildForTest's answer for what is installed on the
// open document uri.
func InstalledForTest(dm *DocumentManager, uri string) BuiltForTest {
	snap := dm.Snapshot(uri)
	out := BuiltForTest{Syntax: len(snap.Errors), Damaged: len(snap.Damaged), Nodes: len(snap.Nodes)}
	if snap.Analysis != nil {
		out.TypeErrors = ErrorsInFile(fileuri.ToPath(uri), snap.Analysis.TypeErrors)
	}
	out.setProgram(snap.Program)
	return out
}

// UnionDemandsForTest exposes unionDemands, the fold of files' own
// ImplManifests that MissingImpls reads, to external _test packages.
func UnionDemandsForTest(files []*FileAnalysis) map[string]map[string][]Recording {
	return unionDemands(files)
}

// KeepStaleDemandsForTest runs f with FinalizeCoherence's cache keeping
// each file's part after the file's bodies are rebuilt (staleDemands).
func KeepStaleDemandsForTest(f func()) {
	staleDemands.Store(true)
	defer staleDemands.Store(false)
	f()
}

// KeptImplImportsForTest answers the unused whole-file imports of p's files
// as the program's kept uses find them (checkImplImportsKept), and as
// CheckImplImports finds them from scratch.
func KeptImplImportsForTest(p *CheckedProgram) (kept, fresh map[*FileAnalysis][]TypeError) {
	program := p.checks.program
	kept = p.Files[p.Entry].Program().checkImplImportsKept(program)
	return kept, CheckImplImports(program)
}

// KeptCoherenceForTest answers, for p's entry program, what the cached
// coherence checks answer (FinalizeCoherence and the entry document's
// entryCoherence) and the same checks worked out from scratch by
// MissingImpls: over every file's demands, and over the entry's with only
// the declarations' demands of the other files a project build kept.
func KeptCoherenceForTest(p *CheckedProgram) (kept, fresh, keptEntry, freshEntry []TypeError) {
	entry := p.Files[p.Entry]
	prog := entry.Program()
	var demanders []*FileAnalysis
	for _, f := range prog.Files() {
		if f != entry && f.kept != nil {
			decls := *f
			decls.ImplManifest = f.kept.manifest
			f = &decls
		}
		demanders = append(demanders, f)
	}
	return FinalizeCoherence(entry), MissingImpls(prog, prog.Files()), entryCoherence(p), MissingImpls(prog, demanders)
}

// ProgramDemandsForTest answers the union of every ImplManifest of fa's
// program, each file's own, bucket by bucket.
func ProgramDemandsForTest(fa *FileAnalysis) map[string]map[string][]Recording {
	out := map[string]map[string][]Recording{}
	files := fa.Program().Files()
	if len(files) == 0 {
		files = []*FileAnalysis{fa}
	}
	for _, file := range files {
		for iface, byType := range file.ImplManifest {
			if out[iface] == nil {
				out[iface] = map[string][]Recording{}
			}
			for typeName, recs := range byType {
				out[iface][typeName] = append(out[iface][typeName], recs...)
			}
		}
	}
	return out
}
