package lsp

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestLiteralCompletion_DateOffersItsDocumentedExamples(t *testing.T) {
	s := NewServer()
	s.snippetSupport = true
	src := "import std/calendar.Date\n\nfn f() {\n    _ = Date\"" + cursorMark + "\"\n}\n"
	items := completeIn(t, s, "file:///literal_completion.nomi", src).Items
	if len(items) == 0 {
		t.Fatal("no examples offered inside Date\"\"")
	}
	first := items[0]
	if first.Label != "2026-05-04" {
		t.Fatalf("first example = %q, want 2026-05-04; got %v", first.Label, itemLabels(items))
	}
	if first.Detail == nil || *first.Detail != "example from Date docs" {
		t.Errorf("detail = %v", first.Detail)
	}
	edit := first.TextEdit.(protocol.TextEdit)
	if edit.NewText != "${1:2026}-${2:05}-${3:04}" {
		t.Errorf("snippet = %q", edit.NewText)
	}
	if first.InsertTextFormat == nil || *first.InsertTextFormat != protocol.InsertTextFormatSnippet {
		t.Error("the example is not sent as a snippet")
	}
	want := protocol.Range{Start: protocol.Position{Line: 3, Character: 13}, End: protocol.Position{Line: 3, Character: 13}}
	if edit.Range != want {
		t.Errorf("range = %+v, want %+v", edit.Range, want)
	}
	if len(items) > literalExampleLimit {
		t.Errorf("offered %d examples, more than %d", len(items), literalExampleLimit)
	}
	for _, it := range items {
		if strings.Contains(it.Label, "Date") || it.Label == "…" {
			t.Errorf("offered %q, which is not a body", it.Label)
		}
	}
}

func TestLiteralCompletion_PlainTextReplacesTheTypedBody(t *testing.T) {
	s := NewServer()
	src := "import std/calendar.Date\n\nfn f() {\n    _ = Date\"20" + cursorMark + "\n}\n"
	items := completeIn(t, s, "file:///literal_completion.nomi", src).Items
	it := mustItem(t, items, "2026-05-04")
	edit := it.TextEdit.(protocol.TextEdit)
	if edit.NewText != "2026-05-04" || it.InsertTextFormat != nil {
		t.Errorf("without snippet support the body is inserted as text; got %q", edit.NewText)
	}
	want := protocol.Range{Start: protocol.Position{Line: 3, Character: 13}, End: protocol.Position{Line: 3, Character: 15}}
	if edit.Range != want {
		t.Errorf("range = %+v, want %+v", edit.Range, want)
	}
}

func TestLiteralCompletion_TypeWithoutExamplesOffersNothing(t *testing.T) {
	src := "import std/literals.{Fragment, Literal}\n\n/// A probe.\ntype Probe\n\nimpl Literal for Probe {\n" +
		"    fn from_fragments(fragments: List<Fragment<String>>): Probe {\n        _ = fragments\n        Probe\n    }\n}\n\n" +
		"fn f(): Probe {\n    Probe\"" + cursorMark + "\"\n}\n"
	if items := complete(t, src); len(items) != 0 {
		t.Fatalf("offered %v for a type whose docs have no example", itemLabels(items))
	}
}

func TestLiteralCompletion_UserTypeExamples(t *testing.T) {
	src := "import std/literals.{Fragment, Literal}\n\n/// A probe, written `Probe\"a-1\"`.\ntype Probe\n\nimpl Literal for Probe {\n" +
		"    //! assert Probe.name(Probe\"b-2\") == \"\"\n" +
		"    fn from_fragments(fragments: List<Fragment<String>>): Probe {\n        _ = fragments\n        Probe\n    }\n}\n\n" +
		"fn f(): Probe {\n    Probe\"" + cursorMark + "\"\n}\n"
	if got := strings.Join(itemLabels(complete(t, src)), " "); got != "a-1 b-2" {
		t.Fatalf("examples = %q, want \"a-1 b-2\"", got)
	}
}

func TestLiteralBodyAt(t *testing.T) {
	tests := []struct {
		name, src, tag string
		ok             bool
	}{
		{"closed", `x = Date"20` + cursorMark + `"`, "Date", true},
		{"unclosed", `x = Date"20` + cursorMark, "Date", true},
		{"triple opener", `x = Toml"""` + cursorMark, "Toml", true},
		{"raw", "x = Regex`\\d" + cursorMark + "`", "Regex", true},
		{"prompt", `//! assert Date"` + cursorMark + `"`, "Date", true},
		{"plain string", `x = "20` + cursorMark + `"`, "", false},
		{"lowercase", `x = date"20` + cursorMark + `"`, "", false},
		{"after the literal", `x = Date"2026"` + cursorMark, "", false},
		{"in a slot", `x = Date"${y` + cursorMark + `}"`, "", false},
		{"in a comment", `// Date"20` + cursorMark, "", false},
		{"a quote inside a string", `x = "a Date" + Time"` + cursorMark, "Time", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			off := strings.Index(tt.src, cursorMark)
			content := strings.Replace(tt.src, cursorMark, "", 1)
			lb, ok := literalBodyAt(content, off)
			if ok != tt.ok || lb.tag != tt.tag {
				t.Fatalf("literalBodyAt = %+v, %v; want tag %q, %v", lb, ok, tt.tag, tt.ok)
			}
		})
	}
}

// Every stdlib type with a `Literal` impl documents at least one example, so
// completion has something to offer inside each.
func TestLiteralCompletion_EveryStdlibLiteralTypeHasAnExample(t *testing.T) {
	types := map[string][]string{
		"calendar": {"Date", "Time", "NaiveDateTime", "OffsetDateTime", "DateTime"},
		"regex":    {"Regex"},
		"toml":     {"Toml"},
	}
	for module, names := range types {
		src, ok := std.ReadFile(module)
		if !ok {
			t.Fatalf("no std/%s", module)
		}
		if !strings.Contains(string(src), "impl Literal for") {
			t.Fatalf("std/%s declares no Literal impl", module)
		}
		for _, name := range names {
			if !strings.Contains(string(src), "impl Literal for "+name+" {") {
				t.Errorf("std/%s has no Literal impl for %s", module, name)
			}
			if len(docExamples(string(src), name)) == 0 {
				t.Errorf("std/%s documents no %s literal example", module, name)
			}
		}
	}
}

// A `///` line that only mentions the type next to a backtick is prose, not
// a raw typed literal: std/calendar.nomi's "two `Date`s — positive if" once
// offered "s — positive if " as a Date example.
func TestDocExamples_ReadCodeNotProse(t *testing.T) {
	src := strings.Join([]string{
		"/// A day. See `Date`s — positive if `later` is after; write `Date\"2026-05-04\"`.",
		"/// A raw one: `` Date`2026-06-01` ``, and a placeholder `Date\"…\"`.",
		"pub struct Date {",
		"    n: Int",
		"}",
		"",
		"impl Date {",
		"    /// Number of whole days between two `Date`s — positive if `later` is",
		"    //! assert Date.days_between(try Date\"2026-05-04\", try Date\"2026-05-07\") == 3",
		"    //! assert try Date\"2026-${m}-04\" == x",
		"    pub fn days_between(a: Date, b: Date): Int {",
		"        0",
		"    }",
		"}",
	}, "\n")
	got := strings.Join(docExamples(src, "Date"), " | ")
	if want := "2026-05-04 | 2026-06-01 | 2026-05-07"; got != want {
		t.Fatalf("docExamples = %q, want %q", got, want)
	}
}
