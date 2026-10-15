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
