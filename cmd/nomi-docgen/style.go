package main

// The site's Writing Nomi section: docs/style.md as one page per topic (a
// `## Topic` heading and the numbered `### N. Rule` sections under it), an
// index listing every rule, and the section's sidebar (_sidebar.json), which
// tour/astro.config.mjs reads.
//
//	go run ./cmd/nomi-docgen -style docs/style.md tour/src/content/docs/writing
//
// docs/style.md stays the one source: its examples are tested from it
// (TestDocExamplesAreFormatted), so the pages are generated and gitignored
// (scripts/build-tour-wasm.sh writes them), and the tests that read every
// page under tour/src/content/docs skip them.
//
// A topic's page is named after its heading as a spec page is (`## Names
// and bindings` is names-and-bindings.md, served at
// /writing/names-and-bindings/), and its rules keep their numbered
// headings, one level up, so a rule's anchor is the one GitHub gives it in
// docs/style.md. A page's title is its topic.
//
// A bare `§N` in the guide names its rule N and becomes a link to the rule's
// heading. `spec §N`, with the forms the spec's own pages read (`spec §7
// *Calling Convention*`), names a section of docs/spec.md and links its page
// in the Specification section. A link to spec.md goes to the Specification
// section, a link into the guide (`#anchor`) to the page its heading landed
// on, and a link to another file in the repository to GitHub. A paragraph's
// lines are joined before the references are read, so a reference may wrap.

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// styleSite is the section's path on the site.
const styleSite = "/writing/"

// styleRepo is where a link from the guide to another file in the
// repository points.
const styleRepo = "https://github.com/nomi-language/nomi/blob/main/"

const styleGenerateCommand = "go run ./cmd/nomi-docgen -style docs/style.md tour/src/content/docs/writing"

type styleTopic struct {
	site  string // the section's path on the site: styleSite or toolingSite
	title string // the heading's text
	slug  string
	body  []specLine // the lines after the `## Topic` heading
	rules []*styleRule
}

type styleRule struct {
	num    int
	title  string // the heading's text after `N. `
	topic  *styleTopic
	anchor string // its id on its topic's page
}

// styleDoc is docs/style.md (or docs/tooling.md, which has no numbered
// rules) read into its preamble and topics, with the spec its references
// name.
type styleDoc struct {
	site     string     // the section's path on the site; styleSite when empty
	file     string     // the document's name in docs/; style.md when empty
	numbered bool       // whether its `### N. Rule` headings are rules
	preamble []specLine // after the `# ` title, before the first topic
	topics   []*styleTopic
	rules    map[int]*styleRule
	// docAnchors maps each heading's anchor in docs/style.md read as one
	// page (as GitHub shows it) to its topic and its anchor there.
	docAnchors map[string]styleTarget
	spec       *specDoc
	// unresolved lists each reference that names no rule or heading.
	unresolved []string
}

type styleTarget struct {
	topic  *styleTopic
	anchor string // "" for the page itself
}

// link answers a link to the target from topic from's page (nil for the
// index).
func (t styleTarget) link(from *styleTopic) string {
	if t.topic == from && t.anchor != "" {
		return "#" + t.anchor
	}
	site := t.topic.site
	if site == "" {
		site = styleSite
	}
	out := site + t.topic.slug + "/"
	if t.anchor != "" {
		out += "#" + t.anchor
	}
	return out
}

var (
	styleRuleHeading = regexp.MustCompile(`^### (\d+)\. (.+)$`)
	styleRuleRef     = regexp.MustCompile(`^\d+`)
)

// readStyle reads the guide at stylePath and the spec at specPath. The
// rules must be numbered 1, 2, 3, ... in the order they appear.
func readStyle(stylePath, specPath string) (*styleDoc, error) {
	return readGuide(stylePath, specPath, &styleDoc{site: styleSite, file: "style.md", numbered: true})
}

