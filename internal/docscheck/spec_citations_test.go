package docscheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// A spec citation outside docs/spec.md names a chapter by its number and,
// optionally, a heading or bold lead-in in it: `spec §7`, `§7 *Calls*`,
// `§7, *Calls*`, `§7's *Calls*`, `§7 "Calls"`. These are the forms
// cmd/nomi-docgen links in the spec's own pages.
var (
	citeRef       = regexp.MustCompile(`§ ?(\d+)(?:\.(\d+))?(?:(?:,? |'s )(\*[^*\n]+\*|"[^"\n]+"))?`)
	specChapter   = regexp.MustCompile(`^## (\d+)\. (.+)$`)
	specHeadingRe = regexp.MustCompile(`^#{3,6} (.+)$`)
	specLeadIn    = regexp.MustCompile(`^(?:> )?\*\*([^*]+?)[.:]?\*\*`)
	markdownLink  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	styleBefore   = regexp.MustCompile(`(?:style(?: guide)?|style\.md) $`)
)

// specChapters is a spec's chapters by number.
type specChapters map[int]*chapterNames

// chapterNames is what a citation may name in one chapter.
type chapterNames struct {
	title string
	names []string // the title, then each heading, in order
	leads []string // bold lead-ins
}

// readSpecChapters reads each `## N. Title` of a spec with the headings and
// bold lead-ins under it, outside fenced code.
func readSpecChapters(src string) specChapters {
	chapters := specChapters{}
	var cur *chapterNames
	fence := ""
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if len(line)-len(trimmed) <= 3 {
			if fence != "" {
				if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, fence[:1]) == "" {
					fence = ""
				}
				continue
			}
			for _, c := range []string{"`", "~"} {
				n := len(trimmed) - len(strings.TrimLeft(trimmed, c))
				if n >= 3 && !(c == "`" && strings.Contains(trimmed[n:], "`")) {
					fence = strings.Repeat(c, n)
				}
			}
			if fence != "" {
				continue
			}
		} else if fence != "" {
			continue
		}
		if m := specChapter.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			cur = &chapterNames{title: m[2], names: []string{m[2]}}
			chapters[n] = cur
			continue
		}
		if cur == nil {
			continue
		}
		if m := specHeadingRe.FindStringSubmatch(line); m != nil {
			cur.names = append(cur.names, m[1])
		} else if m := specLeadIn.FindStringSubmatch(line); m != nil {
			cur.leads = append(cur.leads, m[1])
		}
	}
	return chapters
}

// slugOf is the GitHub-style id of a heading's text: code spans keep their
// contents, emphasis markers and link targets go.
func slugOf(md string) string {
	md = markdownLink.ReplaceAllString(md, "$1")
	md = strings.NewReplacer("`", "", "*", "", `\`, "").Replace(md)
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(md)) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// has reports whether name names a heading or lead-in of the chapter, by
// the rules cmd/nomi-docgen links it with: the same text; the part of a
// heading before a colon or a dash; a lead-in; or a heading's first words.
func (c *chapterNames) has(name string) bool {
	want := slugOf(name)
	for _, h := range c.names {
		if slugOf(h) == want {
			return true
		}
		for _, sep := range []string{": ", " — "} {
			if lead, _, ok := strings.Cut(h, sep); ok && slugOf(lead) == want {
				return true
			}
		}
	}
	for _, l := range c.leads {
		if slugOf(l) == want {
			return true
		}
	}
	for _, h := range c.names {
		if strings.HasPrefix(slugOf(h), want+"-") {
			return true
		}
	}
	return false
}

// citationProblems answers what is wrong with each spec citation in text,
// the prose of one line (a Markdown line or a comment's text). A citation
// of the style guide (`style §N`, `style guide §N`, `style.md §N`, a link
// to style.md) is not one. onlySpec keeps the citations written `spec §N`,
// as in the style guide, where a bare `§N` names one of its rules;
// prevEndsSpec says the line before ended with `spec`, so a `§` that opens
// this line is one too.
func citationProblems(text string, chapters specChapters, onlySpec, prevEndsSpec bool) []string {
	var out []string
	inCode := false
	for i := 0; i < len(text); i++ {
		if text[i] == '`' {
			inCode = !inCode
			continue
		}
		if inCode || !strings.HasPrefix(text[i:], "§") {
			continue
		}
		before := text[:i]
		if styleBefore.MatchString(before) || (strings.HasSuffix(before, "[") && strings.Contains(text[i:], "](style.md")) {
			continue
		}
		isSpec := strings.HasSuffix(before, "spec ") || (strings.TrimSpace(before) == "" && prevEndsSpec)
		if onlySpec && !isSpec {
			continue
		}
		m := citeRef.FindStringSubmatch(text[i:])
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		c := chapters[n]
		switch {
		case c == nil:
			out = append(out, fmt.Sprintf("`%s` names no chapter: docs/spec.md has chapters 1 to %d", m[0], len(chapters)))
		case m[2] != "":
			out = append(out, fmt.Sprintf("`%s` names a numbered section, and the spec numbers only its chapters: cite `§%d *Heading*`", m[0], n))
		case m[3] != "" && !c.has(strings.Trim(m[3], `*"`)):
			out = append(out, fmt.Sprintf("`%s` names no heading or bold lead-in of chapter %d, %s", m[0], n, c.title))
		}
		i += len(m[0]) - 1
	}
	return out
}

