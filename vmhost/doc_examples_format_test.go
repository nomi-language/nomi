package vmhost_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// TestDocExamplesAreFormatted holds every Nomi example a reader sees to the
// layout `nomi fmt` gives it, so an editor never shows one text and then,
// after the reader's first edit, reformats the parts they did not touch.
// It reads:
//
//   - every ```nomi and ```nomi-run block of the tour's chapters, the docs
//     under docs/, README.md and CONTRIBUTING.md, and the ```nomi fences in
//     std's `///` and `//#` doc comments, which the stdlib reference renders;
//     a block holding `// FILE: <name>` markers is checked per file, its
//     Nomi files only, as the tour's multi-file editor formats them;
//   - every std `//!` attached test, as the reference page shows it
//     (format.RenderAttachedTestBody) and as its editor formats an edit
//     (format.FormatAttachedTestBody).
//
// A block that is a fragment with `...` standing for omitted code is
// formatted with each `...` replaced by an identifier of the same width and
// then put back. A block that does not parse even so (a signature table, a
// list of variants, an `error` block whose error is a parse error) is
// skipped; the log counts each file's examples and skips.
//
// NOMI_FORMAT_DOC_EXAMPLES=1 writes the formatted text of every unformatted
// fenced example back into its source.
func TestDocExamplesAreFormatted(t *testing.T) {
	root := filepath.Clean("..")
	tally := &docTally{root: root, files: map[string]*docFileTally{}, edits: map[string][]docEdit{}}

	markdown := []string{"README.md", "CONTRIBUTING.md"}
	docs, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range docs {
		markdown = append(markdown, relTo(t, root, path))
	}
	tourDocs := filepath.Join(root, "tour", "src", "content", "docs")
	err = filepath.WalkDir(tourDocs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path == filepath.Join(tourDocs, "reference") {
			return fs.SkipDir // generated from std's doc comments, read below
		}
		if !d.IsDir() && (strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".mdx")) {
			markdown = append(markdown, relTo(t, root, path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk tour content: %v", err)
	}
	for _, rel := range markdown {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		tally.file(rel)
		for _, lang := range []string{"nomi", "nomi-run"} {
			for _, b := range doctest.ExtractBlocks(string(data), lang) {
				tally.checkBlock(t, rel, b.Line, b.Code, func(line string) string { return line })
			}
		}
	}

	var stdFiles []string
	err = filepath.WalkDir(filepath.Join(root, "std"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".nomi") {
			stdFiles = append(stdFiles, relTo(t, root, path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk std: %v", err)
	}
	for _, rel := range stdFiles {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		for _, doc := range docCommentRuns(src) {
			for _, b := range doctest.ExtractBlocks(doc.text, "nomi") {
				tally.checkBlock(t, rel, doc.line+b.Line, b.Code, doc.wrap)
			}
		}
		nodes, err := parser.Parse(lexer.Lex(src))
		if err != nil {
			t.Errorf("%s does not parse: %v", rel, err)
			continue
		}
		for _, n := range nodes {
			ast.Inspect(n, func(n ast.Node) bool {
				for _, test := range ast.AttachedTestsOf(n) {
					tally.checkPrompt(t, rel, test)
				}
				return true
			})
		}
	}

	if tally.checked < 1000 {
		t.Errorf("checked %d examples; the scan is not reading the repository", tally.checked)
	}
	tally.log(t)
	if os.Getenv("NOMI_FORMAT_DOC_EXAMPLES") == "1" {
		tally.write(t)
	}
}

type docTally struct {
	root    string
	checked int
	files   map[string]*docFileTally
	edits   map[string][]docEdit
}

type docFileTally struct {
	checked, unformatted int
	skips                map[string]int // reason -> count
}

// docEdit replaces count source lines from line (1-based) with lines.
type docEdit struct {
	line, count int
	lines       []string
}

func (d *docTally) file(name string) *docFileTally {
	f := d.files[name]
	if f == nil {
		f = &docFileTally{skips: map[string]int{}}
		d.files[name] = f
	}
	return f
}

func (d *docTally) log(t *testing.T) {
	var names []string
	for f := range d.files {
		names = append(names, f)
	}
	sort.Strings(names)
	unformatted, skipped := 0, 0
	for _, name := range names {
		f := d.files[name]
		var reasons []string
		for r, c := range f.skips {
			reasons = append(reasons, fmt.Sprintf("%d %s", c, r))
			skipped += c
		}
		sort.Strings(reasons)
		unformatted += f.unformatted
		line := fmt.Sprintf("%s: %d checked, %d unformatted", name, f.checked, f.unformatted)
		if len(reasons) > 0 {
			line += ", skipped " + strings.Join(reasons, "; ")
		}
		t.Log(line)
	}
	t.Logf("checked %d examples in %d files, %d unformatted, %d skipped", d.checked, len(names), unformatted, skipped)
}

func (d *docTally) write(t *testing.T) {
	for name, edits := range d.edits {
		path := filepath.Join(d.root, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		sort.Slice(edits, func(i, j int) bool { return edits[i].line > edits[j].line })
		for _, e := range edits {
			at := e.line - 1
			lines = append(lines[:at], append(append([]string(nil), e.lines...), lines[at+e.count:]...)...)
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d formatted examples into %s", len(edits), name)
	}
}

// docFileMarker is the tour client's FILE_MARKER.
var docFileMarker = regexp.MustCompile(`^\s*//\s*FILE:\s*([A-Za-z0-9_.\-/]+\.[A-Za-z0-9]+)\s*$`)

// checkBlock checks one fenced block whose fence is line of file; wrap turns
// a line of the block into the source line that holds it.
func (d *docTally) checkBlock(t *testing.T, file string, line int, code string, wrap func(string) string) {
	t.Helper()
	code, indent := dedent(code)
	inner := func(l string) string {
		if l == "" {
			return wrap("")
		}
		return wrap(indent + l)
	}
	lines := strings.Split(code, "\n")
	type part struct {
		name string
		line int // the fence-relative line of the part's first line
		body []string
	}
	var parts []*part
	for i, l := range lines {
		if m := docFileMarker.FindStringSubmatch(l); m != nil {
			parts = append(parts, &part{name: m[1], line: i + 2})
			continue
		}
		if len(parts) > 0 {
			parts[len(parts)-1].body = append(parts[len(parts)-1].body, l)
		}
	}
	if len(parts) == 0 {
		d.checkUnit(t, file, line+1, code, inner)
		return
	}
	for _, p := range parts {
		// A manifest, a catalog or a data file the program reads is not
		// Nomi, and the tour's editor formats only the Nomi files.
		if !strings.HasSuffix(p.name, ".nomi") {
			continue
		}
		d.checkUnit(t, file, line+p.line, strings.Join(p.body, "\n"), inner)
	}
}

// checkUnit checks one Nomi text whose first line is line of file, as the
// tour's editor formats it: format.Format, trailing newlines dropped.
func (d *docTally) checkUnit(t *testing.T, file string, line int, code string, wrap func(string) string) {
	t.Helper()
	code = strings.TrimRight(code, "\n")
	if strings.TrimSpace(code) == "" {
		return
	}
	out, err := format.Format(code)
	if err != nil && strings.Contains(code, "...") {
		if hole := elisionHole(code); hole != "" {
			if elided, err2 := format.Format(strings.ReplaceAll(code, "...", hole)); err2 == nil {
				out, err = strings.ReplaceAll(elided, hole, "..."), nil
			}
		}
	}
	f := d.file(file)
	if err != nil {
		f.skips["that do not parse (signatures, types, variants, partial code, parse-error examples)"]++
		return
	}
	d.checked++
	f.checked++
	out = strings.TrimRight(out, "\n")
	if out == code {
		return
	}
	f.unformatted++
	t.Errorf("%s:%d: this example is not formatted; `nomi fmt` writes it as:\n%s", file, line, lineDiff(code, out))
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		lines = append(lines, wrap(l))
	}
	d.edits[file] = append(d.edits[file], docEdit{line: line, count: strings.Count(code, "\n") + 1, lines: lines})
}

// checkPrompt checks one `//!` attached test as the reference page shows it
// and its editor formats it.
func (d *docTally) checkPrompt(t *testing.T, file string, test ast.AttachedTest) {
	t.Helper()
	shown := format.RenderAttachedTestBody(test)
	if shown == "" {
		return
	}
	f := d.file(file)
	out, err := format.FormatAttachedTestBody(shown)
	if err != nil {
		t.Errorf("%s:%d: the reference page shows this `//!` example, which its editor cannot format: %v\n%s", file, test.Line, err, shown)
		return
	}
	d.checked++
	f.checked++
	if out != shown {
		f.unformatted++
		t.Errorf("%s:%d: the reference editor reformats this `//!` example after an edit:\n%s", file, test.Line, lineDiff(shown, out))
	}
}

// elisionHole is an identifier as wide as `...` that code does not hold.
func elisionHole(code string) string {
	for _, c := range []string{"zzq", "qzz", "zqz", "qqz", "zqq"} {
		if !strings.Contains(code, c) {
			return c
		}
	}
	return ""
}

type docRun struct {
	line int    // the source line before the run's first line
	text string // the run's lines without their prefix
	wrap func(string) string
}

// docCommentRuns answers each run of `///` or `//#` lines of src with the
// prefix (and one space after it) taken off, as the reference renders them.
func docCommentRuns(src string) []docRun {
	var runs []docRun
	var cur *docRun
	var curPrefix string
	for i, l := range strings.Split(src, "\n") {
		trimmed := strings.TrimLeft(l, " \t")
		prefix := ""
		switch {
		case strings.HasPrefix(trimmed, "///"):
			prefix = "///"
		case strings.HasPrefix(trimmed, "//#"):
			prefix = "//#"
		}
		if prefix == "" || (cur != nil && prefix != curPrefix) {
			if cur != nil {
				runs = append(runs, *cur)
				cur = nil
			}
			if prefix == "" {
				continue
			}
		}
		text := strings.TrimPrefix(strings.TrimPrefix(trimmed, prefix), " ")
		if cur == nil {
			lead := l[:len(l)-len(trimmed)] + prefix
			cur = &docRun{line: i, text: text, wrap: func(line string) string {
				if line == "" {
					return lead
				}
				return lead + " " + line
			}}
			curPrefix = prefix
		} else {
			cur.text += "\n" + text
		}
	}
	if cur != nil {
		runs = append(runs, *cur)
	}
	return runs
}

// dedent drops the indentation every non-blank line of code shares, as a
// fence indented under a list item carries, and answers it.
func dedent(code string) (string, string) {
	lines := strings.Split(code, "\n")
	common := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " "))
		if common < 0 || n < common {
			common = n
		}
	}
	if common <= 0 {
		return code, ""
	}
	for i, l := range lines {
		if len(l) >= common {
			lines[i] = l[common:]
		} else {
			lines[i] = strings.TrimLeft(l, " ")
		}
	}
	return strings.Join(lines, "\n"), strings.Repeat(" ", common)
}

func relTo(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(rel)
}

// lineDiff is a line diff of a and b: `-` for a's lines, `+` for b's.
func lineDiff(a, b string) string {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	lcs := make([][]int, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out strings.Builder
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			fmt.Fprintf(&out, "  %s\n", x[i])
			i, j = i+1, j+1
		case j < len(y) && (i == len(x) || lcs[i][j+1] >= lcs[i+1][j]):
			fmt.Fprintf(&out, "+ %s\n", y[j])
			j++
		default:
			fmt.Fprintf(&out, "- %s\n", x[i])
			i++
		}
	}
	return out.String()
}
