package analysis

import "slices"

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

// BuiltForTest is what one document's build gives the language server.
type BuiltForTest struct {
	TypeErrors             []TypeError
	Syntax, Damaged, Nodes int
	Program                bool
	ProgramFiles           int
	ReachesEntry           bool
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
		out.TypeErrors = b.fa.TypeErrors
	}
	if b.program != nil {
		out.Program, out.ProgramFiles, out.ReachesEntry = true, len(b.program.Files), b.program.ReachesEntry
	}
	return out
}

// InstalledForTest is BuildForTest's answer for what is installed on the
// open document uri.
func InstalledForTest(dm *DocumentManager, uri string) BuiltForTest {
	snap := dm.Snapshot(uri)
	out := BuiltForTest{Syntax: len(snap.Errors), Damaged: len(snap.Damaged), Nodes: len(snap.Nodes)}
	if snap.Analysis != nil {
		out.TypeErrors = snap.Analysis.TypeErrors
	}
	if p := snap.Program; p != nil {
		out.Program, out.ProgramFiles, out.ReachesEntry = true, len(p.Files), p.ReachesEntry
	}
	return out
}

// UnionDemandsForTest exposes unionDemands, the fold of files' own
// ImplManifests that MissingImpls reads, to external _test packages.
func UnionDemandsForTest(files []*FileAnalysis) map[string]map[string][]Recording {
	return unionDemands(files)
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
