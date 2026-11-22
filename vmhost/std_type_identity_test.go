package vmhost_test

import "testing"

// A phrase placeholder with no function formats its value with std's
// `Localize`, so a parameter of the interface type is placeable only when
// the interface is std's, by its declaration. A program's own interface
// named `Localize` is not; read by the name, it was taken for std's and
// the phrase's expansion failed as a defect in Nomi.
func TestStdTypeIdentity_AProgramLocalizeIsNotStds(t *testing.T) {
	refusesModule(t, map[string]string{
		"main.nomi": `import std::io

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

phrase greet(who: Localize) ` + "`Hello, {$who}`" + `

fn main()
    io.print(Phrase.format(greet(Name{text: "Ada"}), Locale"en"))
end
`,
	}, "`$who` is a `Localize`, which does not implement `Localize`")
}
