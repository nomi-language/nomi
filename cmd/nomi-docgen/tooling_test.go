package main

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

var toolingPath = filepath.Join("..", "..", "docs", "tooling.md")

// TestToolingReferencesResolve generates the Tooling pages from
// docs/tooling.md and holds every link in them to a page and an anchor that
// the generated pages declare: each `spec §N` reference (against the
// Specification pages generated from docs/spec.md), each link into the
// document and the index's list of sections. A link to the standard
// library reference must name a std module.
func TestToolingReferencesResolve(t *testing.T) {
	pages, unresolved, err := toolingPages(toolingPath, specPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range unresolved {
		t.Errorf("docs/tooling.md: %s names no spec section, heading or bold lead-in, and no heading of the document", ref)
	}
	spec, _, err := specPages(specPath)
	if err != nil {
		t.Fatal(err)
	}
	anchors := map[string]map[string]bool{} // "/tooling/x/" or "/spec/x/" -> its ids
	for site, set := range map[string]map[string]string{toolingSite: pages, specSite: spec} {
		for name, text := range set {
			if !strings.HasSuffix(name, ".md") {
				continue
			}
			page := site + strings.TrimSuffix(name, ".md") + "/"
			if name == "index.md" {
				page = site
			}
			anchors[page] = pageAnchors(text)
		}
	}
	link := regexp.MustCompile(`\]\(((?:/tooling/|/spec/)[^)#]*|#[^)]*)(#[^)]*)?\)`)
	reference := regexp.MustCompile(`\]\(/reference/([a-z_]+)/`)
	names := make([]string, 0, len(pages))
	for name := range pages {
		names = append(names, name)
	}
	sort.Strings(names)
	checked := map[string]int{}
	for _, name := range names {
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		self := toolingSite + strings.TrimSuffix(name, ".md") + "/"
		if name == "index.md" {
			self = toolingSite
		}
		for _, l := range fenceLines(strings.Split(pages[name], "\n")) {
			if l.fence {
				continue
			}
			for _, m := range link.FindAllStringSubmatch(l.text, -1) {
				page, anchor := m[1], m[2]
				if strings.HasPrefix(m[1], "#") {
					page, anchor = self, m[1]
				}
				ids, ok := anchors[page]
				switch {
				case !ok:
					t.Errorf("tooling/%s links %s, which is no page", name, m[0])
				case anchor != "" && !ids[strings.TrimPrefix(anchor, "#")]:
					t.Errorf("tooling/%s links %s, whose page has no such anchor", name, m[0])
				}
				checked[strings.SplitN(strings.Trim(page, "/"), "/", 2)[0]]++
			}
			for _, m := range reference.FindAllStringSubmatch(l.text, -1) {
				if _, ok := std.ReadFile(m[1]); !ok {
					t.Errorf("tooling/%s links %s, and std has no module %s", name, m[0], m[1])
				}
			}
		}
	}
	// The index lists every section, the sections link each other, and
	// they cite the spec.
	if checked["tooling"] < 40 || checked["spec"] < 8 {
		t.Errorf("checked %d links into the document and %d into the spec; the pages hold more", checked["tooling"], checked["spec"])
	}
}

// TestToolingPagesSplitTheDocument holds the generator to its shape: a page
// per `##` section named after its heading, titled by it, in the sidebar
// and on the index, and the document's code kept whole.
func TestToolingPagesSplitTheDocument(t *testing.T) {
	pages, _, err := toolingPages(toolingPath, specPath)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := readGuide(toolingPath, specPath, &styleDoc{site: toolingSite, file: "tooling.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.topics) < 5 {
		t.Fatalf("docs/tooling.md holds %d sections", len(doc.topics))
	}
	fences := 0
	for _, topic := range doc.topics {
		page, ok := pages[topic.slug+".md"]
		if !ok {
			t.Errorf("no tooling/%s.md for `## %s`", topic.slug, topic.title)
			continue
		}
		if !strings.HasPrefix(page, "---\ntitle: "+yamlString(headingText(topic.title))+"\n") {
			t.Errorf("tooling/%s.md is not titled %q", topic.slug, topic.title)
		}
		if !strings.Contains(pages[sidebarFile], `"link": "`+toolingSite+topic.slug+`/"`) {
			t.Errorf("the sidebar does not list %s", topic.title)
		}
		if !strings.Contains(pages["index.md"], "("+toolingSite+topic.slug+"/)") {
			t.Errorf("the index does not list %s", topic.title)
		}
		for _, s := range toolingSections(topic) {
			if !strings.Contains(page, "\n## "+s.title+"\n") {
				t.Errorf("tooling/%s.md has no `## %s` heading", topic.slug, s.title)
			}
		}
		for _, l := range topic.body {
			if l.fence && strings.HasPrefix(strings.TrimSpace(l.text), "```") && strings.TrimSpace(l.text) != "```" {
				fences++
			}
		}
		fences -= strings.Count(page, "\n```") - strings.Count(page, "\n```\n")
	}
	if fences != 0 {
		t.Errorf("the pages hold %d fewer fenced blocks with a language than the document's sections", fences)
	}
}

// TestToolingPagesStartWithInstalling holds docs/install.md to its place as
// the section's first page: in the sidebar right after the overview, on the
// index, and with each of its sections.
func TestToolingPagesStartWithInstalling(t *testing.T) {
	pages, _, err := toolingPages(toolingPath, specPath)
	if err != nil {
		t.Fatal(err)
	}
	page, ok := pages[installSlug+".md"]
	if !ok {
		t.Fatalf("no tooling/%s.md", installSlug)
	}
	if !strings.HasPrefix(page, "---\ntitle: \"Installing Nomi\"\n") {
		t.Errorf("tooling/%s.md does not open with docs/install.md's title:\n%s", installSlug, page[:min(len(page), 200)])
	}
	for _, heading := range []string{"\n## From source\n", "\n## Windows\n"} {
		if !strings.Contains(page, heading) {
			t.Errorf("tooling/%s.md has no %q", installSlug, strings.TrimSpace(heading))
		}
	}
	side := pages[sidebarFile]
	if first, second := strings.Index(side, `"link": "`+toolingSite+`"`), strings.Index(side, `"link": "`+toolingSite+installSlug+`/"`); first < 0 || second < 0 || strings.Count(side[first:second], `"link"`) != 1 {
		t.Errorf("the sidebar does not list Installing Nomi right after the overview:\n%s", side)
	}
	if !strings.Contains(pages["index.md"], "("+toolingSite+installSlug+"/)") {
		t.Errorf("the index does not list Installing Nomi")
	}
}
