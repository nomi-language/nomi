package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/doctest"
	nomiformat "github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/std"
)

// siteURL is the origin the files written for models link to. They are read
// outside the site, so every link in them is absolute.
const siteURL = "https://nomi-lang.org"

// writeLLMs writes what -llms publishes, from the repository at root into its
// tour: the cheat sheet's page (src/content/docs/cheat-sheet.md), and, at the
// site root (public/), llms.txt, llms-full.txt and cheatsheet.md, the cheat
// sheet as Markdown to paste into a model's context. docs/cheatsheet.md is the
// cheat sheet's one source; its examples are ```nomi-run blocks with their
// expect blocks, which TestTourDoctests runs. The page keeps them, so a reader
// can edit and run each one, and it is committed, as the Examples pages are,
// because tour.expect records every page's blocks (TestCheatSheetPageIsCurrent
// fails while it is stale). The three files at the site root are gitignored
// and show the examples as plain ```nomi fences without their expect blocks.
func writeLLMs(root, tourDir string) error {
	files, err := renderLLMs(root, tourDir)
	if err != nil {
		return err
	}
	for rel, text := range files {
		path := filepath.Join(tourDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("nomi-docgen: wrote %d files for models to %s\n", len(files), tourDir)
	return nil
}

// renderLLMs answers each file writeLLMs writes, keyed by its path under the
// tour directory.
func renderLLMs(root, tourDir string) (map[string]string, error) {
	read := func(rel string) (string, error) {
		data, err := os.ReadFile(filepath.Join(root, rel))
		return string(data), err
	}
	cheat, err := read("docs/cheatsheet.md")
	if err != nil {
		return nil, err
	}
	spec, err := read("docs/spec.md")
	if err != nil {
		return nil, err
	}
	style, err := read("docs/style.md")
	if err != nil {
		return nil, err
	}
	config, err := os.ReadFile(filepath.Join(tourDir, "astro.config.mjs"))
	if err != nil {
		return nil, err
	}
	groups := llmsGroups(string(config), comingFrom, "Language Tour", "Libraries")
	for _, g := range groups {
		for i, item := range g.items {
			g.items[i].description = pageDescription(tourDir, item.link)
		}
	}

	plainCheat := plainDoc(cheat)
	return map[string]string{
		"src/content/docs/cheat-sheet.md": cheatSheetPage(cheat),
		"public/cheatsheet.md":            absoluteLinks(plainCheat),
		"public/llms.txt":                 llmsIndex(groups),
		"public/llms-full.txt":            llmsFull(plainCheat, spec, style),
	}, nil
}

var (
	expectComment = regexp.MustCompile(`(?s)\n<!-- expect\n.*?\n-->\n`)
	htmlComment   = regexp.MustCompile(`(?s)\n?<!--.*?-->\n?`)
	siteLink      = regexp.MustCompile(`\]\((/[^)]*)\)`)
)

// plainDoc is a Markdown source with its runnable blocks shown as plain
// ```nomi fences and their expect blocks left out.
func plainDoc(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "```nomi-run") {
			line = "```nomi"
		}
		out = append(out, line)
	}
	return expectComment.ReplaceAllString(strings.Join(out, "\n"), "\n")
}

// absoluteLinks points a document's site-relative links at siteURL.
func absoluteLinks(md string) string {
	return siteLink.ReplaceAllString(md, "]("+siteURL+"$1)")
}

// withoutComments drops a document's HTML comments, the spec's maintainer
// notes.
func withoutComments(md string) string {
	return htmlComment.ReplaceAllString(md, "\n")
}

// cheatSheetPage is the site's page for the cheat sheet, from its source: its
// title moves to the front matter, its links to the site become relative, and
// its ```nomi-run blocks and their expect blocks stay as they are.
func cheatSheetPage(plain string) string {
	title, body, _ := strings.Cut(plain, "\n")
	title = strings.TrimSpace(strings.TrimPrefix(title, "# "))
	body = strings.ReplaceAll(body, "]("+siteURL+"/", "](/")
	return fmt.Sprintf("---\ntitle: %s\ndescription: The rules of Nomi most often written wrong, each with a checked example.\n---\n%s", title, body)
}

type llmsLink struct {
	label, link, description string
}

type llmsGroup struct {
	label string
	items []llmsLink
}

var sidebarEntry = regexp.MustCompile(`\{ label: '([^']+)', link: '([^']+)' \}`)

