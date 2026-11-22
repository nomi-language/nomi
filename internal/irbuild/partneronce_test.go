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
	"nomi.toml": "[module]\nname = \"m\"\n",
	"x.txt":     "hello\n",
	"eng.nomi": "import std::embed.Files\n\nonce v: Int = 3\n\nembed data `x.txt`\n\n" +
		"pub fn size() -> Int\n    Files.read(data, \"x.txt\") |> Result.map(fn b -> Bytes.length(b)) |> Result.with_default(0)\nend\n",
}

// A partner test reads its file's private `once` and private `embed`, as
// it calls its private functions (spec §14 *Visibility*).
func TestIRPartnerTest_ReadsItsFilesPrivateOnce(t *testing.T) {
	out, exit := runPartnerTests(t, withFiles(privateOnces, map[string]string{"eng_test.nomi": `import
    std::embed.Files
    eng
end

test "private onces"
    assert eng.v == 3
    assert Files.read(eng.data, "x.txt") == Ok(String.to_bytes("hello\n"))
end
`}), "eng_test.nomi")
	if exit != 0 || strings.Contains(out, "FAIL") {
		t.Fatalf("the cases fail (exit %d):\n%s", exit, out)
	}
}

// Another file of the module may not read the private `once`: the front
// end rejects it, so the builder's partner rule admits no read the language
// refuses.
func TestIRPartnerTest_AnotherFileCannotReadAPrivateOnce(t *testing.T) {
	dir := t.TempDir()
	for name, src := range withFiles(privateOnces, map[string]string{"other.nomi": "import eng\n\npub fn v() -> Int\n    eng.v\nend\n"}) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Analyze(filepath.Join(dir, "other.nomi"))
	if err == nil {
		t.Fatal("the front end admits another file's read of a private once")
	}
	if !strings.Contains(err.Error(), "file 'eng' has no member 'v'") || strings.Count(err.Error(), "\n") > 0 {
		t.Fatalf("the front end rejects the read for another reason: %v", err)
	}
}
