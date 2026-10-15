package analysis

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