// llmsGroups reads the named groups of the tour's sidebar from
// astro.config.mjs, in the order given: each group's `label: '...'` line,
// then its `{ label, link }` items up to the group's closing `],`.
func llmsGroups(config string, labels ...string) []llmsGroup {
	var groups []llmsGroup
	for _, label := range labels {
		start := strings.Index(config, "label: '"+label+"',")
		if start < 0 {
			continue
		}
		rest := config[start:]
		if end := strings.Index(rest, "],"); end >= 0 {
			rest = rest[:end]
		}
		g := llmsGroup{label: label}
		for _, m := range sidebarEntry.FindAllStringSubmatch(rest, -1) {
			g.items = append(g.items, llmsLink{label: m[1], link: m[2]})
		}
		groups = append(groups, g)
	}
	return groups
}

var descriptionLine = regexp.MustCompile(`(?m)^description: *"?(.*?)"?\s*$`)

// pageDescription is the front matter description of the tour page at link,
// or "" when it has none.
func pageDescription(tourDir, link string) string {
	slug := strings.Trim(link, "/")
	if slug == "" {
		slug = "index"
	}
	data, err := os.ReadFile(filepath.Join(tourDir, "src/content/docs", slug+".md"))
	if err != nil {
		return ""
	}
	front, _, _ := strings.Cut(strings.TrimPrefix(string(data), "---\n"), "\n---")
	if m := descriptionLine.FindStringSubmatch(front); m != nil {
		return strings.ReplaceAll(m[1], `\"`, `"`)
	}
	return ""
}

const llmsSummary = "Nomi is a statically typed, fully immutable, functional-first language. Calls are qualified (`String.trim(s)`, never `s.trim()`), a named struct is built by its name (`Point{x: 1}`) and other literals start with their shape's symbol (`.{x: 1}`, `%{\"a\" => 1}`), errors are `Maybe` and `Result` values with a postfix `?`, and tasks run inside `concurrent` blocks. It runs on a bytecode VM written in Go."

// comingFrom is the sidebar group of guides for someone who writes another
// language.
const comingFrom = "Coming from…"

