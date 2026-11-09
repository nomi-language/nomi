package docscheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A citation of a package doc's section names the package by its directory
// and the section by its heading: `(internal/irbuild package doc, "Linkage
// names")`. The citation may wrap across comment lines.
var packageDocCitation = regexp.MustCompile(`(?:^|[\s(])((?:internal|cmd|lib|rt|vmhost|vmrunner|hostadapt|std)(?:/[\w./-]+)??)(?:'s)? package doc,? (?:section )?"([^"]+)"`)

// commentPrefix is what a line of a comment starts with, stripped before the
// lines are joined so that a citation can wrap.
var commentPrefix = regexp.MustCompile(`^\s*(?://|#|\*|--)?\s*`)

// guideLink is a Markdown link's target.
var guideLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// TestPackageDocCitationsResolve holds every package doc citation in tracked
// Go, Nomi, Markdown and shell files to the package docs: the directory has
// a doc.go, and the doc.go has a `# Heading` line with the cited heading.
// The design notes live in package docs, so a renamed heading or a moved
// section fails here until its citations move.
func TestPackageDocCitationsResolve(t *testing.T) {
	root, files := trackedFilesMatching(t, "*.go", "*.nomi", "*.md", "*.sh")
	headings := map[string]map[string]bool{}
	headingsOf := func(dir string) (map[string]bool, bool) {
		if h, ok := headings[dir]; ok {
			return h, h != nil
		}
		data, err := os.ReadFile(filepath.Join(root, dir, "doc.go"))
		if err != nil {
			headings[dir] = nil
			return nil, false
		}
		h := map[string]bool{}
		for _, line := range strings.Split(string(data), "\n") {
			if rest, ok := strings.CutPrefix(line, "// # "); ok {
				h[strings.TrimSpace(rest)] = true
			}
		}
		headings[dir] = h
		return h, true
	}
	var problems []string
	found := 0
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "package doc") {
			continue
		}
		for _, m := range packageDocCitation.FindAllStringSubmatch(joinedLines(string(data)), -1) {
			found++
			dir, heading := strings.TrimSuffix(m[1], "."), m[2]
			h, ok := headingsOf(dir)
			switch {
			case !ok:
				problems = append(problems, fmt.Sprintf("%s: %s has no doc.go", rel, dir))
			case !h[heading]:
				problems = append(problems, fmt.Sprintf("%s: %s/doc.go has no heading %q", rel, dir, heading))
			}
		}
	}
	if found == 0 {
		t.Fatal("found no package doc citation; the scan is not reading the repository")
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// TestMarkdownLinksToRepositoryFilesResolve holds each relative link in the
// repository's own guides (AGENTS.md, CONTRIBUTING.md, README.md, docs/,
// editors/, tour/README.md, tree-sitter-nomi/README.md) to a file or
// directory that exists, since they point at package docs by path.
func TestMarkdownLinksToRepositoryFilesResolve(t *testing.T) {
	root, files := trackedFilesMatching(t, "AGENTS.md", "CONTRIBUTING.md", "README.md", "docs/*.md", "editors/*.md", "editors/**/*.md", "tour/README.md", "tree-sitter-nomi/README.md")
	if len(files) < 5 {
		t.Fatalf("found %d guides; the scan is not reading the repository", len(files))
	}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		inFence := false
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
			}
			if inFence {
				continue
			}
			for _, m := range guideLink.FindAllStringSubmatch(line, -1) {
				target, _, _ := strings.Cut(m[1], "#")
				if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "/") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				if _, err := os.Stat(filepath.Join(root, filepath.Dir(rel), filepath.FromSlash(target))); err != nil {
					t.Errorf("%s:%d: link to %s, which does not exist", rel, i+1, m[1])
				}
			}
		}
	}
}

// joinedLines joins a file's lines, each without its comment prefix, so a
// citation wrapped across comment lines reads as one.
func joinedLines(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = commentPrefix.ReplaceAllString(l, "")
	}
	return strings.Join(lines, " ")
}