// readGuide reads the document at stylePath into doc, whose site, file and
// numbered say which document it is, with the spec at specPath.
func readGuide(stylePath, specPath string, doc *styleDoc) (*styleDoc, error) {
	spec, err := readSpec(specPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(stylePath)
	if err != nil {
		return nil, err
	}
	lines := fenceLines(strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"))
	doc.rules, doc.docAnchors, doc.spec = map[int]*styleRule{}, map[string]styleTarget{}, spec
	docSlugs := newSlugger()
	slugs := map[string]bool{}
	var cur *styleTopic
	var pageSlugs *slugger
	for _, l := range lines {
		if !l.fence {
			if m := specSubheading.FindStringSubmatch(l.text); m != nil {
				text := headingText(m[2])
				level := len(m[1])
				if level == 1 && cur == nil {
					continue // the document's title
				}
				docAnchor := docSlugs.slug(text)
				if level == 2 {
					cur = &styleTopic{site: doc.site, title: strings.TrimSpace(m[2]), slug: pageSlug(m[2])}
					if slugs[cur.slug] {
						return nil, fmt.Errorf("two topics would both be %s%s/", doc.site, cur.slug)
					}
					slugs[cur.slug] = true
					doc.topics = append(doc.topics, cur)
					doc.docAnchors[docAnchor] = styleTarget{topic: cur}
					pageSlugs = newSlugger()
					continue
				}
				if cur == nil {
					return nil, fmt.Errorf("%s: heading %q comes before the first `## Topic`", stylePath, l.text)
				}
				anchor := pageSlugs.slug(text)
				doc.docAnchors[docAnchor] = styleTarget{topic: cur, anchor: anchor}
				if r := styleRuleHeading.FindStringSubmatch(l.text); r != nil && doc.numbered {
					num, _ := strconv.Atoi(r[1])
					if num != len(doc.rules)+1 {
						return nil, fmt.Errorf("%s: rule %d follows rule %d; number the rules 1, 2, 3, ... in order", stylePath, num, len(doc.rules))
					}
					rule := &styleRule{num: num, title: strings.TrimSpace(r[2]), topic: cur, anchor: anchor}
					doc.rules[num] = rule
					cur.rules = append(cur.rules, rule)
				}
			}
		}
		if cur == nil {
			doc.preamble = append(doc.preamble, l)
			continue
		}
		cur.body = append(cur.body, l)
	}
	if len(doc.rules) == 0 && doc.numbered {
		return nil, fmt.Errorf("%s holds no `### N. Rule` sections", stylePath)
	}
	if len(doc.topics) == 0 {
		return nil, fmt.Errorf("%s holds no `## ` sections", stylePath)
	}
	return doc, nil
}

// joinParagraphs joins each paragraph's and list item's lines into one, so
// a reference that wraps reads as one. Code, headings, tables, quotes and
// HTML stay as they are.
func joinParagraphs(lines []specLine) []specLine {
	joinable := func(t string) bool {
		return t != "" && t != "---" && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "|") &&
			!strings.HasPrefix(t, ">") && !strings.HasPrefix(t, "<")
	}
	var out []specLine
	for _, l := range lines {
		t := strings.TrimSpace(l.text)
		if n := len(out); n > 0 && !l.fence && !out[n-1].fence && joinable(t) &&
			joinable(strings.TrimSpace(out[n-1].text)) && !specListItem.MatchString(t) {
			out[n-1].text += " " + t
			continue
		}
		out = append(out, l)
	}
	return out
}

// rewriteProse rewrites a line of prose on topic from's page (nil for the
// index): its links, its rule references and its spec references.
func (d *styleDoc) rewriteProse(line string, from *styleTopic) string {
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
			rest := line[i+len("§"):]
			if strings.HasSuffix(b.String(), "spec ") {
				if text, link, ok := d.spec.resolveRef(rest, nil); ok {
					fmt.Fprintf(&b, "[§%s](%s)", text, link)
					i += len("§") + len(text)
					continue
				}
			} else if num := styleRuleRef.FindString(rest); num != "" {
				n, _ := strconv.Atoi(num)
				if rule := d.rules[n]; rule != nil {
					fmt.Fprintf(&b, "[§%s](%s)", num, styleTarget{rule.topic, rule.anchor}.link(from))
					i += len("§") + len(num)
					continue
				}
				d.unresolved = append(d.unresolved, "§"+num+" (no such rule)")
			}
		}
		b.WriteByte(line[i])
		i++
	}
	return b.String()
}

// rewriteLinks points a link to the spec at the Specification section, a
// link into the document at the page its heading landed on, a link to the
// other generated document (style.md, tooling.md) at its section, and a
// link to another file in the repository at GitHub.
func (d *styleDoc) rewriteLinks(line string, from *styleTopic) string {
	site, self := d.site, d.file
	if site == "" {
		site, self = styleSite, "style.md"
	}
	return specMdLink.ReplaceAllStringFunc(line, func(m string) string {
		target := specMdLink.FindStringSubmatch(m)[1]
		file, anchor, _ := strings.Cut(target, "#")
		switch {
		case strings.Contains(target, "://"), strings.HasPrefix(target, "/"), strings.HasPrefix(target, "mailto:"):
			return m
		case file == "spec.md":
			if anchor == "" {
				return "](" + specSite + ")"
			}
			if t, ok := d.spec.docAnchors[anchor]; ok {
				return "](" + t.link(nil) + ")"
			}
		case file == "" || file == self:
			if anchor == "" {
				return "](" + site + ")"
			}
			if t, ok := d.docAnchors[anchor]; ok {
				return "](" + t.link(from) + ")"
			}
		case file == "install.md":
			out := toolingSite + installSlug + "/"
			if anchor != "" {
				out += "#" + anchor
			}
			return "](" + out + ")"
		case (file == "style.md" || file == "tooling.md") && anchor == "":
			if file == "style.md" {
				return "](" + styleSite + ")"
			}
			return "](" + toolingSite + ")"
		default:
			return "](" + styleRepo + path.Clean("docs/"+target) + ")"
		}
		d.unresolved = append(d.unresolved, target)
		return m
	})
}

