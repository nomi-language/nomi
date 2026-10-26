package main

// The tour's Examples section: one page per Exercism exercise under
// tests/exercism, an intro page with a table of them, and the section's
// sidebar (_sidebar.json), which groups the pages by subject.
//
//	go run ./cmd/nomi-docgen -examples tests/exercism tour/src/content/docs/examples
//
// The pages are committed, since the tour's doctests and tour.expect read
// every page under tour/src/content/docs. TestExamplePagesAreCurrent fails
// while they differ from what this writes.

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
)

// exercise is one directory of tests/exercism.
type exercise struct {
	slug        string
	title       string
	subject     subject
	description string // NOTES.md's first paragraph, on one line
	specPath    string // `exercises/<slug>` in problem-specifications
	choices     string // the body of NOTES.md's `## Choices` section
	// roughEdges are NOTES.md's `## Friction` items, each rendered with
	// its tag in words (frictionLabels).
	roughEdges []string
	files      []exerciseFile
	chapters   []chapter
}

type exerciseFile struct {
	name string
	text string
}

// subject is the group an example is listed under, in the sidebar and in
// the intro's table. NOTES.md names it on a `Subject: <title>` line under
// its title.
type subject struct {
	title string
	slug  string // the intro's `?subject=` filter value
}

// The subjects, in sidebar order. Each exercise's NOTES.md names one.
var subjectOrder = []subject{
	{"Strings & Text", "strings-and-text"},
	{"Numbers & Math", "numbers-and-math"},
	{"Collections & Algorithms", "collections-and-algorithms"},
	{"Parsing & Validation", "parsing-and-validation"},
	{"Encoding & Ciphers", "encoding-and-ciphers"},
	{"Grids & Matrices", "grids-and-matrices"},
	{"Games & Simulation", "games-and-simulation"},
	{"Dates & Time", "dates-and-time"},
}

// chapter is a tour chapter an example leans on.
type chapter struct {
	title string
	link  string
}

// slug is the chapter's `?chapter=` filter value on the intro page.
func (c chapter) slug() string { return strings.Trim(c.link, "/") }

// The chapters an example's solution can lean on, in tour order.
var (
	chapterBindings   = chapter{"Bindings & Expressions", "/bindings-and-expressions/"}
	chapterFunctions  = chapter{"Functions & Lambdas", "/functions-and-lambdas/"}
	chapterPipes      = chapter{"Pipes", "/pipes/"}
	chapterScalars    = chapter{"Scalars & Strings", "/scalars-and-strings/"}
	chapterCollection = chapter{"Collections", "/collections/"}
	chapterIteration  = chapter{"Iteration & Loops", "/iteration-and-loops/"}
	chapterTypes      = chapter{"Structs, Enums, Distinct Types", "/structs-enums-distinct/"}
	chapterPatterns   = chapter{"Pattern Matching", "/pattern-matching/"}
	chapterGenerics   = chapter{"Generics", "/generics/"}
	chapterInterfaces = chapter{"Interfaces & Dispatch", "/interfaces-and-dispatch/"}
	chapterOrder      = []chapter{
		chapterBindings, chapterFunctions, chapterPipes, chapterScalars, chapterCollection, chapterIteration,
		chapterTypes, chapterPatterns, chapterGenerics, chapterInterfaces,
	}
)

const problemSpecsURL = "https://github.com/exercism/problem-specifications"

var adaptedLine = regexp.MustCompile(`^Adapted from Exercism problem-specifications \(MIT\), (exercises/[a-z0-9-]+)\.$`)

// sidebarFile holds the Examples group's sidebar items, which
// tour/astro.config.mjs imports. Starlight's docs loader reads only
// Markdown, so it is no page.
const sidebarFile = "_sidebar.json"

// examplePages answers every file the Examples section holds, by file name:
// `<slug>.md` per exercise under dir, `index.md`, and the sidebar.
func examplePages(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var exercises []exercise
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ex, err := readExercise(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		exercises = append(exercises, ex)
	}
	if len(exercises) == 0 {
		return nil, fmt.Errorf("%s holds no exercises", dir)
	}
	sort.Slice(exercises, func(i, j int) bool { return exercises[i].slug < exercises[j].slug })
	sidebar, err := renderExamplesSidebar(exercises)
	if err != nil {
		return nil, err
	}
	pages := map[string]string{"index.md": renderExamplesIndex(exercises), sidebarFile: sidebar}
	for _, ex := range exercises {
		pages[ex.slug+".md"] = renderExample(ex)
	}
	return pages, nil
}

