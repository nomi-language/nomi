package main

import (
	"strings"
	"testing"
)

// TestRelinkAProgramWithLiteralsCheckedAtLoad: main.nomi writes
// `Date"2026-05-04"` and `Code"abc"`, whose handler code.nomi declares, and
// code.nomi writes `Duration"2h"`. The program is kept with its file
// records, and a body edit of either file, which leaves each literal's text
// alone, relinks it and runs as a fresh run does. An edit of the handler's
// body makes main.nomi's untouched `Code"abc"` invalid, and one of main.nomi
// makes its date invalid: both are reported as a fresh check reports them,
// since a relink checks every literal again.
func TestRelinkAProgramWithLiteralsCheckedAtLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi several times per edit; -short")
	}
	code := func(body string) string {
		return `import
    std::duration.Duration
    std::literals.{Fragment, Literal}
end

pub struct Code
    text: String
end

impl Literal for Code
    fn from_fragments(fragments: List<Fragment<String>>) -> Result<Code, String>
        text =
            fragments
            |> Iter.map(fn frag ->
                case frag
                    .Static(s) -> s
                    .Dynamic(v) -> v
                end
            end)
            |> String.join()

` + body + `
    end
end

pub fn window() -> Duration
    Duration"2h"
end
`
	}
	ok := "        Ok(Code{text:})"
	main := func(day, greeting string) string {
		return `import
    std::calendar.Date
    std::io
    code.{Code, window}
end

fn main()
    io.print("` + greeting + `")
    day = Date"` + day + `"
    c = Code"abc"
    io.print("${Date.month(day)} ${c.text} ${window()}")
end
`
	}
	p := cacheProject{name: "literals", entry: "main.nomi", files: map[string]string{
		"nomi.toml": "[module]\nname = \"literals\"\nentry_points = [\"main\"]\n",
		"code.nomi": code(ok),
		"main.nomi": main("2026-05-04", "hello"),
	}}
	_, _, byEdit := runCacheEditsBy(t, p, []cacheEdit{
		{what: "main's greeting", rel: "main.nomi", text: main("2026-05-04", "hello again")},
		{what: "a line above main's literals", rel: "main.nomi", text: strings.Replace(main("2026-05-04", "hi"), "fn main()\n", "fn main()\n    io.print(\"first\")\n", 1)},
		{what: "the handler's body", rel: "code.nomi", text: code("        Ok(Code{text: \"${text}!\"})")},
		{what: "the handler fails", rel: "code.nomi", text: code(`        Err("no codes: ${text}")`)},
		{what: "the handler back", rel: "code.nomi", text: code(ok)},
		{what: "an invalid date", rel: "main.nomi", text: main("2026-13-04", "hi")},
		{what: "the date back", rel: "main.nomi", text: main("2026-05-04", "hi")},
	})
	for _, what := range []string{"main's greeting", "a line above main's literals", "the handler's body"} {
		if byEdit[what] == 0 {
			t.Errorf("after %s, no command relinked the program: %v", what, cacheWhy[what])
		}
	}
	for _, what := range []string{"the handler fails", "an invalid date"} {
		if byEdit[what] != 0 {
			t.Errorf("after %s, a command relinked a program a fresh check rejects", what)
		}
	}
}
