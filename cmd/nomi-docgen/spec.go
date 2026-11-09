package main

// The site's Specification section: docs/spec.md split into one page per
// numbered section, an index page, and the section's sidebar
// (_sidebar.json), which tour/astro.config.mjs reads.
//
//	go run ./cmd/nomi-docgen -spec docs/spec.md tour/src/content/docs/spec
//
// docs/spec.md stays the one source: internal/parser's spec example test
// and vmhost.TestSpecPrograms read it, so the pages are generated and
// gitignored (scripts/build-tour-wasm.sh writes them, as it writes the
// stdlib reference), and the tests that read every page under
// tour/src/content/docs skip them.
//
// A page's file name is its heading's text without the number, in
// lowercase words joined by hyphens (`## 4. Types` is types.md, served
// at /spec/types/), so a link to a page lasts when chapters are
// renumbered. Headings keep their text, so their anchors
// are the ones GitHub gives them, counted per page. A page's own heading
// becomes its title and every subheading moves up one level.
//
// A section reference in the prose becomes a link: `§13` to the page,
// `§38.1` to the subsection whose heading starts with that number, and
// `§10, *Bindings with `else`*` (also `§10 *...*`, `§10's *...*` and a
// quoted heading) to that heading's anchor on the page. A link into the
// document (`#anchor`, `spec.md#anchor`) goes to the page and anchor that
// heading landed on, and a link to another document under docs/ goes to
// its file on GitHub.

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// specRepo is where a link from the spec to another file in the repository
// points, since only the spec is on the site. The link's target is relative
// to docs/.
const specRepo = "https://github.com/nomi-language/nomi/blob/main/"

// specSite is the section's path on the site.
const specSite = "/spec/"

type specSection struct {
	num      int
	title    string // the heading's text after `N. `
	slug     string
	body     []specLine // the lines after the `## N.` heading
	headings []specHeading
	labels   []specLabel
}

// specLabel is a paragraph's bold lead-in (`**Logical:**`, `> **Module
// boundaries.**`), which a reference may name as it names a heading. The
// page gives one an anchor only when a reference names it.
type specLabel struct {
	text   string
	line   int    // its line in the section's body
	anchor string // "" until a reference names it
}

type specHeading struct {
	level  int    // in docs/spec.md
	text   string // its plain text, as a slugger reads it
	anchor string // its id on its page
}

type specLine struct {
	text  string
	fence bool // inside a fenced code block, or a fence line itself
}

// specDoc is docs/spec.md read into its preamble and sections.
type specDoc struct {
	preamble []specLine // after the `# ` title, before §1
	sections []*specSection
	byNum    map[int]*specSection
	// docAnchors maps each heading's anchor in docs/spec.md read as one
	// page (as GitHub shows it) to its section and its anchor there.
	docAnchors map[string]specTarget
	// unresolved lists each reference that names no section or heading.
	unresolved []string
}

type specTarget struct {
	section *specSection
	anchor  string // "" for the page itself
}

var (
	specSectionHeading = regexp.MustCompile(`^## (\d+)\. (.+)$`)
	specSubheading     = regexp.MustCompile(`^(#{1,6}) (.+)$`)
	// specRef matches a section reference after its `§`: the number, an
	// optional subsection number, and an optional heading after it.
	specRef        = regexp.MustCompile(`^(\d+)(?:\.(\d+))?(?:(,? |'s )(\*[^*\n]+\*|"[^"\n]+"))?`)
	specMdLink     = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	specInlineLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	specNumbered   = regexp.MustCompile(`^\d+\.\d+ (.*)$`)
	specListItem   = regexp.MustCompile(`^(?:[-*+]|\d+\.) `)
	specLabelLine  = regexp.MustCompile(`^(> )?\*\*([^*]+?)[.:]?\*\*`)
)

// fenceLines marks each line inside a fenced code block, fence lines
// included. A backtick fence's info string holds no backtick, so a line
// that opens and closes an inline span with three backticks is prose.
func fenceLines(lines []string) []specLine {
	out := make([]specLine, len(lines))
	var open string
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		out[i].text = line
		if open != "" {
			out[i].fence = true
			if indent <= 3 && strings.HasPrefix(trimmed, open) && strings.Trim(trimmed, open[:1]) == "" {
				open = ""
			}
			continue
		}
		if indent > 3 {
			continue
		}
		for _, c := range []string{"`", "~"} {
			n := len(trimmed) - len(strings.TrimLeft(trimmed, c))
			if n < 3 || c == "`" && strings.Contains(trimmed[n:], "`") {
				continue
			}
			open = strings.Repeat(c, n)
			out[i].fence = true
		}
	}
	return out
}