// llmsIndex is llms.txt: the site's map for a model, in the llms.txt format
// (a title, a summary, then sections of links).
func llmsIndex(groups []llmsGroup) string {
	var b strings.Builder
	b.WriteString("# Nomi\n\n> " + llmsSummary + "\n\n")
	b.WriteString("Read the cheat sheet before writing Nomi: it lists the rules most often written wrong, each with an example the compiler checks. llms-full.txt holds the cheat sheet, the specification, Writing Nomi and every standard library module's API in one file.\n\n")
	b.WriteString("## Start here\n\n")
	fmt.Fprintf(&b, "- [Cheat sheet](%s/cheatsheet.md): the rules most often written wrong, with checked examples\n", siteURL)
	fmt.Fprintf(&b, "- [Full text](%s/llms-full.txt): the cheat sheet, the specification, Writing Nomi and the standard library in one file\n", siteURL)
	for _, g := range groups {
		if g.label != comingFrom {
			continue
		}
		for _, item := range g.items {
			// The sidebar names each guide by its language alone, under
			// the group's heading; out of the group it needs the phrase.
			item.label = "Coming from " + item.label
			writeLLMsItem(&b, item)
		}
	}
	for _, g := range groups {
		if g.label == comingFrom || len(g.items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n", g.label)
		for _, item := range g.items {
			if item.link == "/cheat-sheet/" {
				// Listed first, under Start here.
				continue
			}
			writeLLMsItem(&b, item)
		}
	}
	b.WriteString("\n## Reference\n\n")
	fmt.Fprintf(&b, "- [Specification](%s/spec/): what the language does, section by section\n", siteURL)
	fmt.Fprintf(&b, "- [Writing Nomi](%s/writing/): idiom and API design\n", siteURL)
	fmt.Fprintf(&b, "- [Standard library](%s/reference/): every module's API, generated from its doc comments\n", siteURL)
	fmt.Fprintf(&b, "- [Tooling](%s/tooling/): the `nomi` command, the editors and the language server\n", siteURL)
	b.WriteString("\n## Optional\n\n")
	fmt.Fprintf(&b, "- [Examples](%s/examples/): Exercism exercises solved in Nomi, with their tests\n", siteURL)
	return b.String()
}

func writeLLMsItem(b *strings.Builder, item llmsLink) {
	if item.link == "/cheat-sheet/" {
		return
	}
	fmt.Fprintf(b, "- [%s](%s%s)", item.label, siteURL, item.link)
	if item.description != "" {
		fmt.Fprintf(b, ": %s", item.description)
	}
	b.WriteString("\n")
}

// llmsFull is llms-full.txt: the cheat sheet, the specification, Writing
// Nomi and every standard library module's API, as one Markdown file.
func llmsFull(plainCheat, spec, style string) string {
	var b strings.Builder
	b.WriteString("# Nomi: the full text\n\n> " + llmsSummary + "\n\n")
	b.WriteString("This file holds, in order: the cheat sheet, the language specification, Writing Nomi (idiom), and the standard library's API. Generated from the Nomi repository's docs/ and std/.\n\n")
	for _, doc := range []string{plainCheat, spec, style} {
		b.WriteString(strings.TrimSpace(absoluteLinks(withoutComments(doc))))
		b.WriteString("\n\n")
	}
	b.WriteString(absoluteLinks(plainStdlib(std.Load())))
	return b.String()
}

// plainStdlib is every module's API for llms-full.txt: the reference
// pages' content as Markdown with no HTML, one `## std/<module>` section
// per module.
func plainStdlib(lib *std.StdLib) string {
	var b strings.Builder
	b.WriteString("# Standard library\n\n")
	b.WriteString("Every module's public API, from its doc comments. A module is imported as `import std::<module>`.\n\n")
	for _, name := range referenceCandidates(lib) {
		b.WriteString(plainModule(lib, name))
	}
	return b.String()
}

func plainModule(lib *std.StdLib, name string) string {
	fa := lib.Files[name]
	if fa == nil || fa.ModuleScope == nil {
		return ""
	}
	entries := sortedAPIEntries(fa, lib.Nodes[name])
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## std::%s\n\n", name)
	if doc := strings.TrimSpace(moduleDoc(name)); doc != "" {
		b.WriteString(demoteHeadings(doc, 2))
		b.WriteString("\n\n")
	}
	shared := sharedImplHeadings(name, entries)
	for _, group := range apiGroups(entries) {
		fmt.Fprintf(&b, "### %s\n\n", groupHeadingLabel(name, group.apiGroupKey))
		for _, entry := range group.Entries {
			if !entry.RendersAsGroupHeading() {
				fmt.Fprintf(&b, "#### %s\n\n", plainEntryHeading(name, entry, shared))
			}
			plainEntry(&b, entry)
		}
	}
	return b.String()
}

// plainEntryHeading is the label renderEntryHeading writes, without HTML.
func plainEntryHeading(moduleName string, entry apiEntry, shared map[string]bool) string {
	if label := implHeadingLabel(moduleName, entry); label != "" {
		if header := implHeader(entry); header != "" {
			if shared[label] {
				label += " " + entry.Impl.Interface.TypeString()
			}
			return label + " (" + header + ")"
		}
		return label
	}
	label := entry.Sym.Name
	if owner := entryHeadingOwner(moduleName, entry); owner != "" {
		label = owner + "." + label
	}
	if entry.Sym.ImplInterface != "" {
		label += " (impl " + entry.Sym.ImplInterface + ")"
	}
	return label
}

// plainEntry is renderAPIEntry's content without HTML: the declaration and
// its doc, its variants' docs, and its examples as ```nomi fences.
func plainEntry(b *strings.Builder, entry apiEntry) {
	s := entry.Sym
	body := hoverdoc.Render(s)
	if body == "" {
		return
	}
	b.WriteString(body)
	b.WriteString("\n\n")
	if variants := plainVariants(s); variants != "" {
		b.WriteString(variants)
	}
	for _, test := range attachedTestsOf(s.Node) {
		if text := nomiformat.RenderAttachedTestBody(test); text != "" {
			fmt.Fprintf(b, "```nomi\n%s\n```\n\n", strings.TrimRight(text, "\n"))
		}
	}
}

func plainVariants(s *analysis.Symbol) string {
	var b strings.Builder
	renderVariantDocs(&b, s.Node)
	return strings.Replace(b.String(), "<p class=\"nomi-ref-context\">Variants</p>", "Variants:", 1)
}

// demoteHeadings moves every Markdown heading outside a code fence down by
// levels, so a module's own `## ` sections sit under its `## std/<module>`.
func demoteHeadings(md string, levels int) string {
	lines := strings.Split(md, "\n")
	fence := false
	for i, line := range lines {
		if strings.HasPrefix(line, "```") {
			fence = !fence
			continue
		}
		if !fence && strings.HasPrefix(line, "#") {
			if hashes := len(line) - len(strings.TrimLeft(line, "#")); strings.HasPrefix(line[hashes:], " ") {
				lines[i] = strings.Repeat("#", levels) + line
			}
		}
	}
	return strings.Join(lines, "\n")
}

// cheatSheetBlocks counts the cheat sheet's runnable examples, so a test can
// tell a sheet whose examples were all dropped from one that has them.
func cheatSheetBlocks(src string) int {
	return len(doctest.ExtractBlocks(src, "nomi-run"))
}
