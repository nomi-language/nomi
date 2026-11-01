package main

// The site's Tooling section: docs/tooling.md as one page per `##` section,
// an index with a table row per page linking its `###` sections, and the section's
// sidebar (_sidebar.json), which tour/astro.config.mjs reads.
//
//	go run ./cmd/nomi-docgen -tooling docs/tooling.md tour/src/content/docs/tooling
//
// It is read and rendered as Writing Nomi is (style.go), without numbered
// rules: a page is named after its heading (`## Running tests` is
// running-tests.md, served at /tooling/running-tests/), its headings move
// one level up, `spec §N *Heading*` links the Specification page, a link
// into the document (`#anchor`) goes to the page its heading landed on, and
// a link to another file in the repository goes to GitHub. docs/install.md,
// beside it, is the section's first page, /tooling/install/, whole. The
// pages are generated and gitignored (scripts/build-tour-wasm.sh writes
// them).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// toolingSite is the section's path on the site.
const toolingSite = "/tooling/"

const toolingGenerateCommand = "go run ./cmd/nomi-docgen -tooling docs/tooling.md tour/src/content/docs/tooling"

// toolingPages answers every file the Tooling section holds, by file name,
// and each reference that names no section or heading.
func toolingPages(toolingPath, specPath string) (map[string]string, []string, error) {
	doc, err := readGuide(toolingPath, specPath, &styleDoc{site: toolingSite, file: "tooling.md"})
	if err != nil {
		return nil, nil, err
	}
	type item struct {
		Label string `json:"label"`
		Link  string `json:"link"`
	}
	install, err := installPage(filepath.Join(filepath.Dir(toolingPath), "install.md"), specPath)
	if err != nil {
		return nil, nil, err
	}
	pages := map[string]string{installSlug + ".md": install.page}
	sidebar := []item{{"Overview", toolingSite}, {install.title, toolingSite + installSlug + "/"}}
	var index strings.Builder
	index.WriteString("---\ntitle: Tooling\n")
	index.WriteString("description: \"The nomi command and its flags, nomi.toml, modules and dependencies, the test runner, builds, Go bindings, environment variables and editor support.\"\n---\n\n")
	fmt.Fprintf(&index, "<!-- Generated from docs/tooling.md by `%s`. Do not edit. -->\n\n", toolingGenerateCommand)
	index.WriteString(doc.render(doc.preamble, nil))
	index.WriteString("\nThese pages are generated from [`docs/tooling.md`](" + styleRepo + "docs/tooling.md) and [`docs/install.md`](" + styleRepo + "docs/install.md) in the repository.\n")
	index.WriteString("\n| Page | What it covers |\n|---|---|\n")
	toolingRow(&index, install.title, toolingSite+installSlug+"/", install.sections, "")
	for _, t := range doc.topics {
		sections := toolingSections(t)
		var page strings.Builder
		fmt.Fprintf(&page, "---\ntitle: %s\n", yamlString(headingText(t.title)))
		if len(sections) > 0 {
			titles := make([]string, len(sections))
			for i, s := range sections {
				titles[i] = plainText(s.title)
			}
			fmt.Fprintf(&page, "description: %s\n", yamlString("Nomi tooling: "+strings.Join(titles, "; ")+"."))
		}
		fmt.Fprintf(&page, "---\n\n<!-- Generated from docs/tooling.md by `%s`. Do not edit. -->\n\n", toolingGenerateCommand)
		page.WriteString(doc.render(t.body, t))
		pages[t.slug+".md"] = page.String()
		sidebar = append(sidebar, item{headingText(t.title), toolingSite + t.slug + "/"})
		links := make([]toolingSection, len(sections))
		for i, s := range sections {
			links[i] = toolingSection{s.title, styleTarget{t, s.anchor}.link(nil)}
		}
		toolingRow(&index, t.title, toolingSite+t.slug+"/", links, doc.rewriteProse(firstSentence(firstParagraph(t.body)), t))
	}
	pages["index.md"] = index.String()
	side, err := json.MarshalIndent(sidebar, "", "\t")
	if err != nil {
		return nil, nil, err
	}
	pages[sidebarFile] = string(side) + "\n"
	unresolved := append(doc.unresolved, doc.spec.unresolved...)
	return pages, append(unresolved, install.unresolved...), nil
}

