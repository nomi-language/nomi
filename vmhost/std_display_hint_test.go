package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// Interpolating a std type with no `Display` gets help the author can act
// on: a `Phrase` or `Noun` says how it is formatted for a reader, and any
// other std type says only std can implement it. Neither tells the author
// to add `derive Display` to a type std declares.
func TestStdTypeWithoutDisplay_HintSaysWhatToDoInstead(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"Phrase", "import std::io\n\nphrase hello(name: String) `Hello, {$name}!`\n\nfn main()\n    io.print(\"${hello(\"Ann\")}\")\nend\n",
			"a `Phrase` is formatted for a reader: `Phrase.format(m, locale)`"},
		{"Noun", "import std::io\n\nnoun book `.input {$count :integer} .match $count one {{book}} * {{books}}`\n\nfn main()\n    io.print(\"${book}\")\nend\n",
			"a `Noun` is formatted for a reader: `Localize.localize(noun, locale)`"},
		{"Collator", "import\n    std::collation.Collator\n    std::io\nend\n\nfn main()\n    io.print(\"${Collator.new(Locale\"sv\")}\")\nend\n",
			"only std can implement `Display` for `Collator`"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := vmhost.LoadSource("main.nomi", c.src)
			if err == nil {
				t.Fatalf("the front end admits interpolating a %s:\n%s", c.name, c.src)
			}
			msg := err.Error()
			if !strings.Contains(msg, "no impl of `Display` for `"+c.name+"`") {
				t.Fatalf("the error is not the missing Display:\n%s", msg)
			}
			if !strings.Contains(msg, c.want) {
				t.Errorf("the help does not say %q:\n%s", c.want, msg)
			}
			if strings.Contains(msg, "derive Display") {
				t.Errorf("the help tells the author to edit std:\n%s", msg)
			}
		})
	}
}
