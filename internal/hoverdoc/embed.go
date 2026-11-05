package hoverdoc

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// maxEmbedFilesShown is how many embedded files a hover lists before it
// says how many more there are.
const maxEmbedFilesShown = 10

// embedPatternsText is an `embed`'s patterns on one line, as written.
func embedPatternsText(d *ast.EmbedDecl) string {
	parts := make([]string, len(d.Patterns))
	for i, p := range d.Patterns {
		if strings.ContainsAny(p.Text, " \t") {
			parts[i] = `"` + p.Text + `"`
		} else {
			parts[i] = p.Text
		}
	}
	return strings.Join(parts, " ")
}

// EmbedSummary says what an `embed` holds: how many files and bytes, and
// the first files by path with their sizes.
func EmbedSummary(info *analysis.EmbedInfo) string {
	var b strings.Builder
	n := len(info.Files)
	noun := "files"
	if n == 1 {
		noun = "file"
	}
	fmt.Fprintf(&b, "%d %s, %s, embedded when the program is compiled", n, noun, ByteSize(info.TotalSize()))
	for i, f := range info.Files {
		if i == maxEmbedFilesShown {
			fmt.Fprintf(&b, "\n- and %d more", n-maxEmbedFilesShown)
			break
		}
		fmt.Fprintf(&b, "\n- `%s` (%s)", f.Path, ByteSize(f.Size))
	}
	return b.String()
}

// ByteSize is n bytes for a reader: `512 B`, `1.5 KiB`, `3.2 MiB`.
func ByteSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
}
