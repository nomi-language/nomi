package docscheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/virtualproject"
)

// The tour client cuts a block into its files with highlight.mjs's
// FILE_MARKER, and every Go reader of a block with
// virtualproject.FileMarker. A block the two cut differently shows one
// set of tabs and runs another, so the two are one expression.
func TestFileMarkersMatch(t *testing.T) {
	path := filepath.Join("..", "..", "tour", "src", "lib", "highlight.mjs")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^const FILE_MARKER = /(.*)/;$`).FindSubmatch(data)
	if m == nil {
		t.Fatalf("%s has no `const FILE_MARKER = /.../;` line", path)
	}
	js := strings.ReplaceAll(string(m[1]), `\/`, `/`)
	if goExpr := virtualproject.FileMarker.String(); js != goExpr {
		t.Errorf("%s's FILE_MARKER is\n\t%s\nand virtualproject.FileMarker is\n\t%s\nchange them together", path, js, goExpr)
	}
}