func readSpec(path string) (*specDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := fenceLines(strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"))
	doc := &specDoc{byNum: map[int]*specSection{}, docAnchors: map[string]specTarget{}}
	docSlugs := newSlugger()
	slugs := map[string]int{}
	var cur *specSection
	var pageSlugs *slugger
	for _, l := range lines {
		if !l.fence {
			if m := specSectionHeading.FindStringSubmatch(l.text); m != nil {
				num, _ := strconv.Atoi(m[1])
				cur = &specSection{num: num, title: strings.TrimSpace(m[2])}
				cur.slug = pageSlug(cur.title)
				if prev, ok := slugs[cur.slug]; ok {
					return nil, fmt.Errorf("§%d and §%d would both be spec/%s.md", prev, num, cur.slug)
				}
				if doc.byNum[num] != nil {
					return nil, fmt.Errorf("two sections are numbered %d", num)
				}
				slugs[cur.slug] = num
				doc.byNum[num] = cur
				doc.sections = append(doc.sections, cur)
				pageSlugs = newSlugger()
				doc.docAnchors[docSlugs.slug(headingText(m[1]+". "+m[2]))] = specTarget{section: cur}
				continue
			}
			if m := specSubheading.FindStringSubmatch(l.text); m != nil {
				text := headingText(m[2])
				docAnchor := docSlugs.slug(text)
				if cur != nil {
					h := specHeading{level: len(m[1]), text: text, anchor: pageSlugs.slug(text)}
					cur.headings = append(cur.headings, h)
					doc.docAnchors[docAnchor] = specTarget{section: cur, anchor: h.anchor}
				} else if len(m[1]) == 1 {
					continue // the document's title
				}
			}
		}
		if cur == nil {
			doc.preamble = append(doc.preamble, l)
			continue
		}
		if m := specLabelLine.FindStringSubmatch(l.text); m != nil && !l.fence {
			cur.labels = append(cur.labels, specLabel{text: m[2], line: len(cur.body)})
		}
		cur.body = append(cur.body, l)
	}
	if len(doc.sections) == 0 {
		return nil, fmt.Errorf("%s holds no `## N. Title` sections", path)
	}
	return doc, nil
}

// pageSlug is a section's file name: its heading's words, lowercased and
// joined by single hyphens.
func pageSlug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(headingText(title)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	return b.String()
}

// headingText is a heading's text as the page shows it: code spans keep
// their contents; emphasis markers, escapes and link targets go.
func headingText(md string) string {
	var b strings.Builder
	for i := 0; i < len(md); {
		switch md[i] {
		case '`':
			if code, end, ok := codeSpan(md, i); ok {
				b.WriteString(code)
				i = end
				continue
			}
		case '*':
			i++
			continue
		case '\\':
			if i+1 < len(md) {
				b.WriteByte(md[i+1])
				i += 2
				continue
			}
		case '[':
			if loc := specInlineLink.FindStringSubmatchIndex(md[i:]); loc != nil && loc[0] == 0 {
				b.WriteString(headingText(md[i+loc[2] : i+loc[3]]))
				i += loc[1]
				continue
			}
		}
		b.WriteByte(md[i])
		i++
	}
	return strings.TrimSpace(b.String())
}

// codeSpan reads the code span that opens at s[i] (a run of backticks):
// its contents, the index after it, and whether it closes. One that does
// not close is the run of backticks alone.
func codeSpan(s string, i int) (string, int, bool) {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	for j := i + n; j < len(s); {
		if s[j] != '`' {
			j++
			continue
		}
		m := 0
		for j+m < len(s) && s[j+m] == '`' {
			m++
		}
		if m == n {
			code := s[i+n : j]
			if len(code) >= 2 && code[0] == ' ' && code[len(code)-1] == ' ' && strings.Trim(code, " ") != "" {
				code = code[1 : len(code)-1]
			}
			return code, j + m, true
		}
		j += m
	}
	return "", i + n, false
}

// slugger gives headings the ids GitHub and Astro give them
// (github-slugger): lowercased, every character but a letter, digit, mark,
// `-` or `_` dropped, each space a hyphen, and a repeat suffixed `-1`,
// `-2`, ...
type slugger struct{ seen map[string]int }

func newSlugger() *slugger { return &slugger{seen: map[string]int{}} }

func (s *slugger) slug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r):
			b.WriteRune(r)
		}
	}
	base := b.String()
	out := base
	for {
		n, ok := s.seen[out]
		if !ok {
			break
		}
		s.seen[base] = n + 1
		out = base + "-" + strconv.Itoa(n+1)
	}
	s.seen[out] = 0
	return out
}