func readExercise(dir string) (exercise, error) {
	ex := exercise{slug: filepath.Base(dir)}
	notes, err := os.ReadFile(filepath.Join(dir, "NOTES.md"))
	if err != nil {
		return ex, err
	}
	if err := parseNotes(&ex, string(notes)); err != nil {
		return ex, fmt.Errorf("%s/NOTES.md: %w", dir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ex, err
	}
	var solutions, tests, others []exerciseFile
	for _, e := range entries {
		if e.IsDir() {
			return ex, fmt.Errorf("%s: the generator does not take folders (%s)", dir, e.Name())
		}
		if e.Name() == "NOTES.md" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return ex, err
		}
		f := exerciseFile{name: e.Name(), text: string(data)}
		switch {
		case strings.HasSuffix(f.name, "_test.nomi"):
			tests = append(tests, f)
		case strings.HasSuffix(f.name, ".nomi"):
			solutions = append(solutions, f)
		default:
			others = append(others, f)
		}
	}
	if len(solutions) == 0 || len(tests) == 0 {
		return ex, fmt.Errorf("%s: want a solution and a _test.nomi file", dir)
	}
	ex.files = append(append(append(ex.files, solutions...), tests...), others...)
	ex.chapters = chaptersOf(solutions)
	return ex, nil
}

var subjectLine = regexp.MustCompile(`^Subject: (.+)$`)

// parseNotes reads a NOTES.md: its `# ` title, the `Subject:` line under
// it, its first paragraph, its "Adapted from" line, its `## Choices`
// section and its `## Friction` items. Any other section (Indexing)
// stays out of the tour.
func parseNotes(ex *exercise, notes string) error {
	lines := strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "# ") {
		return fmt.Errorf("the first line is not a `# ` title")
	}
	ex.title = strings.TrimSpace(strings.TrimPrefix(lines[0], "# "))
	i := 1
	skipBlank := func() {
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
	}
	skipBlank()
	var m []string
	if i < len(lines) {
		m = subjectLine.FindStringSubmatch(strings.TrimSpace(lines[i]))
	}
	if m == nil {
		return fmt.Errorf("no `Subject: <subject>` line under the title; the subjects are %s", subjectTitles())
	}
	s, ok := subjectNamed(m[1])
	if !ok {
		return fmt.Errorf("unknown subject %q; the subjects are %s (subjectOrder in cmd/nomi-docgen/examples.go)", m[1], subjectTitles())
	}
	ex.subject = s
	i++
	skipBlank()
	var para []string
	for ; i < len(lines) && strings.TrimSpace(lines[i]) != ""; i++ {
		para = append(para, strings.TrimSpace(lines[i]))
	}
	ex.description = strings.Join(para, " ")
	if ex.description == "" {
		return fmt.Errorf("no first paragraph")
	}
	var choices, friction []string
	section := ""
	for _, line := range lines[i:] {
		if m := adaptedLine.FindStringSubmatch(line); m != nil {
			ex.specPath = m[1]
		}
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimSpace(line)
			continue
		}
		switch section {
		case "## Choices":
			choices = append(choices, line)
		case "## Friction":
			friction = append(friction, line)
		}
	}
	edges, err := roughEdges(friction)
	if err != nil {
		return err
	}
	ex.roughEdges = edges
	// A Choices line may point at Friction, which the page calls Rough
	// edges.
	for k, line := range choices {
		if strings.Contains(line, " (see Friction)") && len(edges) == 0 {
			return fmt.Errorf("a Choices line says \"(see Friction)\", and Friction has no item")
		}
		choices[k] = strings.ReplaceAll(line, " (see Friction)", " (see [Rough edges](#rough-edges))")
	}
	if ex.specPath == "" {
		return fmt.Errorf("no `Adapted from Exercism problem-specifications (MIT), exercises/<slug>.` line")
	}
	ex.choices = strings.TrimSpace(strings.Join(choices, "\n"))
	if ex.choices == "" {
		return fmt.Errorf("no `## Choices` section")
	}
	return nil
}

// frictionLabels name a `## Friction` item's tag for a reader.
var frictionLabels = map[string]string{
	"stdlib":        "Not in the standard library",
	"verbose":       "Longer than it should be",
	"feature":       "A missing feature",
	"error-message": "A confusing error",
	"idiom":         "Two ways to write it",
	"bug":           "A bug",
}

var frictionItem = regexp.MustCompile(`^- \[([a-z-]+)\] (.*)$`)

