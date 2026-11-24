package docscheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// htmlBlockStart matches a line that Markdown reads as the start of an HTML
// block: up to three spaces, `<` or `</`, and one of CommonMark's block tag
// names. Such a line ends the paragraph it sits in, and everything up to the
// next blank line is passed through as HTML, so a wrapped sentence whose
// next line begins with a placeholder like `<dir>` loses its code spans and
// is indented as a quotation.
var htmlBlockStart = regexp.MustCompile(`^ {0,3}</?(address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul)([ \t>/]|$)`)

// TestNoProseLineStartsAnHTMLBlock fails for a line outside a code fence, in
// the Markdown the site generates pages from or GitHub renders, that begins
// with an HTML block tag. docs/tooling.md once wrapped the placeholder
// `<dir>` to the start of a line, and the page rendered the rest of the
// paragraph as raw HTML. The tour's own pages write HTML on purpose and are
// not read here. Rewrap the sentence, or put the text in a fence.
func TestNoProseLineStartsAnHTMLBlock(t *testing.T) {
	root, tracked := trackedFilesMatching(t, "*.md")
	var files []string
	for _, rel := range tracked {
		if strings.HasPrefix(rel, "tour/") || strings.Contains(rel, "/testdata/") || strings.HasPrefix(rel, "tests/") {
			continue
		}
		files = append(files, filepath.Join(root, rel))
	}
	if len(files) < 10 {
		t.Fatalf("found %d Markdown files, so the filter no longer matches the tree", len(files))
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, file)
		fence := ""
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimLeft(line, " ")
			if fence != "" {
				if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, "`~ ") == "" {
					fence = ""
				}
				continue
			}
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				fence = trimmed[:3]
				for len(fence) < len(trimmed) && trimmed[len(fence)] == fence[0] {
					fence += fence[:1]
				}
				continue
			}
			if htmlBlockStart.MatchString(line) {
				t.Errorf("%s:%d begins with an HTML block tag, so Markdown renders the rest of its paragraph as raw HTML: %s", rel, i+1, strings.TrimSpace(line))
			}
		}
	}
}
