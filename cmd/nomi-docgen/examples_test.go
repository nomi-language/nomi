package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestExamplePagesAreCurrent holds the committed Examples pages to what
// `nomi-docgen -examples` writes from tests/exercism: a page per exercise,
// the intro and the sidebar, each byte for byte, and no other file.
func TestExamplePagesAreCurrent(t *testing.T) {
	want, err := examplePages(filepath.Join("..", "..", "tests", "exercism"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("..", "..", "tour", "src", "content", "docs", "examples")
	const fix = "go run ./cmd/nomi-docgen -examples tests/exercism tour/src/content/docs/examples"
	var names []string
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("examples/%s is missing; run %s", name, fix)
			continue
		}
		if string(got) != want[name] {
			t.Errorf("examples/%s is stale; run %s", name, fix)
		}
	}
	have, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range have {
		if _, ok := want[filepath.Base(path)]; !ok {
			t.Errorf("examples/%s is no exercise's page; run %s", filepath.Base(path), fix)
		}
	}
}

// TestExampleNotesNameAKnownSubject holds NOTES.md to a `Subject:` line
// under its title that names one of subjectOrder.
func TestExampleNotesNameAKnownSubject(t *testing.T) {
	const rest = "\nDo a thing.\n\nAdapted from Exercism problem-specifications (MIT), exercises/x.\n\n## Choices\n\n- One.\n"
	for _, tc := range []struct {
		notes string
		want  string // an error substring, or "" for the subject's slug below
		slug  string
	}{
		{"# X\n\nSubject: Strings & Text\n" + rest, "", "strings-and-text"},
		{"# X\n" + rest, "no `Subject: <subject>` line under the title", ""},
		{"# X\n\nSubject: Text\n" + rest, `unknown subject "Text"`, ""},
		{"# X\n\nSubject: Strings & Text\nRun: manual\n" + rest, "", "strings-and-text"},
		{"# X\n\nSubject: Strings & Text\nRun: later\n" + rest, "unknown `Run: later`", ""},
	} {
		var ex exercise
		err := parseNotes(&ex, tc.notes)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("parseNotes(%q) = %v", tc.notes, err)
		case tc.want == "" && ex.subject.slug != tc.slug:
			t.Errorf("parseNotes(%q) subject = %q, want %q", tc.notes, ex.subject.slug, tc.slug)
		case tc.want == "" && ex.description != "Do a thing.":
			t.Errorf("parseNotes(%q) description = %q", tc.notes, ex.description)
		case tc.want == "" && ex.manual != strings.Contains(tc.notes, "Run: manual"):
			t.Errorf("parseNotes(%q) manual = %v", tc.notes, ex.manual)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("parseNotes(%q) = %v, want an error containing %q", tc.notes, err, tc.want)
		}
	}
}

// TestExampleChaptersComeFromTheCode pins the chapter derivation on two
// solutions whose features are known.
func TestExampleChaptersComeFromTheCode(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want string
	}{
		{"pub fn two_fer(name: String = \"you\") -> String {\n    \"One for ${name}, one for me.\"\n}\n",
			"Functions & Lambdas, Scalars & Strings"},
		{"pub fn total(xs: List<Int>) -> Int {\n    xs |> Iter.map(&(&1 * 2)) |> Iter.sum()\n}\n",
			"Functions & Lambdas, Pipes, Iteration & Loops"},
		{"pub enum Size {\n    Small\n    Large\n}\n\nimpl Display for Size {\n    fn to_string(size: self) -> String {\n        case size {\n            .Small -> \"S\"\n            .Large -> \"L\"\n        }\n    }\n}\n",
			"Structs, Enums, Distinct Types, Pattern Matching, Interfaces & Dispatch"},
	} {
		var titles []string
		for _, c := range chaptersOf([]exerciseFile{{name: "x.nomi", text: tc.src}}) {
			titles = append(titles, c.title)
		}
		if got := strings.Join(titles, ", "); got != tc.want {
			t.Errorf("chapters of\n%s= %q, want %q", tc.src, got, tc.want)
		}
	}
}

// A Friction item is checked but never reaches a page: a known tag is
// accepted, `None.` is no item, an item that says it was fixed is refused
// (a fix deletes it), and so is an unknown tag.
func TestExampleFrictionIsCheckedNotPublished(t *testing.T) {
	if err := checkFriction([]string{"", "- [stdlib] No transpose.", "  Gleam has one.", ""}); err != nil {
		t.Fatal(err)
	}
	if err := checkFriction([]string{"", "None."}); err != nil {
		t.Fatalf("None. gave %v", err)
	}
	if err := checkFriction([]string{"- [verbose] Long. Fixed: now short."}); err == nil || !strings.Contains(err.Error(), "delete a fixed item") {
		t.Fatalf("a fixed item gave %v", err)
	}
	if err := checkFriction([]string{"- [vague] Hm."}); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("an unknown tag gave %v", err)
	}
	ex := exercise{title: "T", slug: "t", description: "D.", specPath: "exercises/t", choices: "- A choice."}
	if page := renderExample(ex); strings.Contains(page, "Rough edges") || strings.Contains(page, "rough") {
		t.Fatalf("a page mentions rough edges:\n%s", page)
	}
}