// toolingRow writes the index's row for a page at link: its title, and its
// sections, each linked (a section's anchor here is its whole link), or
// for a page with none, lead, its first sentence.
func toolingRow(index *strings.Builder, title, link string, sections []toolingSection, lead string) {
	cells := make([]string, len(sections))
	for i, s := range sections {
		cells[i] = fmt.Sprintf("[%s](%s)", s.title, s.anchor)
	}
	cell := strings.Join(cells, ", ")
	if cell == "" {
		cell = lead
	}
	fmt.Fprintf(index, "| [%s](%s) | %s |\n", strings.ReplaceAll(title, "|", `\|`), link, strings.ReplaceAll(cell, "|", `\|`))
}

// installSlug is docs/install.md's page in the section.
const installSlug = "install"

type renderedInstall struct {
	title      string // the document's `# ` title
	page       string
	sections   []toolingSection // its `## ` headings, linked
	unresolved []string
}

// installPage renders docs/install.md as one page: its `## ` headings stay
// headings of the page, under the title its `# ` heading gives.
func installPage(installPath, specPath string) (renderedInstall, error) {
	data, err := os.ReadFile(installPath)
	if err != nil {
		return renderedInstall{}, err
	}
	title, _, _ := strings.Cut(string(data), "\n")
	title = strings.TrimSpace(strings.TrimPrefix(title, "# "))
	doc, err := readGuide(installPath, specPath, &styleDoc{site: toolingSite, file: "install.md"})
	if err != nil {
		return renderedInstall{}, err
	}
	var page strings.Builder
	fmt.Fprintf(&page, "---\ntitle: %s\n---\n\n<!-- Generated from docs/install.md by `%s`. Do not edit. -->\n\n", yamlString(title), toolingGenerateCommand)
	page.WriteString(doc.render(doc.preamble, nil))
	slugs := newSlugger()
	var sections []toolingSection
	for _, t := range doc.topics {
		fmt.Fprintf(&page, "\n## %s\n\n", t.title)
		page.WriteString(doc.render(t.body, nil))
		sections = append(sections, toolingSection{t.title, toolingSite + installSlug + "/#" + slugs.slug(headingText(t.title))})
	}
	return renderedInstall{title, page.String(), sections, doc.unresolved}, nil
}

type toolingSection struct {
	title  string // the `###` heading's text
	anchor string // its id on its page
}

// toolingSections answers topic t's `###` headings with their anchors on its
// page, which every heading below the page's title takes in turn.
func toolingSections(t *styleTopic) []toolingSection {
	slugs := newSlugger()
	var out []toolingSection
	for _, l := range t.body {
		if l.fence {
			continue
		}
		m := specSubheading.FindStringSubmatch(l.text)
		if m == nil || len(m[1]) < 3 {
			continue
		}
		anchor := slugs.slug(headingText(m[2]))
		if len(m[1]) == 3 {
			out = append(out, toolingSection{strings.TrimSpace(m[2]), anchor})
		}
	}
	return out
}

// writeTooling writes the Tooling pages for docs/tooling.md at toolingPath,
// whose spec references name the spec.md beside it.
func writeTooling(toolingPath, outDir string) error {
	specPath := filepath.Join(filepath.Dir(toolingPath), "spec.md")
	pages, unresolved, err := toolingPages(toolingPath, specPath)
	if err != nil {
		return err
	}
	if len(unresolved) > 0 {
		// TestToolingReferencesResolve fails on these.
		fmt.Fprintf(os.Stderr, "nomi-docgen: %s: these references name no section or heading: %s\n", toolingPath, strings.Join(unresolved, "; "))
	}
	if err := writeGeneratedPages(outDir, pages); err != nil {
		return err
	}
	fmt.Printf("nomi-docgen: wrote %d Tooling pages to %s\n", len(pages)-1, outDir)
	return nil
}