// roughEdges renders a `## Friction` section's items for the page: each
// `- [tag] text` item, with its indented continuation lines, becomes
// `- **Label.** text`. `None.` is no item. Friction lists only what is
// still true, so an item that says `Fixed:` is refused: a fix deletes it.
func roughEdges(lines []string) ([]string, error) {
	var items [][]string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "- "):
			items = append(items, []string{line})
		case len(items) > 0:
			items[len(items)-1] = append(items[len(items)-1], line)
		case strings.TrimSpace(line) != "" && strings.TrimSpace(line) != "None.":
			return nil, fmt.Errorf("`## Friction` text outside a `- [tag]` item: %q", line)
		}
	}
	var out []string
	for _, item := range items {
		m := frictionItem.FindStringSubmatch(item[0])
		if m == nil {
			return nil, fmt.Errorf("a `## Friction` item does not start with `- [tag] `: %q", item[0])
		}
		label, ok := frictionLabels[m[1]]
		if !ok {
			return nil, fmt.Errorf("unknown `## Friction` tag [%s]", m[1])
		}
		text := strings.TrimRight(strings.Join(append([]string{m[2]}, item[1:]...), "\n"), "\n ")
		if strings.Contains(text, "Fixed:") {
			return nil, fmt.Errorf("a `## Friction` item says `Fixed:`; delete a fixed item instead: %q", item[0])
		}
		out = append(out, fmt.Sprintf("- **%s.** %s", label, text))
	}
	return out, nil
}

func subjectNamed(title string) (subject, bool) {
	for _, s := range subjectOrder {
		if s.title == title {
			return s, true
		}
	}
	return subject{}, false
}

func subjectTitles() string {
	var titles []string
	for _, s := range subjectOrder {
		titles = append(titles, s.title)
	}
	return strings.Join(titles, ", ")
}

// chaptersOf derives the tour chapters a solution leans on from its tokens.
// Testing and Modules & Imports are left out: every example has a test file
// that imports its solution.
func chaptersOf(files []exerciseFile) []chapter {
	uses := map[chapter]bool{}
	for _, f := range files {
		toks := significantTokens(lexer.Lex(f.text))
		at := func(i int) token.Token {
			if i < len(toks) {
				return toks[i]
			}
			return token.Token{Type: token.EOF}
		}
		for i, t := range toks {
			next := at(i + 1)
			switch t.Type {
			case token.CAPTURE:
				uses[chapterFunctions] = true
			case token.FN:
				// A declaration is `fn name(` or `fn name<`; anything else
				// is a lambda.
				if next.Type != token.IDENT || (at(i+2).Type != token.LPAREN && at(i+2).Type != token.LT) {
					uses[chapterFunctions] = true
				} else if at(i+2).Type == token.LT {
					uses[chapterGenerics] = true
				}
				if next.Type == token.IDENT && at(i+2).Type == token.LPAREN && hasDefault(toks[i+2:]) {
					uses[chapterFunctions] = true
				}
			case token.PIPE, token.THEN, token.TAP:
				uses[chapterPipes] = true
			case token.STRING_START, token.TRIPLE_STRING_START, token.CODEPOINT_LITERAL:
				uses[chapterScalars] = true
			case token.IF, token.ONCE:
				uses[chapterBindings] = true
			case token.AND, token.OR:
				uses[chapterScalars] = true
			case token.PERCENT:
				if adjacent(t, next) && (next.Type == token.LBRACE || next.Type == token.LBRACKET) {
					uses[chapterCollection] = true
				} else {
					uses[chapterScalars] = true // the remainder operator
				}
			case token.DOTDOT, token.DOTDOTEQ:
				// A spread (`[x, ..rest]`) or a range (`1..10`).
				uses[chapterCollection] = true
			case token.HASH:
				if adjacent(t, next) {
					switch next.Type {
					case token.LBRACKET:
						uses[chapterCollection] = true
					case token.TYPE_IDENT, token.DOT:
						uses[chapterTypes] = true
					}
				}
			case token.STRUCT, token.ENUM, token.TYPE:
				uses[chapterTypes] = true
				if next.Type == token.TYPE_IDENT && at(i+2).Type == token.LT {
					uses[chapterGenerics] = true
				}
			case token.CASE, token.TRY:
				uses[chapterPatterns] = true
			case token.INTERFACE:
				uses[chapterInterfaces] = true
			case token.IMPL:
				// `impl Iface for Type`; an inherent `impl Type` is not dispatch.
				for j := i + 1; j < len(toks) && toks[j].Type != token.LBRACE; j++ {
					if toks[j].Type == token.FOR {
						uses[chapterInterfaces] = true
						break
					}
				}
			case token.IDENT:
				if t.Lexeme == "derive" && t.Col == 1 {
					uses[chapterInterfaces] = true
				}
			case token.TYPE_IDENT:
				if next.Type != token.DOT {
					break
				}
				switch t.Lexeme {
				case "String", "Int", "Float", "Decimal", "Bool":
					uses[chapterScalars] = true
				case "List", "Map", "Set", "Vector":
					uses[chapterCollection] = true
				case "Iter":
					uses[chapterIteration] = true
				case "Maybe", "Result":
					uses[chapterPatterns] = true
				}
			}
		}
	}
	var out []chapter
	for _, c := range chapterOrder {
		if uses[c] {
			out = append(out, c)
		}
	}
	return out
}

