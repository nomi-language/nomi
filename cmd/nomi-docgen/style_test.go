package main

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	stylePath = filepath.Join("..", "..", "docs", "style.md")
	specPath  = filepath.Join("..", "..", "docs", "spec.md")
)

// TestStyleReferencesResolve generates the Writing Nomi pages from
// docs/style.md and holds every link in them to a page and an anchor that
// the generated pages declare: each `§N` rule reference, each `spec §N`
// reference (against the Specification pages generated from
// docs/spec.md), each link into the guide, and the index's rule list.
func TestStyleReferencesResolve(t *testing.T) {
	pages, unresolved, err := stylePages(stylePath, specPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range unresolved {
		t.Errorf("docs/style.md: %s names no rule, spec section, heading or bold lead-in", ref)
	}
	spec, _, err := specPages(specPath)
	if err != nil {
		t.Fatal(err)
	}
	anchors := map[string]map[string]bool{} // "/writing/x/" or "/spec/x/" -> its ids
	for site, set := range map[string]map[string]string{styleSite: pages, specSite: spec} {
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
	link := regexp.MustCompile(`\]\(((?:/writing/|/spec/)[^)#]*|#[^)]*)(#[^)]*)?\)`)
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
		self := styleSite + strings.TrimSuffix(name, ".md") + "/"
		if name == "index.md" {
			self = styleSite
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
					t.Errorf("writing/%s links %s, which is no page", name, m[0])
				case anchor != "" && !ids[strings.TrimPrefix(anchor, "#")]:
					t.Errorf("writing/%s links %s, whose page has no such anchor", name, m[0])
				}
				checked[strings.SplitN(strings.Trim(page, "/"), "/", 2)[0]]++
			}
		}
	}
	// The index lists every rule, the rules cite each other, and they
	// cite the spec.
	if checked["writing"] < 45 || checked["spec"] < 8 {
		t.Errorf("checked %d links into the guide and %d into the spec; the pages hold more", checked["writing"], checked["spec"])
	}
}

// TestStylePagesSplitTheGuide holds the generator to its shape: a page per
// topic named after its heading, its rules one level up with their
// numbers, every rule on the index and every topic in the sidebar, and the
// guide's code kept whole.
func TestStylePagesSplitTheGuide(t *testing.T) {
	pages, _, err := stylePages(stylePath, specPath)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := readStyle(stylePath, specPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.topics) < 2 {
		t.Fatalf("docs/style.md holds %d topics", len(doc.topics))
	}
	want := 0
	for _, topic := range doc.topics {
		page, ok := pages[topic.slug+".md"]
		if !ok {
			t.Errorf("no writing/%s.md for `## %s`", topic.slug, topic.title)
			continue
		}
		if !strings.HasPrefix(page, "---\ntitle: "+yamlString(headingText(topic.title))+"\n") {
			t.Errorf("writing/%s.md is not titled %q", topic.slug, topic.title)
		}
		for _, r := range topic.rules {
			if !strings.Contains(page, "\n## "+r.label()+"\n") {
				t.Errorf("writing/%s.md has no `## %s` heading", topic.slug, r.label())
			}
			if !strings.Contains(pages["index.md"], "- ["+r.label()+"]("+styleSite+topic.slug+"/#"+r.anchor+")") {
				t.Errorf("the index does not list rule %d", r.num)
			}
		}
		if !strings.Contains(pages[sidebarFile], `"link": "`+styleSite+topic.slug+`/"`) {
			t.Errorf("the sidebar does not list %s", topic.title)
		}
		for _, l := range topic.body {
			if l.fence && strings.TrimSpace(l.text) == "```nomi" {
				want++
			}
		}
		want -= strings.Count(page, "```nomi\n")
	}
	if want != 0 {
		t.Errorf("the pages hold %d fewer ```nomi blocks than the guide's topics", want)
	}
}

func TestStyleReferences(t *testing.T) {
	spec := &specDoc{byNum: map[int]*specSection{}}
	seven := &specSection{num: 7, title: "Types", slug: "types",
		headings: []specHeading{{3, "Calling Convention", "calling-convention"}}}
	spec.byNum[7] = seven
	layout := &styleTopic{title: "Layout", slug: "layout"}
	failure := &styleTopic{title: "Failure", slug: "failure"}
	doc := &styleDoc{spec: spec, rules: map[int]*styleRule{
		1:  {num: 1, title: "Pipe nested calls", topic: layout, anchor: "1-pipe-nested-calls"},
		27: {num: 27, title: "Pick the tool", topic: failure, anchor: "27-pick-the-tool"},
	}, docAnchors: map[string]styleTarget{"27-pick-the-tool": {failure, "27-pick-the-tool"}}}
	for _, tc := range []struct{ in, want string }{
		{"see §27.", "see [§27](/writing/failure/#27-pick-the-tool)."},
		{"(§1)", "([§1](#1-pipe-nested-calls))"},
		{"(spec §7 *Calling Convention*)", "(spec [§7 *Calling Convention*](/spec/types/#calling-convention))"},
		{"spec §7.", "spec [§7](/spec/types/)."},
		{"`§27` stays", "`§27` stays"},
		{"[the spec](spec.md)", "[the spec](/spec/)"},
		{"[rule 27](#27-pick-the-tool)", "[rule 27](/writing/failure/#27-pick-the-tool)"},
		{"[tests](../tests/README.md)", "[tests](https://github.com/nomi-language/nomi/blob/main/tests/README.md)"},
	} {
		if got := doc.rewriteProse(tc.in, layout); got != tc.want {
			t.Errorf("rewriteProse(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if len(doc.unresolved) != 0 {
		t.Errorf("unresolved: %v", doc.unresolved)
	}
	doc.rewriteProse("§99", layout)
	if len(doc.unresolved) != 1 {
		t.Errorf("a reference to no rule is not recorded: %v", doc.unresolved)
	}
	joined := joinParagraphs(fenceLines(strings.Split("see (spec §7 *Calling\nConvention*)\n- a\n  b\n\n```nomi\nx\ny\n```", "\n")))
	var texts []string
	for _, l := range joined {
		texts = append(texts, l.text)
	}
	if got, want := strings.Join(texts, "|"), "see (spec §7 *Calling Convention*)|- a b||```nomi|x|y|```"; got != want {
		t.Errorf("joinParagraphs = %q, want %q", got, want)
	}
}