// render writes lines for a page: paragraphs joined, prose rewritten, each
// heading one level up, and a trailing rule dropped.
func (d *styleDoc) render(lines []specLine, from *styleTopic) string {
	var b strings.Builder
	for _, l := range joinParagraphs(lines) {
		switch {
		case l.fence:
			b.WriteString(l.text)
		case strings.HasPrefix(l.text, "##") && specSubheading.MatchString(l.text):
			b.WriteString(l.text[1:])
		default:
			b.WriteString(d.rewriteProse(l.text, from))
		}
		b.WriteByte('\n')
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimSpace(strings.TrimSuffix(out, "---"))
	return out + "\n"
}

func (r *styleRule) label() string { return fmt.Sprintf("%d. %s", r.num, r.title) }

// stylePages answers every file the Writing Nomi section holds, by file
// name, and each reference that names no rule, section or heading.
func stylePages(stylePath, specPath string) (map[string]string, []string, error) {
	doc, err := readStyle(stylePath, specPath)
	if err != nil {
		return nil, nil, err
	}
	type item struct {
		Label string `json:"label"`
		Link  string `json:"link"`
	}
	pages := map[string]string{}
	sidebar := []item{{"Overview", styleSite}}
	var index strings.Builder
	index.WriteString("---\ntitle: Writing Nomi\n")
	index.WriteString("description: \"How to write idiomatic Nomi: conventions for layout, names, calls, types, patterns, failure, collections, tests and module APIs.\"\n---\n\n")
	fmt.Fprintf(&index, "<!-- Generated from docs/style.md by `%s`. Do not edit. -->\n\n", styleGenerateCommand)
	index.WriteString(doc.render(doc.preamble, nil))
	index.WriteString("\nThese pages are generated from [`docs/style.md`](" + styleRepo + "docs/style.md) in the repository.\n")
	for _, t := range doc.topics {
		var page strings.Builder
		fmt.Fprintf(&page, "---\ntitle: %s\n", yamlString(headingText(t.title)))
		if len(t.rules) > 0 {
			titles := make([]string, len(t.rules))
			for i, r := range t.rules {
				titles[i] = plainText(r.title)
			}
			fmt.Fprintf(&page, "description: %s\n", yamlString(fmt.Sprintf("Writing Nomi, rules %d to %d: %s.",
				t.rules[0].num, t.rules[len(t.rules)-1].num, strings.Join(titles, "; "))))
		}
		fmt.Fprintf(&page, "---\n\n<!-- Generated from docs/style.md by `%s`. Do not edit. -->\n\n", styleGenerateCommand)
		page.WriteString(doc.render(t.body, t))
		pages[t.slug+".md"] = page.String()
		sidebar = append(sidebar, item{headingText(t.title), styleSite + t.slug + "/"})
		fmt.Fprintf(&index, "\n## %s\n\n", t.title)
		for _, r := range t.rules {
			fmt.Fprintf(&index, "- [%s](%s)\n", r.label(), styleTarget{t, r.anchor}.link(nil))
		}
	}
	pages["index.md"] = index.String()
	side, err := json.MarshalIndent(sidebar, "", "\t")
	if err != nil {
		return nil, nil, err
	}
	pages[sidebarFile] = string(side) + "\n"
	return pages, append(doc.unresolved, doc.spec.unresolved...), nil
}

// writeStyle writes the Writing Nomi pages for the guide at stylePath,
// whose spec references name the spec.md beside it.
func writeStyle(stylePath, outDir string) error {
	specPath := filepath.Join(filepath.Dir(stylePath), "spec.md")
	pages, unresolved, err := stylePages(stylePath, specPath)
	if err != nil {
		return err
	}
	if len(unresolved) > 0 {
		// TestStyleReferencesResolve fails on these.
		fmt.Fprintf(os.Stderr, "nomi-docgen: %s: these references name no rule, section or heading: %s\n", stylePath, strings.Join(unresolved, "; "))
	}
	if err := writeGeneratedPages(outDir, pages); err != nil {
		return err
	}
	fmt.Printf("nomi-docgen: wrote %d Writing Nomi pages to %s\n", len(pages)-1, outDir)
	return nil
}

// writeGeneratedPages makes outDir hold exactly pages, by file name.
func writeGeneratedPages(outDir string, pages map[string]string) error {
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
	return nil
}