// significantTokens drops comments and layout tokens, so a token's
// neighbours are the code around it.
func significantTokens(toks []token.Token) []token.Token {
	out := toks[:0:0]
	for _, t := range toks {
		switch t.Type {
		case token.COMMENT, token.DOC_COMMENT, token.TEST_PROMPT, token.NEWLINE, token.BLANK_LINE:
			continue
		}
		out = append(out, t)
	}
	return out
}

// hasDefault reports whether the parameter list toks opens gives a
// parameter a default value (`name: String = "you"`).
func hasDefault(toks []token.Token) bool {
	depth := 0
	for _, t := range toks {
		switch t.Type {
		case token.LPAREN:
			depth++
		case token.RPAREN:
			depth--
			if depth == 0 {
				return false
			}
		case token.EQ:
			if depth == 1 {
				return true
			}
		}
	}
	return false
}

// adjacent reports whether b starts where a ends, with no space between.
func adjacent(a, b token.Token) bool {
	return a.EndLine == b.Line && a.EndCol == b.Col
}

func renderExample(ex exercise) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s\ndescription: %s\n---\n\n", strconv.Quote(ex.title), strconv.Quote(ex.description))
	fmt.Fprintf(&b, "<!-- Generated by cmd/nomi-docgen from tests/exercism/%s. Edit the exercise, not this page. -->\n\n", ex.slug)
	fmt.Fprintf(&b, "%s\n\n", ex.description)
	// The subject shares the line, so the block's line, which keys its
	// tour.expect record, stays where it was.
	// So does the rough edges badge, which starts it and links down to the
	// section.
	rough := ""
	if len(ex.roughEdges) > 0 {
		rough = `<a class="nomi-rough-badge" href="#rough-edges">rough edges</a> `
	}
	fmt.Fprintf(&b, "%sAdapted from Exercism's [problem specification](%s/tree/main/%s) (MIT). Listed under [%s](/examples/?subject=%s).\n\n",
		rough, problemSpecsURL, ex.specPath, ex.subject.title, ex.subject.slug)
	b.WriteString("```nomi-run\n")
	for i, f := range ex.files {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "// FILE: %s\n", f.name)
		b.WriteString(strings.TrimRight(f.text, "\n"))
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")
	b.WriteString("## How it's written\n\n")
	b.WriteString(ex.choices)
	b.WriteString("\n")
	if len(ex.roughEdges) > 0 {
		b.WriteString("\n## Rough edges\n\n")
		b.WriteString("Where Nomi made this exercise harder than it should be. Each is an open question for the language, not a settled answer.\n\n")
		b.WriteString(strings.Join(ex.roughEdges, "\n"))
		b.WriteString("\n")
	}
	return b.String()
}