// commentText answers the text of a line's `//` comment in Go or Nomi
// source, or "" when it has none. A `//` inside a string literal is not
// one.
func commentText(line string) string {
	quotes := 0
	for i := 0; i+1 < len(line); i++ {
		switch line[i] {
		case '"':
			if i == 0 || line[i-1] != '\\' {
				quotes++
			}
		case '`':
			if quotes%2 == 0 {
				return "" // a raw string literal opens: its text is not a comment
			}
		case '/':
			if line[i+1] == '/' && quotes%2 == 0 {
				return strings.TrimLeft(line[i:], "/!#")
			}
		}
	}
	return ""
}

func TestSpecCitations_TheScannerReadsEachForm(t *testing.T) {
	chapters := readSpecChapters("# Spec\n\n## 1. Intro\n\n## 2. Functions\n\n### Calls: named arguments\n\n**Trailing lambdas.** Text.\n\n```nomi\n### Not a heading\n```\n")
	for _, tc := range []struct {
		text     string
		onlySpec bool
		bad      int
	}{
		{"see spec §2.", false, 0},
		{"see §2 *Calls*", false, 0},
		{"see §2, *Trailing lambdas*", false, 0},
		{`see §2's "Calls: named arguments"`, false, 0},
		{"see §2 *Not a heading*", false, 1},
		{"see §3", false, 1},
		{"see §2.4", false, 1},
		{"see style §30, style guide §9 and style.md §1", false, 0},
		{"a bare §30 names a rule here, and spec §2 *Calls* the spec", true, 0},
		{"spec §9 *Calls*", true, 1},
		{"`§99` in code", false, 0},
	} {
		if got := citationProblems(tc.text, chapters, tc.onlySpec, false); len(got) != tc.bad {
			t.Errorf("citationProblems(%q) = %q, want %d problems", tc.text, got, tc.bad)
		}
	}
	if got := citationProblems("§2 *Calls* opens the line", chapters, true, true); len(got) != 0 {
		t.Errorf("a citation after a line ending in `spec` = %q, want none", got)
	}
	if got := commentText(`x := "http://a" // spec §2`); got != " spec §2" {
		t.Errorf("commentText = %q, want the comment after the string", got)
	}
}

// TestSpecCitations_EveryCitationOutsideTheSpecNamesAChapterAndHeading
// holds each spec citation in docs/*.md and in Go and Nomi comments to
// docs/spec.md: its number names a chapter, and the heading or bold
// lead-in it gives is in that chapter. Renumbering or retitling the spec
// fails here until the citations move with it.
func TestSpecCitations_EveryCitationOutsideTheSpecNamesAChapterAndHeading(t *testing.T) {
	root, files := trackedFilesMatching(t, "docs/*.md", "*.go", "*.nomi")
	spec, err := os.ReadFile(filepath.Join(root, "docs", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	chapters := readSpecChapters(string(spec))
	if len(chapters) < 10 {
		t.Fatalf("docs/spec.md has %d `## N. Title` chapters; the scan is not reading it", len(chapters))
	}
	checked := 0
	for _, rel := range files {
		if rel == "docs/spec.md" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			if os.IsNotExist(err) {
				continue // deleted in the working tree, not yet committed
			}
			t.Fatal(err)
		}
		if !strings.Contains(string(src), "§") {
			continue
		}
		markdown := strings.HasSuffix(rel, ".md")
		onlySpec := rel == "docs/style.md"
		prevEndsSpec := false
		for n, line := range strings.Split(string(src), "\n") {
			text := line
			if !markdown {
				text = commentText(line)
			}
			if strings.Contains(text, "§") {
				checked++
				for _, p := range citationProblems(text, chapters, onlySpec, prevEndsSpec) {
					t.Errorf("%s:%d: %s", rel, n+1, p)
				}
			}
			prevEndsSpec = strings.HasSuffix(strings.TrimSpace(text), "spec")
		}
	}
	if checked < 100 {
		t.Errorf("read %d lines with a `§`; the repository holds far more", checked)
	}
}
