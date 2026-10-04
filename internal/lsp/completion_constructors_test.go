package lsp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestCompletion_ConstructorsOfTheExpectedType(t *testing.T) {
	duration := "import std/duration.Duration\n\n"
	tests := []struct {
		name    string
		src     string
		label   string
		text    string
		exclude []string
	}{
		{
			"annotated binding offers the typed literal, not a Result constructor",
			"import std/calendar.Date\n\nfn f(): Int {\n    d: Date = " + cursorMark + "\n    1\n}\n",
			`Date"2026-05-04"`, `Date"${1:2026}-${2:05}-${3:04}"`,
			[]string{"Date.parse", "Date.new"},
		},
		{
			"function argument",
			duration + "fn wait(d: Duration): Int {\n    1\n}\n\nfn f(): Int {\n    wait(" + cursorMark + ")\n}\n",
			"Duration.seconds", "Duration.seconds(${1:n})$0",
			[]string{"Duration.as_seconds"},
		},
		{
			"return",
			duration + "fn f(b: Bool): Duration {\n    if b {\n        return " + cursorMark + "\n    }\n    Duration.hours(1)\n}\n",
			"Duration.minutes", "Duration.minutes(${1:n})$0",
			nil,
		},
		{
			"generic binding instantiated against the annotation",
			"fn f(): Int {\n    m: Map<String, Int> = " + cursorMark + "\n    1\n}\n",
			"Map.empty", "Map.empty()$0",
			[]string{"Map.put", "Map.merge", "Map.map_values"},
		},
		{
			"Result of the type offers its owner's Result constructors",
			"import std/calendar.{Date, Error}\n\nfn f(): Result<Date, Error> {\n    " + cursorMark + "\n}\n",
			"Date.parse", "Date.parse(${1:s})$0",
			[]string{`Date"2026-05-04"`},
		},
		{
			"user struct",
			"struct Config {\n    port: Int\n}\n\nimpl Config {\n    pub fn default(): Config {\n        Config{port: 80}\n    }\n\n    pub fn with_port(c: Config, port: Int): Config {\n        Config{port: port}\n    }\n}\n\nfn f(): Int {\n    c: Config = " + cursorMark + "\n    c.port\n}\n",
			"Config.default", "Config.default()$0",
			[]string{"Config.with_port"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := completeWith(t, true, tt.src)
			it := mustItem(t, items, tt.label)
			if te := textEditOf(t, it); te.NewText != tt.text {
				t.Errorf("%s inserts %q, want %q", tt.label, te.NewText, tt.text)
			}
			if len(it.AdditionalTextEdits) != 0 {
				t.Errorf("%s adds an import, but its owner is in scope: %+v", tt.label, it.AdditionalTextEdits)
			}
			labelsExclude(t, items, tt.exclude...)
		})
	}
}

func TestCompletion_ConstructorsRankAfterFittingLocals(t *testing.T) {
	src := "fn f(): Int {\n    word = \"a\"\n    base: Map<String, Int> = Map.empty()\n    m: Map<String, Int> = " + cursorMark + "\n    1\n}\n"
	items := complete(t, src)
	labelsBefore(t, items, "base", "Map.empty")
	labelsBefore(t, items, "Map.empty", "word")
}

func TestCompletion_ConstructorsMatchTheFunctionName(t *testing.T) {
	src := "import std/instant.Instant\n\nfn f(): Int {\n    i: Instant = no" + cursorMark + "\n    1\n}\n"
	it := mustItem(t, completeWith(t, true, src), "Instant.now")
	if it.FilterText == nil || *it.FilterText != "now" {
		t.Errorf("Instant.now filter text = %v, want \"now\"", it.FilterText)
	}
	if te := textEditOf(t, it); te.NewText != "Instant.now()$0" {
		t.Errorf("Instant.now inserts %q", te.NewText)
	}
}

func TestCompletion_OwnerMemberIsNotDuplicated(t *testing.T) {
	src := "import std/duration.Duration\n\nfn f(): Int {\n    d: Duration = Duration.se" + cursorMark + "\n    1\n}\n"
	items := complete(t, src)
	if n := len(itemsWithLabel(items, "seconds")); n != 1 {
		t.Errorf("member completion offers seconds %d times; got %v", n, itemLabels(items))
	}
	labelsExclude(t, items, "Duration.seconds")
}

func TestCompletion_NoConstructorsWithoutANominalExpectedType(t *testing.T) {
	tests := map[string]string{
		"unknown":   "import std/duration.Duration\n\nfn f(): Int {\n    x = " + cursorMark + "\n    1\n}\n",
		"primitive": "fn f(): Int {\n    n: Int = " + cursorMark + "\n    n\n}\n",
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			for _, it := range complete(t, src) {
				if strings.Contains(it.Label, ".") || strings.Contains(it.Label, `"`) {
					t.Errorf("offers %q at a position with no nominal expected type", it.Label)
				}
			}
		})
	}
}

func TestCompletion_ConstructorsPlainTextFallback(t *testing.T) {
	tests := []struct{ src, label, text string }{
		{"import std/duration.Duration\n\nfn f(): Int {\n    d: Duration = " + cursorMark + "\n    1\n}\n", "Duration.seconds", "Duration.seconds"},
		{"fn f(): Int {\n    m: Map<String, Int> = " + cursorMark + "\n    1\n}\n", "Map.empty", "Map.empty()"},
		{"import std/calendar.Date\n\nfn f(): Int {\n    d: Date = " + cursorMark + "\n    1\n}\n", `Date"2026-05-04"`, `Date"2026-05-04"`},
	}
	for _, tt := range tests {
		it := mustItem(t, completeWith(t, false, tt.src), tt.label)
		if it.InsertTextFormat != nil && *it.InsertTextFormat == protocol.InsertTextFormatSnippet {
			t.Errorf("%s is a snippet without snippet support", tt.label)
		}
		if te := textEditOf(t, it); te.NewText != tt.text {
			t.Errorf("%s inserts %q, want %q", tt.label, te.NewText, tt.text)
		}
	}
}

func TestCompletion_ConstructorImportsItsOwner(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"lib.nomi":  "import std/duration.Duration\n\npub fn wait(d: Duration): Int {\n    1\n}\n",
		"main.nomi": "fn main() {\n}\n",
	})
	s := NewServer()
	s.snippetSupport = true
	s.docs.SetWorkspaceRoot(dir)
	s.docs.IndexWorkspace(context.Background())
	uri := "file://" + filepath.Join(dir, "main.nomi")
	list := completeIn(t, s, uri, "import lib\n\nfn f(): Int {\n    lib.wait("+cursorMark+")\n}\n")
	it := mustItem(t, list.Items, "Duration.seconds")
	wantEdit(t, onlyEdit(t, it), 1, 0, 1, 0, "import std/duration.Duration\n")
}
