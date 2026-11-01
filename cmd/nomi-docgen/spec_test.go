package main

import (
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// knownStaleSpecRefs are references in docs/spec.md that name no heading
// or lead-in of their section, each with what it should name. The pages
// link the section alone. Fixing one in the spec removes its entry.
var knownStaleSpecRefs = map[string]string{}

// TestSpecReferencesResolve generates the Specification pages from
// docs/spec.md and holds every reference in them to a page and an anchor
// the pages declare: each `§N` and `§N *Heading*` reference, and each link
// into the document. The anchors are read back from the pages as Astro
// gives them (each heading's slug, counted per page, and each `<a id>`).
func TestSpecReferencesResolve(t *testing.T) {
	pages, unresolved, err := specPages(filepath.Join("..", "..", "docs", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range unresolved {
		if _, known := knownStaleSpecRefs[ref]; !known {
			t.Errorf("docs/spec.md: %s names no section, heading or bold lead-in there", ref)
		}
	}
	for ref := range knownStaleSpecRefs {
		if !slices.Contains(unresolved, ref) {
			t.Errorf("knownStaleSpecRefs lists %s, which resolves now; remove it", ref)
		}
	}

	anchors := map[string]map[string]bool{} // page slug -> its ids
	for name, text := range pages {
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		slug := strings.TrimSuffix(name, ".md")
		if slug == "index" {
			slug = ""
		}
		anchors[slug] = pageAnchors(text)
	}
	link := regexp.MustCompile(`\]\((/spec/[^)#]*|#[^)]*)(#[^)]*)?\)`)
	checked := 0
	names := make([]string, 0, len(pages))
	for name := range pages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		self := strings.TrimSuffix(name, ".md")
		for _, l := range fenceLines(strings.Split(pages[name], "\n")) {
			if l.fence {
				continue
			}
			for _, m := range link.FindAllStringSubmatch(l.text, -1) {
				page, anchor := self, m[2]
				if strings.HasPrefix(m[1], "#") {
					anchor = m[1]
				} else {
					page = strings.Trim(strings.TrimPrefix(m[1], "/spec/"), "/")
				}
				ids, ok := anchors[page]
				switch {
				case !ok:
					t.Errorf("spec/%s links %s, which is no page", name, m[0])
				case anchor != "" && !ids[strings.TrimPrefix(anchor, "#")]:
					t.Errorf("spec/%s links %s, whose page has no such anchor", name, m[0])
				}
				checked++
			}
		}
	}
	if checked < 200 {
		t.Errorf("checked %d links; the pages should hold far more", checked)
	}
}

// pageAnchors answers the ids a generated page's headings and anchors get.
func pageAnchors(page string) map[string]bool {
	ids := map[string]bool{}
	s := newSlugger()
	id := regexp.MustCompile(`<a id="([^"]+)"></a>`)
	for _, l := range fenceLines(strings.Split(page, "\n")) {
		if l.fence {
			continue
		}
		if m := specSubheading.FindStringSubmatch(l.text); m != nil && len(m[1]) >= 2 {
			ids[s.slug(headingText(m[2]))] = true
		}
		for _, m := range id.FindAllStringSubmatch(l.text, -1) {
			ids[m[1]] = true
		}
	}
	return ids
}

// TestSpecPagesSplitTheSpec holds the generator to its shape: a page per
// numbered section named after its heading, with its number in its title,
// subheadings one level up, and the spec's code kept whole.
func TestSpecPagesSplitTheSpec(t *testing.T) {
	pages, _, err := specPages(filepath.Join("..", "..", "docs", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	page, ok := pages["interfaces-implementations-and-derive.md"]
	if !ok {
		t.Fatal("no spec/interfaces-implementations-and-derive.md for `## 12. Interfaces, implementations and derive`")
	}
	if !strings.HasPrefix(page, "---\ntitle: \"12. Interfaces, implementations and derive\"\n") {
		t.Errorf("the page's frontmatter does not title it `12. Interfaces, implementations and derive`:\n%s", page[:min(len(page), 200)])
	}
	if strings.Contains(page, "\n### Implementations") || !strings.Contains(page, "\n## ") {
		t.Errorf("the page's subheadings are not one level up")
	}
	if !strings.Contains(pages["index.md"], "| 12 | [Interfaces, implementations and derive](/spec/interfaces-implementations-and-derive/) |") {
		t.Errorf("the index does not list chapter 12")
	}
	if !strings.Contains(pages[sidebarFile], `"link": "/spec/interfaces-implementations-and-derive/"`) {
		t.Errorf("the sidebar does not list chapter 12")
	}
	spec := 0
	for name, text := range pages {
		if strings.HasSuffix(name, ".md") && name != "index.md" {
			spec += strings.Count(text, "```nomi\n")
		}
	}
	doc, err := readSpec(filepath.Join("..", "..", "docs", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, s := range doc.sections {
		for _, l := range s.body {
			if l.fence && strings.TrimSpace(l.text) == "```nomi" {
				want++
			}
		}
	}
	if spec != want || want == 0 {
		t.Errorf("the pages hold %d ```nomi blocks; the spec's sections hold %d", spec, want)
	}
}

func TestSpecSlugs(t *testing.T) {
	for _, tc := range []struct{ heading, page, anchor string }{
		{"Enums (Algebraic Data Types)", "enums-algebraic-data-types", "enums-algebraic-data-types"},
		{"Module & Dependency Management", "module-dependency-management", "module--dependency-management"},
		{"38.1 `derive` — Compile-time impl synthesis", "38-1-derive-compile-time-impl-synthesis", "381-derive--compile-time-impl-synthesis"},
		{"Unwrapping with `!`", "unwrapping-with", "unwrapping-with-"},
		{"Bindings with `else`", "bindings-with-else", "bindings-with-else"},
		{"If/Else", "if-else", "ifelse"},
	} {
		if got := pageSlug(tc.heading); got != tc.page {
			t.Errorf("pageSlug(%q) = %q, want %q", tc.heading, got, tc.page)
		}
		if got := plainSlug(tc.heading); got != tc.anchor {
			t.Errorf("anchor of %q = %q, want %q", tc.heading, got, tc.anchor)
		}
	}
	s := newSlugger()
	for _, want := range []string{"maybe", "maybe-1", "maybe-2"} {
		if got := s.slug("Maybe"); got != want {
			t.Errorf("a repeated heading's anchor is %q, want %q", got, want)
		}
	}
}

func TestSpecSectionReferences(t *testing.T) {
	doc := &specDoc{byNum: map[int]*specSection{}}
	five := &specSection{num: 5, title: "Functions", slug: "functions",
		headings: []specHeading{{3, "Default Arguments", "default-arguments"}, {3, "Unreachable Code: unreachable", "unreachable-code-unreachable"}}}
	thirtyEight := &specSection{num: 38, title: "Derive", slug: "derive",
		headings: []specHeading{{3, "38.1 derive — Compile-time impl synthesis", "381-derive--compile-time-impl-synthesis"}}}
	doc.byNum[5], doc.byNum[38] = five, thirtyEight
	for _, tc := range []struct{ in, want string }{
		{"see §5.", "see [§5](/spec/functions/)."},
		{"(§5, *Default Arguments*)", "([§5, *Default Arguments*](/spec/functions/#default-arguments))"},
		{"§5 *Unreachable Code*", "[§5 *Unreachable Code*](/spec/functions/#unreachable-code-unreachable)"},
		{"§5's *Default Arguments*", "[§5's *Default Arguments*](/spec/functions/#default-arguments)"},
		{"(see §38.1)", "(see [§38.1](#381-derive--compile-time-impl-synthesis))"},
		{"`§5` stays", "`§5` stays"},
		{"[§5](/x) stays", "[§5](/x) stays"},
		{"§5 *Nothing Here*", "[§5](/spec/functions/) *Nothing Here*"},
	} {
		if got := doc.rewriteProse(tc.in, thirtyEight); got != tc.want {
			t.Errorf("rewriteProse(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := doc.rewriteProse("(§5, *Default Arguments*)", five); got != "([§5, *Default Arguments*](#default-arguments))" {
		t.Errorf("a reference into its own page = %q, want an anchor alone", got)
	}
	if got := doc.rewriteLinks("[notes](implementation-notes.md#go-ffi-wrapper)", five); got != "[notes](https://github.com/nomi-language/nomi/blob/main/docs/implementation-notes.md#go-ffi-wrapper)" {
		t.Errorf("a link to another document = %q, want its GitHub file", got)
	}
}
