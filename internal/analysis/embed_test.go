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
	if !ok || len(call.Args) != 2 {
		t.Fatalf("value %#v", once.Value)
	}
	// No pattern's base is missing, so the tree has no empty directory.
	if empty, ok := call.Args[1].(*ast.ListLit); !ok || len(empty.Items) != 0 || len(info.Missing) != 0 {
		t.Fatalf("empty directories %#v, missing %v", call.Args[1], info.Missing)
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

// A pattern whose base does not exist expands into an empty directory of
// the tree and a warning at the pattern, which a build answers as an
// error. A glob that matches nothing in a base that exists, and a pattern
// naming a path beside a nested declaring file, are not missing: they stay
// errors (frontend's TestEmbed_EachPatternErrorRefuses and
// TestEmbed_PatternsAreFromTheModuleRoot check their messages).
func TestEmbed_APatternWhoseBaseDoesNotExistIsAWarning(t *testing.T) {
	expand := func(src, modulePath string) (*analysis.EmbedInfo, *ast.Call) {
		t.Helper()
		nodes, err := parser.Parse(lexer.Lex(src))
		if err != nil {
			t.Fatal(err)
		}
		files := analysis.MapEmbedFS{"static/a.css": "a", "web/static/b.css": "b"}
		for _, n := range analysis.ExpandDeclarations(nodes, &analysis.ExpansionSite{ModulePath: modulePath, Embed: files}) {
			if o, ok := n.(*ast.OnceBinding); ok {
				call, _ := o.Value.(*ast.Call)
				return analysis.EmbedOf(o), call
			}
		}
		t.Fatalf("no once in %q", src)
		return nil, nil
	}
	info, call := expand("embed site `static web/dist/*.js`\n", "main")
	if len(info.Missing) != 1 || info.Missing[0] != 1 || strings.Join(info.MissingBases(), " ") != "web/dist" {
		t.Fatalf("missing %v, bases %v", info.Missing, info.MissingBases())
	}
	if empty, ok := call.Args[1].(*ast.ListLit); !ok || len(empty.Items) != 1 || empty.Items[0].(*ast.StringLit).Value != "web/dist" {
		t.Fatalf("empty directories %#v", call.Args[1])
	}
	ws := info.Warnings()
	want := "the `embed` pattern `web/dist/*.js` matches no file: `web/dist` does not exist, so `site` holds nothing under it until it does"
	if len(ws) != 1 || ws[0].Message != want || ws[0].Code != analysis.EmbedMissingCode || ws[0].Line != 1 || ws[0].Col != 20 {
		t.Fatalf("warnings %+v", ws)
	}
	if errs := info.MissingErrors(); len(errs) != 1 || errs[0].Message != "the `embed` pattern `web/dist/*.js` matches no file: `web/dist` does not exist" {
		t.Fatalf("build errors %+v", errs)
	}
	if only, _ := expand("embed site `web/dist`\n", "main"); len(only.Warnings()) != 1 ||
		!strings.HasSuffix(only.Warnings()[0].Message, "so `site` is empty until it does") {
		t.Fatalf("an embed with only a missing base: %+v", only.Warnings())
	}
	for _, c := range []struct{ src, module string }{
		{"embed site `static/*.png`\n", "main"},
		{"embed site `static/b.css`\n", "web/main"},
	} {
		if in, _ := expand(c.src, c.module); len(in.Missing) != 0 || len(in.Warnings()) != 0 {
			t.Errorf("%s in %s is a missing base, not an error", strings.TrimSpace(c.src), c.module)
		}
	}
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
