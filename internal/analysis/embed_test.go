package analysis_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// An `embed` in a file prepared with no directory to read from is refused
// at its keyword. It fails if such a file starts being admitted.
func TestEmbed_NoDirectoryIsRefused(t *testing.T) {
	_, errs := checkSourceWithStdlib("embed site `static`\n")
	got := errorsAt(errs, 1, 1)
	want := "`embed site` has no directory to read files from: this file is not on disk or in a module"
	if len(got) != 1 || !strings.HasPrefix(got[0], want) {
		t.Fatalf("errors at 1:1 = %q, want [%q]; all: %v", got, want, errs)
	}
}

// An `embed` expands into the `once` it names, whose value holds every
// file its patterns match, in path order, read from the site's files.
func TestEmbed_ExpandsIntoAOnceHoldingItsFiles(t *testing.T) {
	src := "/// The site.\npub embed site `web static/*.css`\n"
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	files := analysis.MapEmbedFS{
		"web/index.html":    "<h1>hi</h1>",
		"web/.hidden":       "no",
		"web/img/a.png":     "\x89PNG",
		"static/site.css":   "body {}",
		"static/site.js":    "no",
		"other/ignored.txt": "no",
	}
	site := &analysis.ExpansionSite{ModulePath: "main", Embed: files}
	out := analysis.ExpandDeclarations(nodes, site)
	var once *ast.OnceBinding
	for _, n := range out {
		if o, ok := n.(*ast.OnceBinding); ok {
			once = o
		}
	}
	if once == nil || once.Name != "site" || !once.Public || once.Doc != "The site." {
		t.Fatalf("no `once site` in %#v", out)
	}
	info := analysis.EmbedOf(once)
	if info == nil {
		t.Fatal("the once has no embed")
	}
	var paths []string
	for _, f := range info.Files {
		paths = append(paths, f.Path)
	}
	if got := strings.Join(paths, " "); got != "static/site.css web/img/a.png web/index.html" {
		t.Fatalf("files %q", got)
	}
	if info.TotalSize() != int64(len("body {}\x89PNG<h1>hi</h1>")) {
		t.Fatalf("size %d", info.TotalSize())
	}
	if len(info.Matches) != 2 || strings.Join(info.Matches[0], " ") != "web" || strings.Join(info.Matches[1], " ") != "static/site.css" {
		t.Fatalf("matches %q", info.Matches)
	}
	call, ok := once.Value.(*ast.Call)
	if !ok || len(call.Args) != 1 {
		t.Fatalf("value %#v", once.Value)
	}
	value, ok := call.Args[0].(*ast.EmbeddedFiles)
	if !ok || len(value.Files) != 3 || value.Files[1].Source != "web/img/a.png" || value.Files[1].Size != 4 || value.Files[1].Sum != sha256Hex("\x89PNG") {
		t.Fatalf("argument %#v", call.Args[0])
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// The language server's site finds the files without hashing them.
func TestEmbed_SkippingContentsKeepsSizes(t *testing.T) {
	nodes, err := parser.Parse(lexer.Lex("embed site `a.txt`\n"))
	if err != nil {
		t.Fatal(err)
	}
	site := &analysis.ExpansionSite{ModulePath: "main", Embed: analysis.MapEmbedFS{"a.txt": "hello"}, SkipEmbedContents: true}
	for _, n := range analysis.ExpandDeclarations(nodes, site) {
		if o, ok := n.(*ast.OnceBinding); ok {
			value := o.Value.(*ast.Call).Args[0].(*ast.EmbeddedFiles)
			if len(value.Files) != 1 || value.Files[0].Sum != "" || value.Files[0].Size != 5 {
				t.Fatalf("files %#v", value.Files)
			}
			return
		}
	}
	t.Fatal("no once")
}
