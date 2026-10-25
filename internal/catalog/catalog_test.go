package catalog_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/catalog"
)

func TestParseReadsEntriesWithTheirOffsets(t *testing.T) {
	text := "# Polish\n\n[cart_items]\nsource = '''{$count} items'''\ntext = '''\n{$count} produkty'''\n\n[\"order status\"]\nsource = \"a \\\"b\\\"\"\ntext = 'c'  # trailing\n"
	f, diags := catalog.Parse(text)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	if len(f.Entries) != 2 {
		t.Fatalf("%d entries", len(f.Entries))
	}
	e := f.Entry("cart_items")
	if e.Source.Text != "{$count} items" || e.Text.Text != "{$count} produkty" {
		t.Errorf("source %q text %q", e.Source.Text, e.Text.Text)
	}
	// A literal string places each byte of its text: `$count` in `text`.
	if got := text[e.Text.Offset(1):e.Text.Offset(7)]; got != "$count" || !e.Text.Exact {
		t.Errorf("text offset reads %q", got)
	}
	if l, c := catalog.LineCol(text, e.Span.Start); l != 3 || c != 1 {
		t.Errorf("header at %d:%d", l, c)
	}
	q := f.Entry("order status")
	if q == nil || q.Source.Text != `a "b"` || q.Source.Exact || q.Text.Text != "c" {
		t.Errorf("quoted entry %+v", q)
	}
}

func TestParseRefusesWhatACatalogIsNot(t *testing.T) {
	cases := []struct{ text, want string }{
		{"[a]\nsource = 3\n", "a catalog's values are strings: write `source = '''...'''`"},
		{"[a.b]\n", "a catalog's tables are messages: `[name]`, with no dotted name"},
		{"[[a]]\n", "a catalog has no arrays of tables: each message is one `[name]` table"},
		{"[a]\ngender = 'x'\ngender = 'y'\n", "`a` gives `gender` twice"},
		{"[a]\ntext = 'x'\ntext = 'y'\n", "`a` gives `text` twice"},
		{"[a]\n[a]\n", "`a` has two tables in this catalog"},
		{"[a]\ntext = '''x\n", "this `'''` string is not closed"},
		{"[a]\ntext = 'x' y\n", "unexpected text after the value; a catalog line holds one key and its string"},
		{"text = 'x'\n", "`text` is outside any message: a catalog's keys are in `[name]` tables"},
	}
	for _, c := range cases {
		_, diags := catalog.Parse(c.text)
		if len(diags) == 0 || diags[0].Message != c.want {
			t.Errorf("%q: %v, want %q", c.text, diags, c.want)
		}
	}
}

func TestSetSourceRewritesOrAddsTheSource(t *testing.T) {
	got, err := catalog.SetSource("[a]\nsource = '''old'''\ntext = '''x'''\n", "a", "new {$n}")
	if err != nil || got != "[a]\nsource = '''new {$n}'''\ntext = '''x'''\n" {
		t.Errorf("rewrite: %q %v", got, err)
	}
	got, err = catalog.SetSource("[a]\ntext = '''x'''\n", "a", "it's")
	if err != nil || got != "[a]\nsource = '''it's'''\ntext = '''x'''\n" {
		t.Errorf("add: %q %v", got, err)
	}
	if f, d := catalog.Parse(got); len(d) > 0 || f.Entry("a").Source.Text != "it's" {
		t.Errorf("the added source does not read back: %v", d)
	}
	if _, err := catalog.SetSource("[a]\n", "b", "x"); err == nil || !strings.Contains(err.Error(), "no entry for `b`") {
		t.Errorf("missing entry: %v", err)
	}
}