func renderExamplesIndex(exercises []exercise) string {
	var b strings.Builder
	b.WriteString(`---
title: Examples
description: Exercism exercises solved in Nomi, each with its tests, runnable in the page.
---

<!-- Generated by cmd/nomi-docgen from tests/exercism. Edit the exercises, not this page. -->

Each example is a small exercise solved in Nomi, with a test file that holds
every case the exercise defines. The editor opens on the solution, and the
test file is the next tab. Running the example runs the tests and shows the
report. Edit either file and the tests run again, so you can break a case and
see how the failure reads, or try another way to write the solution.

Every example leans on [Testing](/testing/) and
[Modules & Imports](/modules-and-imports/). The table lists the other
chapters each solution uses, and the sidebar groups the examples by subject.

The exercises come from
[exercism/problem-specifications](https://github.com/exercism/problem-specifications),
MIT licence, Copyright (c) 2014, 2019, 2021 Exercism.

`)
	// The filter, a select per kind, is hidden until the tour client
	// (tour-client.mjs's filterExamples) wires it up, so a page without
	// JavaScript shows the whole table and no dead controls. Each run of HTML has no blank line,
	// which would end the Markdown HTML block.
	usedSubjects := map[subject]bool{}
	usedChapters := map[chapter]bool{}
	for _, ex := range exercises {
		usedSubjects[ex.subject] = true
		for _, c := range ex.chapters {
			usedChapters[c] = true
		}
	}
	b.WriteString("<div class=\"nomi-examples-filter\" data-examples-filter hidden>\n")
	filterGroup := func(kind, label, all string, values [][2]string) {
		fmt.Fprintf(&b, "<label>%s <select data-filter=\"%s\">\n", label, kind)
		fmt.Fprintf(&b, "<option value=\"\">%s</option>\n", all)
		for _, v := range values {
			fmt.Fprintf(&b, "<option value=\"%s\">%s</option>\n", v[0], html.EscapeString(v[1]))
		}
		b.WriteString("</select></label>\n")
	}
	var subjects, chapters [][2]string
	for _, s := range subjectOrder {
		if usedSubjects[s] {
			subjects = append(subjects, [2]string{s.slug, s.title})
		}
	}
	for _, c := range chapterOrder {
		if usedChapters[c] {
			chapters = append(chapters, [2]string{c.slug(), c.title})
		}
	}
	filterGroup("subject", "Subject", "All subjects", subjects)
	filterGroup("chapter", "Chapter", "All chapters", chapters)
	b.WriteString("<p class=\"nomi-examples-count\" role=\"status\" data-examples-count></p>\n")
	b.WriteString("</div>\n\n")

	b.WriteString("<table class=\"nomi-examples\" data-examples>\n<thead>\n")
	b.WriteString("<tr><th scope=\"col\">Example</th><th scope=\"col\">Subject</th><th scope=\"col\">What it does</th><th scope=\"col\">Chapters</th></tr>\n")
	b.WriteString("</thead>\n<tbody>\n")
	for _, ex := range exercises {
		var links, slugs []string
		for _, c := range ex.chapters {
			links = append(links, fmt.Sprintf("<a href=\"%s\">%s</a>", c.link, html.EscapeString(c.title)))
			slugs = append(slugs, c.slug())
		}
		fmt.Fprintf(&b, "<tr data-subject=\"%s\" data-chapters=\"%s\">\n", ex.subject.slug, strings.Join(slugs, " "))
		// An example with open friction carries a badge that links to its
		// Rough edges section.
		rough := ""
		if len(ex.roughEdges) > 0 {
			rough = fmt.Sprintf(" <a class=\"nomi-rough-badge\" href=\"/examples/%s/#rough-edges\">rough edges</a>", ex.slug)
		}
		fmt.Fprintf(&b, "<td><a href=\"/examples/%s/\">%s</a>%s</td>\n", ex.slug, html.EscapeString(ex.title), rough)
		fmt.Fprintf(&b, "<td>%s</td>\n", html.EscapeString(ex.subject.title))
		fmt.Fprintf(&b, "<td>%s</td>\n", html.EscapeString(ex.description))
		fmt.Fprintf(&b, "<td>%s</td>\n", strings.Join(links, ", "))
		b.WriteString("</tr>\n")
	}
	b.WriteString("</tbody>\n</table>\n")
	return b.String()
}

// sidebarItem is one entry of a Starlight sidebar: a link, or a group of
// items.
type sidebarItem struct {
	Label     string        `json:"label"`
	Link      string        `json:"link,omitempty"`
	Collapsed bool          `json:"collapsed,omitempty"`
	Items     []sidebarItem `json:"items,omitempty"`
}

// renderExamplesSidebar answers the Examples group's items: the intro, then
// one collapsed group per subject in subjectOrder, each listing its
// exercises by slug. The pages stay at /examples/<slug>/.
func renderExamplesSidebar(exercises []exercise) (string, error) {
	items := []sidebarItem{{Label: "About the examples", Link: "/examples/"}}
	for _, s := range subjectOrder {
		group := sidebarItem{Label: s.title, Collapsed: true}
		for _, ex := range exercises {
			if ex.subject == s {
				group.Items = append(group.Items, sidebarItem{Label: ex.title, Link: "/examples/" + ex.slug + "/"})
			}
		}
		if len(group.Items) > 0 {
			items = append(items, group)
		}
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "\t")
	if err := enc.Encode(items); err != nil {
		return "", err
	}
	return b.String(), nil
}

// writeExamples writes the Examples files into outDir and removes any other
// file there, so a removed exercise loses its page.
func writeExamples(exercismDir, outDir string) error {
	pages, err := examplePages(exercismDir)
	if err != nil {
		return err
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
	fmt.Printf("nomi-docgen: wrote %d example pages to %s\n", len(pages)-2, outDir)
	return nil
}
