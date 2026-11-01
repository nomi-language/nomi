package analysis

import (
	"path/filepath"
	"strings"
)

// GeneratedTourPageForTest reports whether path is a page the tour build
// generates from another source, whose examples are checked there: the
// stdlib reference from std/, the Specification from docs/spec.md, Writing
// Nomi from docs/style.md and Tooling from docs/tooling.md. A sweep over the
// tour's own pages skips them.
func GeneratedTourPageForTest(path string) bool {
	p := filepath.ToSlash(path)
	for _, dir := range []string{"/content/docs/reference/", "/content/docs/spec/", "/content/docs/writing/", "/content/docs/tooling/"} {
		if strings.Contains(p, dir) {
			return true
		}
	}
	return false
}
