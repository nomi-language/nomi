package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// privateOnces is a file with a private `once` and a private `embed`, which
// is a `once` too, and the file it embeds.
var privateOnces = map[string]string{
	"nomi.toml": "[package]\nname = \"m\"\n",
	"x.txt":     "hello\n",
	"eng.nomi": "import std::embed.Files\n\nprivate once v: Int = 3\n\nprivate embed data `x.txt`\n\n" +
		"pub fn size() -> Int\n    Files.read(data, \"x.txt\") |> Result.map(fn b -> Bytes.length(b)) |> Result.with_default(0)\nend\n",
}

// No other file of the module reads the private `once`, a test file named
// after eng.nomi included: the front end rejects it, so the builder's rule
// that refuses a private `once` across files admits no read the language
// refuses.
func TestIRPrivateOnce_NoOtherFileReadsIt(t *testing.T) {
	for _, name := range []string{"other.nomi", "eng_test.nomi"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for file, src := range withFiles(privateOnces, map[string]string{name: "import eng\n\npub fn v() -> Int\n    eng.v\nend\n"}) {
				if err := os.WriteFile(filepath.Join(dir, file), []byte(src), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Analyze(filepath.Join(dir, name))
			if err == nil {
				t.Fatalf("the front end admits %s's read of a private once", name)
			}
			if !strings.Contains(err.Error(), "`v` is private to `eng.nomi`") || strings.Count(err.Error(), "\n") > 1 { // the one error and its help
				t.Fatalf("the front end rejects the read for another reason: %v", err)
			}
		})
	}
}
