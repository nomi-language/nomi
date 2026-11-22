package frontend_test

import (
	"strings"
	"testing"
)

// A placeholder with no function writes its value with std's `Localize`
// (spec §18 *Phrases*), so its type implements that interface. A
// program's own interface named `Localize` is another interface: a type
// that implements only it is the ordinary error at the placeholder. Read
// by the interface's name, the check passed it and the phrase's expansion
// failed as a defect in Nomi.
func TestPhrases_AProgramLocalizeIsNotStds(t *testing.T) {
	const own = `import std::io

interface Localize
    fn shout(value: self) -> String
end

struct Name
    text: String
end

impl Localize for Name
    fn shout(n: Name) -> String
        n.text
    end
end

phrase greet(who: Name) ` + "`Hello, {$who}`" + `

fn main()
    io.print(Phrase.format(greet(Name{text: "Ada"}), Locale"en"))
end
`
	ds := checkShop(t, map[string]string{"main.nomi": own})
	want := "main.nomi:17:33: en: `$who` is a `Name`, which does not implement `Localize`\n" +
		"  help: a placeholder with no function formats its value with `Localize`: implement `Localize for Name`, or annotate it, as `{$who :string}` for an enum"
	if len(ds) != 1 || ds[0] != want {
		t.Fatalf("got:\n%s\nwant only:\n%s", strings.Join(ds, "\n"), want)
	}

	const stds = `import
    std::io
    std::localize.{Localize}
end

struct Name
    text: String
end

impl Localize for Name
    fn localize(n: Name, _locale: Locale) -> String
        n.text
    end
end

phrase greet(who: Name) ` + "`Hello, {$who}`" + `

fn main()
    io.print(Phrase.format(greet(Name{text: "Ada"}), Locale"en"))
end
`
	if ds := checkShop(t, map[string]string{"main.nomi": stds}); len(ds) > 0 {
		t.Fatalf("std's Localize is refused:\n%s", strings.Join(ds, "\n"))
	}
}
