package main

import (
	"strings"
	"testing"
)

// TestRelinkChecksEveryRefinementLiteralAgain: main.nomi writes literals of
// the refinements rules.nomi declares, `Port`, whose rule calls a helper,
// and the struct `Span`. The program is kept with its file records, and a
// body edit of either file relinks it and runs as a fresh run does. An edit
// of the helper's body that main.nomi's untouched `80` breaks, an edit of
// Span's rule that its untouched literal breaks, and one of main.nomi that
// writes a literal its rule refuses are reported as a fresh check reports
// them, since a relink checks every refinement literal again, kept or
// lowered again.
func TestRelinkChecksEveryRefinementLiteralAgain(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi several times per edit; -short")
	}
	rules := func(low, span string) string {
		return `pub fn port_ok(p: Int) -> Bool
    p >= ` + low + ` and p <= 65535
end

pub type Port Int when port_ok

pub struct Span
    low:  Int
    high: Int
end when fn s -> ` + span + `

pub fn describe(p: Port) -> String
    "port ${p}"
end
`
	}
	main := func(greeting, port string) string {
		return `import
    std::io
    rules.{Port, Span, describe}
end

fn main()
    io.print("` + greeting + `")
    p: Port = ` + port + `
    s: Span = {low: 1, high: 2}
    io.print("${describe(p)} ${s.low}..${s.high}")
end
`
	}
	p := cacheProject{name: "refinements", entry: "main.nomi", files: map[string]string{
		"nomi.toml":  "[module]\nname = \"refinements\"\nentry_points = [\"main\"]\n",
		"rules.nomi": rules("1", "s.low <= s.high"),
		"main.nomi":  main("hello", "80"),
	}}
	_, _, byEdit := runCacheEditsBy(t, p, []cacheEdit{
		{what: "main's greeting", rel: "main.nomi", text: main("hello again", "80")},
		{what: "the helper's body", rel: "rules.nomi", text: rules("0", "s.low <= s.high")},
		{what: "the helper refuses 80", rel: "rules.nomi", text: rules("1024", "s.low <= s.high")},
		{what: "the helper back", rel: "rules.nomi", text: rules("1", "s.low <= s.high")},
		{what: "span's rule refuses the literal", rel: "rules.nomi", text: rules("1", "s.low > s.high")},
		{what: "span's rule back", rel: "rules.nomi", text: rules("1", "s.low <= s.high")},
		{what: "main writes port 0", rel: "main.nomi", text: main("hi", "0")},
		{what: "main back", rel: "main.nomi", text: strings.Replace(main("hi", "80"), "hi", "hey", 1)},
	})
	for _, what := range []string{"main's greeting", "the helper's body"} {
		if byEdit[what] == 0 {
			t.Errorf("after %s, no command relinked the program: %v", what, cacheWhy[what])
		}
	}
	for _, what := range []string{"the helper refuses 80", "span's rule refuses the literal", "main writes port 0"} {
		if byEdit[what] != 0 {
			t.Errorf("after %s, a command relinked a program a fresh check rejects", what)
		}
	}
}
