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

pub subtype Port Int when port_ok

pub struct Span
    low:  Int
    high: Int
    when fn s -> ` + span + `
end

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

// TestRelinkChecksAGenericSubtypesKeptLiteral: main.nomi's literal of the
// generic subtype `Many<Int>` is checked through a function built at its
// type arguments (irbuild.RefinementSiteStep.Check), which the kept
// lowering holds beside its unit's literal builds. An edit of the helper
// the rule calls, in rules.nomi alone, relinks the program, and main.nomi's
// kept literal, which the new helper refuses, is reported as a fresh check
// reports it, not let through.
func TestRelinkChecksAGenericSubtypesKeptLiteral(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi several times per edit; -short")
	}
	rules := func(least string) string {
		return `pub fn enough(n: Int) -> Bool
    n >= ` + least + `
end

pub subtype Many<T> List<T> when fn xs -> enough(List.length(xs))

pub fn count<T>(xs: Many<T>) -> Int
    List.length(xs)
end
`
	}
	const main = `import
    std::io
    rules.{Many, count}
end

fn main()
    m: Many<Int> = [1, 2]
    io.print("${count(m)}")
end
`
	p := cacheProject{name: "generic-refinements", entry: "main.nomi", files: map[string]string{
		"nomi.toml":  "[module]\nname = \"generic_refinements\"\nentry_points = [\"main\"]\n",
		"rules.nomi": rules("1"),
		"main.nomi":  main,
	}}
	_, _, byEdit := runCacheEditsBy(t, p, []cacheEdit{
		{what: "the helper's body", rel: "rules.nomi", text: rules("2")},
		{what: "the helper refuses the literal", rel: "rules.nomi", text: rules("3")},
		{what: "the helper back", rel: "rules.nomi", text: rules("1")},
	})
	if byEdit["the helper's body"] == 0 {
		t.Errorf("after the helper's body, no command relinked the program: %v", cacheWhy["the helper's body"])
	}
	if byEdit["the helper refuses the literal"] != 0 {
		t.Errorf("after the helper refuses the literal, a command relinked a program a fresh check rejects")
	}
}