func plainSlug(text string) string { return newSlugger().slug(headingText(text)) }

// sameHeading reports whether a reference's text names a heading: equal
// once both are slugged, or equal to the heading with its `N.M ` number
// left off.
func sameHeading(ref, heading string) bool {
	r := plainSlug(ref)
	if r == plainSlug(heading) {
		return true
	}
	if m := specNumbered.FindStringSubmatch(heading); m != nil {
		return r == plainSlug(m[1])
	}
	return false
}

// link answers a link to the target from the page of section from (nil
// for the index).
func (t specTarget) link(from *specSection) string {
	if t.section == from && t.anchor != "" {
		return "#" + t.anchor
	}
	out := specSite + t.section.slug + "/"
	if t.anchor != "" {
		out += "#" + t.anchor
	}
	return out
}

// rewriteProse rewrites a line of prose on section from's page: its
// section references and its links.
func (d *specDoc) rewriteProse(line string, from *specSection) string {
	line = d.rewriteLinks(line, from)
	var b strings.Builder
	for i := 0; i < len(line); {
		if line[i] == '`' {
			_, end, _ := codeSpan(line, i)
			b.WriteString(line[i:end])
			i = end
			continue
		}
		if strings.HasPrefix(line[i:], "§") && !insideLink(line, i) {
			if text, link, ok := d.resolveRef(line[i+len("§"):], from); ok {
				fmt.Fprintf(&b, "[§%s](%s)", text, link)
				i += len("§") + len(text)
				continue
			}
		}
		b.WriteByte(line[i])
		i++
	}
	return b.String()
}

// resolveRef reads the section reference rest starts with (the text after
// its `§`) and answers the reference's text and its link from section
// from's page (nil for the index, or for a page outside the spec). A number
// that names no section answers false. A subsection or heading that names
// nothing there leaves the link at the section. Both are recorded in
// d.unresolved.
func (d *specDoc) resolveRef(rest string, from *specSection) (string, string, bool) {
	m := specRef.FindStringSubmatchIndex(rest)
	if m == nil {
		return "", "", false
	}
	text := rest[:m[1]]
	num, _ := strconv.Atoi(rest[m[2]:m[3]])
	sec := d.byNum[num]
	if sec == nil {
		d.unresolved = append(d.unresolved, "§"+text+" (no such section)")
		return "", "", false
	}
	target := specTarget{section: sec}
	if m[4] >= 0 {
		number := rest[m[2]:m[5]]
		if target.anchor = sec.numberedAnchor(number); target.anchor == "" {
			d.unresolved = append(d.unresolved, "§"+number)
		}
	}
	if m[8] >= 0 {
		name := strings.Trim(rest[m[8]:m[9]], `*"`)
		if anchor, ok := sec.namedAnchor(name); !ok {
			d.unresolved = append(d.unresolved, "§"+text)
			text = rest[:m[6]] // the number alone
		} else if m[4] < 0 {
			target.anchor = anchor
		}
	}
	return text, target.link(from), true
}

// insideLink reports whether line[i] is in a Markdown link's text.
func insideLink(line string, i int) bool {
	open := strings.LastIndex(line[:i], "[")
	return open >= 0 && !strings.Contains(line[open:i], "]")
}

// numberedAnchor answers the anchor of the subsection whose heading starts
// with number (`38.1`), or "" for none.
func (s *specSection) numberedAnchor(number string) string {
	for _, h := range s.headings {
		if strings.HasPrefix(h.text, number+" ") {
			return h.anchor
		}
	}
	return ""
}

// namedAnchor answers the anchor of what name names in the section ("" for
// the section itself), trying in turn: a heading with that text; a heading
// that starts with it before a colon or a dash (`Scripts` for `Scripts:
// `nomi <file>“); a bold lead-in with that text; and a heading whose
// first words it is (`Locales` for `Locales and Plural Rules`).
func (s *specSection) namedAnchor(name string) (string, bool) {
	type candidate struct {
		text   string
		anchor string
	}
	candidates := []candidate{{s.title, ""}}
	for _, h := range s.headings {
		candidates = append(candidates, candidate{h.text, h.anchor})
	}
	for _, c := range candidates {
		if sameHeading(name, c.text) {
			return c.anchor, true
		}
	}
	for _, c := range candidates {
		if lead, _, ok := cutAny(c.text, ": ", " — "); ok && sameHeading(name, lead) {
			return c.anchor, true
		}
	}
	for i := range s.labels {
		l := &s.labels[i]
		if sameHeading(name, l.text) {
			if l.anchor == "" {
				l.anchor = s.labelAnchor(l.text)
			}
			return l.anchor, true
		}
	}
	ref := plainSlug(name) + "-"
	for _, c := range candidates {
		if strings.HasPrefix(plainSlug(c.text), ref) {
			return c.anchor, true
		}
	}
	return "", false
}

