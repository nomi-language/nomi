package vmhost_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A call into another file leaves out a parameter whose default is a typed
// literal, from a file that does not import the literal's type. The default
// cannot be lowered at the call, where the tag names nothing, so it is the
// declaring file's accessor's. The tag was not among the names a default
// mentions, so the call lowered it in place and the program was blocked:
// "a typed literal: no handler". std/storage_testing's `Memory.new()`, whose
// base is the literal Url`https://storage.test/`, hit it from a program that
// does not import Url.
func TestSiblingDefault_ATypedLiteralTheCallerCannotName(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"nomi.toml": "[module]\nname = \"demo\"\nentry_points = [\"main\"]\n",
		"links.nomi": `import std::url.Url

pub struct Box
    base: Url
end

impl Box
    pub fn new(base: Url = Url` + "`https://a.test/`" + `) -> Box
        Box{base:}
    end
end

pub fn link(name: String, base: Url = Url` + "`https://b.test/`" + `) -> String
    "${Url.join_path(base, [name])}"
end
`,
		"main.nomi": `import
    std::io
    links.{self, Box}
end

fn main()
    io.print("${Box.new().base}")
    io.print(links.link("x"))
end
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := vmhost.Load(filepath.Join(dir, "main.nomi"))
	if err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if want := "https://a.test/\nhttps://b.test/x\n"; out.String() != want {
		t.Errorf("output %q, want %q", out.String(), want)
	}
}
