package vmhost_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// lessonNotesFile is the First Steps runner's table of plain notes, which
// the page fetches and shows under a compile error after Check
// (tour-client.mjs, lessonNote).
const lessonNotesFile = "../tour/public/lesson-notes.json"

type lessonNote struct {
	Match   string `json:"match"`
	Note    string `json:"note"`
	Example string `json:"example"`
	Page    bool   `json:"page"`
	Link    *struct {
		Href  string `json:"href"`
		Label string `json:"label"`
	} `json:"link"`
}

// TestLessonNotesMatchTheirErrors holds each note to the compiler: its
// example, compiled as the page compiles a lesson (vmhost.LoadMarked), fails,
// and its first error is answered by this note before any other in the
// table, as the page picks one. A compiler message reworded so its note no
// longer finds it fails here, not silently on the page. A note's link names
// a page the tour has, and its `$N` a group its match has. A note marked
// "page" answers a message the page writes itself, such as a run that times
// out: it has no example, and the page's client holds a string literal its
// match finds.
func TestLessonNotesMatchTheirErrors(t *testing.T) {
	data, err := os.ReadFile(lessonNotesFile)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Notes []lessonNote `json:"notes"`
	}
	if err := json.Unmarshal(data, &table); err != nil {
		t.Fatalf("%s: %v", lessonNotesFile, err)
	}
	if len(table.Notes) == 0 {
		t.Fatalf("%s holds no notes", lessonNotesFile)
	}
	matches := make([]*regexp.Regexp, len(table.Notes))
	for i, n := range table.Notes {
		re, err := regexp.Compile(n.Match)
		if err != nil {
			t.Fatalf("note %d's match %q: %v", i, n.Match, err)
		}
		matches[i] = re
	}
	group := regexp.MustCompile(`\$(\d)`)
	for i, n := range table.Notes {
		t.Run(n.Match, func(t *testing.T) {
			for _, g := range group.FindAllStringSubmatch(n.Note, -1) {
				if int(g[1][0]-'0') > matches[i].NumSubexp() {
					t.Errorf("the note names %s, and its match has %d groups", g[0], matches[i].NumSubexp())
				}
			}
			if n.Link != nil {
				page := filepath.Join("../tour/src/content/docs", strings.Trim(n.Link.Href, "/")+".md")
				if _, err := os.Stat(page); err != nil {
					t.Errorf("the note links %s, and the tour has no %s", n.Link.Href, page)
				}
			}
			if n.Page {
				if n.Example != "" {
					t.Fatalf("a note for the page's own message has no example")
				}
				client, err := os.ReadFile("../tour/src/lib/tour-client.mjs")
				if err != nil {
					t.Fatal(err)
				}
				for _, lit := range regexp.MustCompile(`"[^"\n]*"`).FindAllString(string(client), -1) {
					if matches[i].MatchString(strings.Trim(lit, `"`)) {
						return
					}
				}
				t.Fatalf("tour-client.mjs writes no message the note's match finds")
			}
			_, _, err := vmhost.LoadMarked(n.Example, t.TempDir())
			if err == nil {
				t.Fatalf("the example compiles:\n%s", n.Example)
			}
			first := firstLessonError(err.Error())
			for j, re := range matches {
				if re.MatchString(first) {
					if j != i {
						t.Fatalf("the example's first error is answered by note %d (%q), which comes first:\n%s", j, table.Notes[j].Match, first)
					}
					return
				}
			}
			t.Fatalf("no note finds the example's first error, its own included:\n%s", first)
		})
	}
}

// firstLessonError is the first diagnostic of a compile error's text, as
// the page reads it: its first line, `main.nomi:2:9: message`, and the
// `help:` and `note:` lines that follow it.
func firstLessonError(text string) string {
	lines := strings.Split(text, "\n")
	n := 1
	for n < len(lines) && lessonErrorDetail.MatchString(lines[n]) {
		n++
	}
	return strings.Join(lines[:n], "\n")
}

var lessonErrorDetail = regexp.MustCompile(`^\S+:\d+:\d+: (help|note): `)
