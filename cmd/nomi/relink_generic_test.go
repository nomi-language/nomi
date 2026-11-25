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
	named := `pub fn label<T>(x: T) -> String where T: Display
    "<${x}>"
end

pub struct Tag<T>
    value: T
end

impl Tag<T>
    pub fn show<T>(t: Tag<T>) -> String where T: Display
        "tag ${t.value}"
    end
end

impl Display for Tag<T> where T: Display
    fn to_string(t: Tag<T>) -> String
        "#${t.value}"
    end
end
`
	p := cacheProject{name: "generic-across", entry: "main.nomi", files: map[string]string{
		"nomi.toml":  "[package]\nname = \"genericacross\"\nentry_points = [\"main\"]\n",
		"named.nomi": named,
		"box.nomi": `pub struct Coin
    cents: Int
end

impl Display for Coin
    fn to_string(c: Coin) -> String
        "${c.cents}c"
    end
end
`,
		"a.nomi": `import
    named.Tag
    named.label
end

pub fn a_line(n: Int) -> String
    "${label(n)} ${Tag.show(Tag{value: n})} ${Tag{value: n}}"
end
`,
		"b.nomi": `import
    box.Coin
    named.Tag
    named.label
end

pub fn b_line(s: String) -> String
    "${label(s)} ${label(Coin{cents: 5})} ${Tag.show(Tag{value: Coin{cents: 7}})} ${Tag{value: Coin{cents: 8}}}"
end
`,
		"c.nomi": `import named.label

pub fn c_line(n: Int) -> String
    label(n * 2)
end
`,
		"main.nomi": `import
    std::io
    a.a_line
    b.b_line
    c.c_line
end

fn main()
    io.print(a_line(3))
    io.print(b_line("hi"))
    io.print(c_line(4))
end
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

// TestRelinkAfterEditsOfAFileThatHoldsOnlyTemplates: pick.nomi declares
// only bounded generic functions, one of them reached only through the
// other's instance, and box.nomi only a generic struct and its generic
// Display impl, so neither file lowers anything of its own: each holds
// only the instances main.nomi asks it for. Each body edit, one after
// another, relinks the program and prints what a fresh command does. A
// relink that dropped every instance a file held left it with no object,
// and the instances main.nomi then asked for again were kept outside any
// file's object, where the next edit of the template did not drop them:
// the run after it used the body from before.
func TestRelinkAfterEditsOfAFileThatHoldsOnlyTemplates(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi several times per edit; -short")
	}
	pick := `/// The largest of items, or fallback when there is none larger.
pub fn largest<T>(items: List<T>, fallback: T) -> T where T: Comparable
    Iter.reduce(items, fallback, fn best, x -> larger(best, x))
end

fn larger<T>(a: T, b: T) -> T where T: Comparable
    case Comparable.compare(a, b)
        .Less -> b
        _ -> a
    end
end
`
	box := `pub struct Box<T>
    value: T
end

impl Display for Box<T> where T: Display
    fn to_string(b: Box<T>) -> String
        "box ${b.value}"
    end
end
`
	p := cacheProject{name: "templates-only", entry: "main.nomi", files: map[string]string{
		"nomi.toml": "[package]\nname = \"templatesonly\"\nentry_points = [\"main\"]\n",
		"pick.nomi": pick,
		"box.nomi":  box,
		"main.nomi": `import
    std::io
    box.Box
    pick.largest
end

fn main()
    io.print("${largest([3, 9, 4], 0)}")
    io.print(largest(["b", "c", "a"], ""))
    io.print("${Box{value: largest([1, 2], 0)}}")
end
`,
	}}
	smallest := strings.Replace(pick, ".Less -> b", ".Greater -> b", 1)
	edits := []cacheEdit{
		{what: "a helper reached only through an instance", rel: "pick.nomi", text: smallest},
		{what: "the helper back", rel: "pick.nomi", text: pick},
		{what: "the helper again", rel: "pick.nomi", text: smallest},
		{what: "the helper's other arm", rel: "pick.nomi", text: strings.Replace(smallest, "_ -> a", "_ -> b", 1)},
		{what: "a generic impl's body", rel: "box.nomi", text: strings.Replace(box, `"box ${b.value}"`, `"[${b.value}]"`, 1)},
		{what: "the generic impl's body again", rel: "box.nomi", text: strings.Replace(box, `"box ${b.value}"`, `"<${b.value}>"`, 1)},
		{what: "the generic impl back", rel: "box.nomi", text: box},
	}
	_, _, byEdit := runCacheEditsBy(t, p, edits)
	for _, e := range edits {
		if byEdit[e.what] == 0 {
			t.Errorf("no command relinked the program after %s in %s: %q", e.what, e.rel, cacheWhy[e.what])
		}
	}
}

// TestRelinkAfterEditsOfAGenericImplAnotherFileBoxes: main.nomi boxes a
// Box<Int> into Display, so it asks box.nomi for the instance of its
// generic Display impl by holding the instance erased, which names
// nothing: box.nomi's object holds the instance and the dispatch entry
// that reaches it. After each edit of the impl's body, every command
// prints what a fresh one does, where a relink that dropped the instance
// ran a program with no Display for Box<Int>.
func TestRelinkAfterEditsOfAGenericImplAnotherFileBoxes(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi several times per edit; -short")
	}
	box := `pub struct Box<T>
    value: T
end

impl Display for Box<T> where T: Display
    fn to_string(b: Box<T>) -> String
        "box ${b.value}"
    end
end

pub fn plain(n: Int) -> Int
    n + 1
end
`
	p := cacheProject{name: "boxed-instance", entry: "main.nomi", files: map[string]string{
		"nomi.toml": "[package]\nname = \"boxedinstance\"\nentry_points = [\"main\"]\n",
		"box.nomi":  box,
		"main.nomi": `import
    std::io
    box.{Box, plain}
end

fn show(d: Display) -> String
    Display.to_string(d)
end

fn main()
    io.print(show(Box{value: plain(4)}))
end
`,
	}}
	runCacheEdits(t, p, []cacheEdit{
		{what: "the boxed impl's body", rel: "box.nomi", text: strings.Replace(box, `"box ${b.value}"`, `"[${b.value}]"`, 1)},
		{what: "the boxed impl's body again", rel: "box.nomi", text: strings.Replace(box, `"box ${b.value}"`, `"<${b.value}>"`, 1)},
		{what: "the boxed impl back", rel: "box.nomi", text: box},
	})
}