// cutAny cuts s around the first of seps it holds.
func cutAny(s string, seps ...string) (string, string, bool) {
	for _, sep := range seps {
		if before, after, ok := strings.Cut(s, sep); ok {
			return before, after, true
		}
	}
	return s, "", false
}

// labelAnchor answers an id for a lead-in that no heading or other lead-in
// on the page has.
func (s *specSection) labelAnchor(text string) string {
	taken := map[string]bool{}
	for _, h := range s.headings {
		taken[h.anchor] = true
	}
	for _, l := range s.labels {
		taken[l.anchor] = true
	}
	base := plainSlug(text)
	out := base
	for n := 1; taken[out]; n++ {
		out = base + "-" + strconv.Itoa(n)
	}
	return out
}

// rewriteLinks points a link into the document at its page and a link to
// another document under docs/ at GitHub.
func (d *specDoc) rewriteLinks(line string, from *specSection) string {
	return specMdLink.ReplaceAllStringFunc(line, func(m string) string {
		target := specMdLink.FindStringSubmatch(m)[1]
		switch {
		case strings.Contains(target, "://"), strings.HasPrefix(target, "/"), strings.HasPrefix(target, "mailto:"):
			return m
		case strings.HasPrefix(target, "#"), target == "spec.md", strings.HasPrefix(target, "spec.md#"):
			_, anchor, _ := strings.Cut(target, "#")
			if anchor == "" {
				return "](" + specSite + ")"
			}
			if t, ok := d.docAnchors[anchor]; ok {
				return "](" + t.link(from) + ")"
			}
			d.unresolved = append(d.unresolved, target)
			return m
		default:
			return "](" + specRepo + path.Clean("docs/"+target) + ")"
		}
	})
}

