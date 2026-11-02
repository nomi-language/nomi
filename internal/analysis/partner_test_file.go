package analysis

import (
	"path/filepath"
	"strings"
)

// IsPartnerTestPath reports whether test is the test file of src: a file in
// the same directory named as src with `_test` before `.nomi`
// (`coffee_test.nomi` for `coffee.nomi`). The name decides, whether or not
// the file declares tests. A partner test reads src's private declarations
// as if they were `pub`, and is inside the boundary of src's opaque types,
// as a Go test in the same package or a Rust test module is (spec §14
// *Visibility*). Nothing else gains access, and the test file re-exports
// none of what it reads.
func IsPartnerTestPath(test, src string) bool {
	if test == "" || src == "" || test == src {
		return false
	}
	if filepath.Dir(test) != filepath.Dir(src) {
		return false
	}
	base := filepath.Base(src)
	stem, ok := strings.CutSuffix(base, ".nomi")
	if !ok || stem == "" {
		return false
	}
	return filepath.Base(test) == stem+"_test.nomi"
}

// readsPrivateOf reports whether this file may read sym although sym is
// not `pub`: sym is declared in the file this file is the partner test of.
func (fa *FileAnalysis) readsPrivateOf(sym *Symbol) bool {
	return fa != nil && sym != nil && IsPartnerTestPath(fa.partnerPath(), sym.SourceFile)
}

// inside reports whether this file is within the private boundary of the
// file at path: it is that file, or its partner test. An opaque type's
// representation and a file's private declarations are open inside it.
func (fa *FileAnalysis) inside(path string) bool {
	return fa != nil && (fa.FilePath == path || IsPartnerTestPath(fa.partnerPath(), path))
}

// partnerPath is the path the partner-test rule reads this file's name
// from: its FilePath, or, for an entry checked with no path (a tour block's
// file, whose siblings carry paths relative to the block), its module key
// as a relative file name.
func (fa *FileAnalysis) partnerPath() string {
	if fa.FilePath != "" || fa.Origin == "" || fa.Origin == OriginEntry {
		return fa.FilePath
	}
	return fa.Origin + ".nomi"
}
