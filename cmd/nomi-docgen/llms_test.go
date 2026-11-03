package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// cheatSheetTokenBudget bounds the cheat sheet a model is given to paste
// into its context, counted as about four characters a token.
const cheatSheetTokenBudget = 6000

var leftoverHTML = regexp.MustCompile(`<(span|pre|p|section|div|ul|li|a)[ >]|<!--`)

// inlineCode is a Markdown code span, whose markup (std/html's examples) is
// code a model reads as code, not leftover HTML.
var inlineCode = regexp.MustCompile("``[^\n]*?``|`[^`\n]*`")

// TestLLMsFiles holds what -llms writes to its sources: llms.txt lists every
// page of the Coming from…, Language Tour and Libraries groups with its
// description; llms-full.txt holds the cheat sheet, the spec, Writing Nomi
// and every module that has a reference page, with no HTML and no
// site-relative link; and the cheat sheet keeps every example, as a plain
// fence, within its token budget.
func TestLLMsFiles(t *testing.T) {
	root := filepath.Join("..", "..")
	tour := filepath.Join(root, "tour")
	files, err := renderLLMs(root, tour)
	if err != nil {
		t.Fatal(err)
	}

	index := files["public/llms.txt"]
	if !strings.HasPrefix(index, "# Nomi\n\n> ") {
		t.Errorf("llms.txt does not open with a title and a summary:\n%.200s", index)
	}
	config, err := os.ReadFile(filepath.Join(tour, "astro.config.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	pages := 0
	guides := 0
	for _, g := range llmsGroups(string(config), comingFrom, "Language Tour", "Libraries") {
		for _, item := range g.items {
			pages++
			if item.link == "/cheat-sheet/" {
				// Listed once, as the raw cheatsheet.md, under Start here.
				continue
			}
			label := item.label
			if g.label == comingFrom {
				guides++
				label = "Coming from " + label
			}
			line := "- [" + label + "](" + siteURL + item.link + "): "
			if !strings.Contains(index, line) {
				t.Errorf("llms.txt has no line for %s with a description; want one starting %q", item.link, line)
			}
		}
	}
	if pages < 30 || guides == 0 {
		t.Errorf("read %d pages, %d of them guides, from the sidebar; the Coming from…, Language Tour and Libraries groups hold more", pages, guides)
	}

	full := files["public/llms-full.txt"]
	for _, heading := range []string{"# Nomi cheat sheet", "# Nomi Language Specification", "# Writing Idiomatic Nomi", "# Standard library"} {
		if !strings.Contains(full, "\n"+heading+"\n") {
			t.Errorf("llms-full.txt has no %q", heading)
		}
	}
	lib := std.Load()
	for _, name := range referenceCandidates(lib) {
		if plainModule(lib, name) != "" && !strings.Contains(full, "\n## std/"+name+"\n") {
			t.Errorf("llms-full.txt has no section for std/%s", name)
		}
	}

	src, err := os.ReadFile(filepath.Join(root, "docs", "cheatsheet.md"))
	if err != nil {
		t.Fatal(err)
	}
	cheat := files["public/cheatsheet.md"]
	for name, text := range map[string]string{"llms-full.txt": full, "cheatsheet.md": cheat, "llms.txt": index} {
		if m := leftoverHTML.FindString(inlineCode.ReplaceAllString(outsideFences(text), "")); m != "" {
			t.Errorf("%s holds HTML (%q); a model reads it as Markdown", name, m)
		}
		if strings.Contains(text, "](/") {
			t.Errorf("%s holds a site-relative link; it is read off the site", name)
		}
	}
	blocks := cheatSheetBlocks(string(src))
	if blocks == 0 {
		t.Fatal("docs/cheatsheet.md has no ```nomi-run examples")
	}
	if got := strings.Count(cheat, "```nomi\n"); got != blocks || strings.Contains(cheat, "nomi-run") {
		t.Errorf("cheatsheet.md shows %d plain ```nomi fences for the source's %d examples", got, blocks)
	}
	if tokens := len(cheat) / 4; tokens > cheatSheetTokenBudget {
		t.Errorf("cheatsheet.md is about %d tokens, over its budget of %d", tokens, cheatSheetTokenBudget)
	}
}

// outsideFences is md with its fenced code blocks left out.
func outsideFences(md string) string {
	var out []string
	fence := false
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "```") {
			fence = !fence
			continue
		}
		if !fence {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// TestCheatSheetPageIsCurrent holds the committed cheat sheet page to what
// -llms writes from docs/cheatsheet.md. The page is committed because
// tour.expect records its runnable blocks; regenerate it with `go run
// ./cmd/nomi-docgen -llms . tour` from the repository root.
func TestCheatSheetPageIsCurrent(t *testing.T) {
	root := filepath.Join("..", "..")
	files, err := renderLLMs(root, filepath.Join(root, "tour"))
	if err != nil {
		t.Fatal(err)
	}
	const rel = "src/content/docs/cheat-sheet.md"
	committed, err := os.ReadFile(filepath.Join(root, "tour", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read the committed page: %v", err)
	}
	if string(committed) != files[rel] {
		t.Errorf("tour/%s is stale; regenerate it with `go run ./cmd/nomi-docgen -llms . tour` from the repository root", rel)
	}
	if !strings.Contains(files[rel], "```nomi-run") || !strings.Contains(files[rel], "<!-- expect") {
		t.Errorf("the cheat sheet page has no runnable blocks with their expect blocks")
	}
}