// render writes a section's lines for its page: prose rewritten, each
// heading one level up, an anchor before each lead-in a reference names,
// and a trailing rule dropped. Every page must be rendered once before
// the pages are written, since a reference marks the lead-in it names.
func (d *specDoc) render(lines []specLine, from *specSection) string {
	anchors := map[int]string{}
	if from != nil {
		for _, l := range from.labels {
			if l.anchor != "" {
				anchors[l.line] = l.anchor
			}
		}
	}
	var b strings.Builder
	for i, l := range lines {
		switch {
		case l.fence:
			b.WriteString(l.text)
		case strings.HasPrefix(l.text, "##") && specSubheading.MatchString(l.text):
			b.WriteString(l.text[1:])
		default:
			text := d.rewriteProse(l.text, from)
			if id, ok := anchors[i]; ok {
				quote := ""
				if strings.HasPrefix(text, "> ") {
					quote, text = "> ", text[2:]
				}
				text = quote + `<a id="` + id + `"></a>` + text
			}
			b.WriteString(text)
		}
		b.WriteByte('\n')
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimSpace(strings.TrimSuffix(out, "---"))
	return out + "\n"
}

// sectionSummary is the index's line for a section: the first sentence
// of the first paragraph or list item before its first subheading, or,
// when there is none, its first subheadings' names.
func sectionSummary(s *specSection) string {
	if para := firstParagraph(s.body); para != "" {
		return firstSentence(para)
	}
	var names []string
	for _, h := range s.headings {
		if len(names) < 4 && h.level == 3 {
			names = append(names, h.text)
		}
	}
	return strings.Join(names, ", ") + "."
}

// firstSentence ends para at its first period, or at a colon that ends it.
func firstSentence(para string) string {
	for i := 0; i < len(para); i++ {
		if para[i] == '`' {
			_, end, _ := codeSpan(para, i)
			i = end - 1
			continue
		}
		if para[i] == '.' && (i+1 == len(para) || para[i+1] == ' ') {
			word := para[strings.LastIndexAny(para[:i], " (")+1 : i]
			if word == "e.g" || word == "i.e" || word == "vs" {
				continue
			}
			return para[:i+1]
		}
	}
	if strings.HasSuffix(para, ":") {
		return strings.TrimSuffix(para, ":") + "."
	}
	return para
}

// firstParagraph joins the lines of the first paragraph or list item
// before the first subheading, with a list item's marker left off.
// Code, tables, quotes and HTML are none.
func firstParagraph(lines []specLine) string {
	var para []string
	for _, l := range lines {
		t := strings.TrimSpace(l.text)
		if !l.fence && strings.HasPrefix(t, "#") {
			break
		}
		if len(para) > 0 {
			if l.fence || t == "" || specListItem.MatchString(t) {
				break
			}
			para = append(para, t)
			continue
		}
		if l.fence || t == "" || t == "---" || strings.HasPrefix(t, "|") ||
			strings.HasPrefix(t, ">") || strings.HasPrefix(t, "<") {
			continue
		}
		para = append(para, specListItem.ReplaceAllString(t, ""))
	}
	return strings.Join(para, " ")
}

// plainText is Markdown as text, for a page's meta description.
func plainText(md string) string {
	return strings.Join(strings.Fields(headingText(md)), " ")
}

// yamlString quotes s for frontmatter: a JSON string is a YAML one.
func yamlString(s string) string {
	out, _ := json.Marshal(s)
	return string(out)
}

func (s *specSection) label() string { return fmt.Sprintf("%d. %s", s.num, headingText(s.title)) }

const specGenerateCommand = "go run ./cmd/nomi-docgen -spec docs/spec.md tour/src/content/docs/spec"

// specPages answers every file the Specification section holds, by file
// name, and each reference that names no section or heading.
func specPages(specPath string) (map[string]string, []string, error) {
	doc, err := readSpec(specPath)
	if err != nil {
		return nil, nil, err
	}
	// The first pass marks the lead-ins references name.
	for _, s := range doc.sections {
		doc.render(s.body, s)
	}
	doc.unresolved = nil
	type item struct {
		Label string `json:"label"`
		Link  string `json:"link"`
	}
	pages := map[string]string{}
	sidebar := []item{{"Overview", specSite}}
	var index strings.Builder
	index.WriteString("---\ntitle: Specification\n")
	index.WriteString("description: \"The Nomi language specification: what the language does, section by section.\"\n---\n\n")
	fmt.Fprintf(&index, "<!-- Generated from docs/spec.md by `%s`. Do not edit. -->\n\n", specGenerateCommand)
	index.WriteString(doc.render(doc.preamble, nil))
	index.WriteString("\nThese pages are generated from [`docs/spec.md`](https://github.com/nomi-language/nomi/blob/main/docs/spec.md) in the repository.\n\n")
	index.WriteString("| § | Section | What it covers |\n|---|---|---|\n")
	for _, s := range doc.sections {
		summary := sectionSummary(s)
		var page strings.Builder
		fmt.Fprintf(&page, "---\ntitle: %s\n", yamlString(s.label()))
		if d := plainText(specInlineLink.ReplaceAllString(summary, "$1")); d != "" {
			fmt.Fprintf(&page, "description: %s\n", yamlString(d))
		}
		fmt.Fprintf(&page, "---\n\n<!-- Generated from docs/spec.md by `%s`. Do not edit. -->\n\n", specGenerateCommand)
		page.WriteString(doc.render(s.body, s))
		pages[s.slug+".md"] = page.String()
		sidebar = append(sidebar, item{s.label(), specSite + s.slug + "/"})
		cell := strings.ReplaceAll(doc.rewriteProse(summary, nil), "|", `\|`)
		fmt.Fprintf(&index, "| %d | [%s](%s%s/) | %s |\n", s.num, headingText(s.title), specSite, s.slug, cell)
	}
	pages["index.md"] = index.String()
	side, err := json.MarshalIndent(sidebar, "", "\t")
	if err != nil {
		return nil, nil, err
	}
	pages[sidebarFile] = string(side) + "\n"
	return pages, doc.unresolved, nil
}

func writeSpec(specPath, outDir string) error {
	pages, unresolved, err := specPages(specPath)
	if err != nil {
		return err
	}
	if len(unresolved) > 0 {
		// TestSpecReferencesResolve fails on these; the pages link the
		// section alone.
		fmt.Fprintf(os.Stderr, "nomi-docgen: %s: these references name no section or heading: %s\n", specPath, strings.Join(unresolved, "; "))
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	old, err := os.ReadDir(outDir)
	if err != nil {
		return err
	}
	for _, e := range old {
		if _, keep := pages[e.Name()]; !keep {
			if err := os.RemoveAll(filepath.Join(outDir, e.Name())); err != nil {
				return err
			}
		}
	}
	for name, text := range pages {
		if err := os.WriteFile(filepath.Join(outDir, name), []byte(text), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("nomi-docgen: wrote %d specification pages to %s\n", len(pages)-1, outDir)
	return nil
}
