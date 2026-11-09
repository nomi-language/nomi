package main

import (
	"strings"
	"testing"
)

// TestRelinkAfterABodyEditOfAGenericUsedAcrossFiles: named.nomi declares a
// generic function, a generic inherent method and a generic Display impl;
// a.nomi and c.nomi use them at Int, and b.nomi at String and at Coin, a
// struct box.nomi declares, which named.nomi does not import. A whole
// lowering keeps each instance in named.nomi's object, so an edit of one
// of those bodies drops instances the other files' kept objects name.
// Each edit, one after another, and undoing them, relinks the kept program
// from its file records (lowering a.nomi and b.nomi again, which ask for
// the instances again, while c.nomi's object names a.nomi's), and every
// command prints what a fresh one does. Such an edit used to miss with "a
// names named.label<…>, which named no longer makes", and lower the whole
// program again.
func TestRelinkAfterABodyEditOfAGenericUsedAcrossFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi several times per edit; -short")
	}
	named := `pub fn label<T>(x: T) -> String where T: Display {
    "<${x}>"
}

pub struct Tag<T> {
    value: T
}

impl Tag<T> {
    pub fn show<T>(t: Tag<T>) -> String where T: Display {
        "tag ${t.value}"
    }
}

impl Display for Tag<T> where T: Display {
    fn to_string(t: Tag<T>) -> String {
        "#${t.value}"
    }
}
`
	p := cacheProject{name: "generic-across", entry: "main.nomi", files: map[string]string{
		"nomi.toml":  "[module]\nname = \"genericacross\"\nentry_points = [\"main\"]\n",
		"named.nomi": named,
		"box.nomi": `pub struct Coin {
    cents: Int
}

impl Display for Coin {
    fn to_string(c: Coin) -> String {
        "${c.cents}c"
    }
}
`,
		"a.nomi": `import {
    named.Tag
    named.label
}

pub fn a_line(n: Int) -> String {
    "${label(n)} ${Tag.show(Tag{value: n})} ${Tag{value: n}}"
}
`,
		"b.nomi": `import {
    box.Coin
    named.Tag
    named.label
}

pub fn b_line(s: String) -> String {
    "${label(s)} ${label(Coin{cents: 5})} ${Tag.show(Tag{value: Coin{cents: 7}})} ${Tag{value: Coin{cents: 8}}}"
}
`,
		"c.nomi": `import named.label

pub fn c_line(n: Int) -> String {
    label(n * 2)
}
`,
		"main.nomi": `import {
    std::io
    a.a_line
    b.b_line
    c.c_line
}

fn main() {
    io.print(a_line(3))
    io.print(b_line("hi"))
    io.print(c_line(4))
}
`,
	}}
	edits := []cacheEdit{
		{what: "a generic function's body", rel: "named.nomi", text: strings.Replace(named, `"<${x}>"`, `"[${x}]"`, 1)},
		{what: "a generic inherent method's body", rel: "named.nomi", text: strings.Replace(named, `"tag ${t.value}"`, `"tagged ${t.value}"`, 1)},
		{what: "a generic impl method's body", rel: "named.nomi", text: strings.Replace(named, `"#${t.value}"`, `"@${t.value}"`, 1)},
		{what: "the original back", rel: "named.nomi", text: named},
		{what: "a generic function's body, on its own line count", rel: "named.nomi", text: strings.Replace(named, `"<${x}>"`, "y = x\n    \"<${y}>\"", 1)},
	}
	_, _, byEdit := runCacheEditsBy(t, p, edits)
	for _, e := range edits {
		if byEdit[e.what] == 0 {
			t.Errorf("no command relinked the program after %s in named.nomi: %q", e.what, cacheWhy[e.what])
		}
	}
}
