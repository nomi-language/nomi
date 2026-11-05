package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A literal path read from another file's `pub embed` is checked against
// the files that declaration holds, from source and against interfaces
// alike: the file set travels in the declaring file's interface, in the
// `once`'s value its expansion keeps.
func TestEmbeddedPathsCheckTheSameAgainstInterfaces(t *testing.T) {
	dir := t.TempDir()
	p := incrementalProject{name: "embedded paths", entry: "main", dir: dir, texts: map[string]string{
		"main": "import {\n    std::embed.Files\n    web::pages\n}\n\nfn main() {\n" +
			"    _ = Files.read_text(pages.assets, \"static/indx.html\")\n" +
			"    _ = Files.list(Files.sub(pages.assets, \"static\")!, \"css\")\n}\n",
		"web/pages": "pub embed assets `static`\n",
	}}
	files := map[string]string{
		"nomi.toml":           "[module]\nname = \"site\"\nentry_points = [\"main\"]\n",
		"static/index.html":   "<h1>hi</h1>\n",
		"static/css/site.css": "body {}\n",
	}
	for key, text := range p.texts {
		files[key+".nomi"] = text
	}
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, got := interfaceDumps(p, p.texts)
	if want != got {
		t.Fatalf("checked against interfaces differs from a full check:\n%s", dumpDiff(want, got))
	}
	if !strings.Contains(want, "`static/indx.html` is not embedded in `assets`") {
		t.Fatalf("the full check does not report the literal:\n%s", want)
	}
	if strings.Contains(want, "`css`") {
		t.Fatalf("an embedded directory is reported:\n%s", want)
	}
}
