package frontend_test

import (
	"regexp"
	"strings"
	"testing"
)

var hiddenAlias = regexp.MustCompile(`[A-Za-z0-9_]\$`)

// A file that names a std type or module without importing it is told
// which import to write: the one module's, or each module's when a few
// export the name, and none for a name std does not export. The name still
// needs its import. The expansion of a file's `phrase` and `noun`
// declarations imports std/phrase under a hidden alias (`phrase$`); no
// diagnostic names that alias. `Phrase`, `Noun` and `Locale` are prelude
// names and need no import, so `Message` names std::mail's and
// std::websocket's.
func TestAStdNameWithoutItsImportNamesTheImport(t *testing.T) {
	const noun = "noun shoe `.input {$count :integer} .match $count one {{shoe}} * {{shoes}}`\n\n"
	cases := []struct {
		name, main string
		want       []string
	}{
		{
			"a phrase printed as Display",
			"import std::io\n\n" + noun +
				"phrase shoe_count(item: Noun, count: Int) `{$count} {$item :nomi:noun count=$count}`\n\n" +
				"fn main()\n    io.print(shoe_count(shoe, 3))\nend\n",
			[]string{
				"main.nomi:8:5: Phrase does not implement Display (required by `where T: Display`)\n  help: a `Phrase` is formatted for a reader: `Phrase.format(m, locale)`",
			},
		},
		{
			"Message in a file with a phrase",
			"import std::io\n\n" +
				"fn show(m: Message) -> String\n    Phrase.format(m, Locale\"en\")\nend\n\nfn main()\n    io.print(\"\")\nend\n",
			[]string{
				"main.nomi:3:12: unknown type \"Message\"\n  help: import `std::mail.Message` or `std::websocket.Message`",
			},
		},
		{
			"a std type, a std module and a name std does not export",
			"fn main()\n    m = Method.Post\n    io.print(m)\n    g = Gizmo.Make\n    _ = g\nend\n",
			[]string{
				"main.nomi:2:9: undefined type or variant 'Method'\n  help: import `std::http.Method`",
				"main.nomi:3:5: undefined variable 'io'\n  help: import `std::io`",
				"main.nomi:4:9: undefined type or variant 'Gizmo'",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := checkShop(t, map[string]string{"main.nomi": c.main})
			if len(ds) == 0 {
				t.Fatalf("the file is accepted:\n%s", c.main)
			}
			for _, d := range ds {
				// A hidden alias is a name with a `$` after it (`phrase$`);
				// a message's own `{$count}` is not one.
				if hiddenAlias.MatchString(d) {
					t.Errorf("a diagnostic names an internal alias: %s", d)
				}
			}
			if got := strings.Join(ds, "\n"); got != strings.Join(c.want, "\n") {
				t.Errorf("got\n%s\nwant\n%s", got, strings.Join(c.want, "\n"))
			}
		})
	}
}
