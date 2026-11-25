package main

import (
	"strings"
	"testing"
)

func tabLines(text string) []specLine {
	return fenceLines(strings.Split(text, "\n"))
}

func joinLines(lines []specLine) string {
	texts := make([]string, len(lines))
	for i, l := range lines {
		texts[i] = l.text
	}
	return strings.Join(texts, "\n")
}

// TestTabs_AGroupBecomesHTML holds expandTabs to the markup the site's
// client reads: a div per group with its key and a div per tab with its
// label, the label first in it, each tab's Markdown kept, and a marker
// inside a code fence left as code.
func TestTabs_AGroupBecomesHTML(t *testing.T) {
	src := strings.Join([]string{
		"Before.",
		"",
		"<!-- tabs: os -->",
		"",
		"**macOS**",
		"",
		"```console",
		"$ one",
		"```",
		"",
		"Under one.",
		"",
		"**A & B**",
		"",
		"```text",
		"<!-- tabs: inside -->",
		"**Not a label**",
		"```",
		"",
		"<!-- end tabs -->",
		"",
		"After.",
	}, "\n")
	lines, err := expandTabs("doc.md", tabLines(src))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Before.",
		"",
		`<div class="nomi-tabgroup" data-tabs="os">`,
		"",
		`<div class="nomi-tabpanel" data-tab="macOS">`,
		"",
		`<p class="nomi-tablabel">macOS</p>`,
		"",
		"```console",
		"$ one",
		"```",
		"",
		"Under one.",
		"",
		"</div>",
		"",
		`<div class="nomi-tabpanel" data-tab="A &amp; B">`,
		"",
		`<p class="nomi-tablabel">A &amp; B</p>`,
		"",
		"```text",
		"<!-- tabs: inside -->",
		"**Not a label**",
		"```",
		"",
		"</div>",
		"",
		"</div>",
		"",
		"After.",
	}, "\n")
	if got := joinLines(lines); got != want {
		t.Errorf("expandTabs wrote\n%s\nwant\n%s", got, want)
	}
}

// TestTabs_AMalformedGroupIsRefused holds expandTabs to refusing each
// malformed group with a message that names the line and what is wrong.
func TestTabs_AMalformedGroupIsRefused(t *testing.T) {
	tab := func(label, body string) string { return "**" + label + "**\n\n" + body + "\n\n" }
	two := tab("A", "a") + tab("B", "b")
	for _, c := range []struct {
		name, src, want string
	}{
		{"no end", "<!-- tabs: os -->\n\n" + two, "doc.md:1: the tab group opened here (key os) has no `<!-- end tabs -->`"},
		{"end alone", "text\n\n<!-- end tabs -->\n", "doc.md:3: `<!-- end tabs -->` closes no tab group"},
		{"nested", "<!-- tabs: os -->\n\n" + tab("A", "a") + "<!-- tabs: editor -->\n", "doc.md:7: a tab group opens inside the one opened on line 1"},
		{"bad key", "<!-- tabs: OS -->\n", `doc.md:1: "<!-- tabs: OS -->" does not open a tab group`},
		{"no key", "<!-- tabs -->\n", `doc.md:1: "<!-- tabs -->" does not open a tab group`},
		{"bad end", "<!-- tabs: os -->\n\n" + two + "<!-- end tabs: os -->\n", `doc.md:11: "<!-- end tabs: os -->" does not close a tab group`},
		{"one tab", "<!-- tabs: os -->\n\n" + tab("A", "a") + "<!-- end tabs -->\n", "doc.md:1: the tab group opened here holds 1 tab"},
		{"empty tab", "<!-- tabs: os -->\n\n" + tab("A", "a") + "**B**\n\n" + tab("C", "c") + "<!-- end tabs -->\n", `doc.md:7: tab "B" holds nothing before line 9`},
		{"last tab empty", "<!-- tabs: os -->\n\n" + tab("A", "a") + "**B**\n\n<!-- end tabs -->\n", `doc.md:7: tab "B" holds nothing before line 9`},
		{"same label", "<!-- tabs: os -->\n\n" + tab("A", "a") + tab("A", "b") + "<!-- end tabs -->\n", `doc.md:7: the tab group opened on line 1 has two tabs labeled "A"`},
		{"text first", "<!-- tabs: os -->\n\nloose\n\n" + two + "<!-- end tabs -->\n", `doc.md:3: the tab group opened on line 1 holds "loose" before its first`},
		{"code first", "<!-- tabs: os -->\n\n```console\n$ x\n```\n\n" + two + "<!-- end tabs -->\n", "doc.md:3: the tab group opened on line 1 holds code before its first"},
		{"heading", "<!-- tabs: os -->\n\n" + tab("A", "## Inside") + "<!-- end tabs -->\n", `doc.md:5: heading "## Inside" is inside the tab group opened on line 1`},
		{"label in a paragraph", "<!-- tabs: os -->\n\n**A**\ntext\n\n" + tab("B", "b") + "<!-- end tabs -->\n", "doc.md:3: tab label **A** must be a paragraph of its own"},
	} {
		_, err := expandTabs("doc.md", tabLines(c.src))
		if err == nil {
			t.Errorf("%s: expandTabs admits\n%s", c.name, c.src)
			continue
		}
		if !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s: expandTabs says\n%s\nwant it to start\n%s", c.name, err, c.want)
		}
	}
}
