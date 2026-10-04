package analysis

// MergeImplManifestForTest exposes mergeImplManifest to external _test
// packages.
func MergeImplManifestForTest(dst, src *FileAnalysis) { mergeImplManifest(dst, src) }
