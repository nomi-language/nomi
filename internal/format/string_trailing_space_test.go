package format

import "testing"

// Spaces at the end of a line of a multi-line string are part of its value,
// and formatting keeps them. The renderer strips trailing spaces from every
// line, and it used to strip these too, changing the string.
func TestFormat_MultiLineStringKeepsTrailingSpaces(t *testing.T) {
	for name, src := range map[string]string{
		"triple-quoted":    "fn main()\n    s = \"\"\"\n        a  \n        b\n        \"\"\"\nend\n",
		"raw":              "fn main()\n    s = `\n        a  \n        b\n        `\nend\n",
		"a line of spaces": "fn main()\n    s = \"\"\"\n        a\n          \n        b\n        \"\"\"\nend\n",
		"a call argument":  "fn main()\n    f(\"\"\"\n    a  \n    \"\"\")\nend\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Format(src)
			if err != nil {
				t.Fatal(err)
			}
			if err := SameMeaning(src, got); err != nil {
				t.Fatalf("%v\n%q", err, got)
			}
			if again, _ := Format(got); again != got {
				t.Fatalf("not idempotent:\n%q\nthen:\n%q", got, again)
			}
		})
	}
}
