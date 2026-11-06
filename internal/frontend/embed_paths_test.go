package frontend_test

import (
	"strings"
	"testing"
)

// A std::embed `Files` call whose path is a literal is checked against the
// files its `embed` holds; one whose path is built at run time is not.

// embedPathFiles is a project whose main.nomi runs body, beside a local
// `embed site` and web/pages.nomi's `pub embed assets`, both of the root's
// static folder and page.html.
func embedPathFiles(body string) map[string]string {
	return map[string]string{
		"nomi.toml": embedManifest,
		"main.nomi": "import {\n    std::embed.Files\n    web::pages\n}\n\nembed site `static page.html`\n\n" +
			"fn main() {\n    name = \"x\"\n    " + body + "\n    use(name)\n    _ = Files.paths(pages.assets)\n}\n\nfn use(_name: String) {}\n",
		"web/pages.nomi":          "pub embed assets `static page.html`\n",
		"static/index.html":       "<h1>hi</h1>\n",
		"static/css/site.css":     "body {}\n",
		"static/docs/a/guide.txt": "guide\n",
		"page.html":               "<p>page</p>\n",
	}
}

func TestEmbedPaths_ALiteralNamingWhatIsEmbeddedChecks(t *testing.T) {
	for _, body := range []string{
		`_ = Files.read(site, "page.html")`,
		`_ = Files.read_text(site, "static/index.html")`,
		`_ = Files.read_text(site, "./static//index.html")`,
		`_ = Files.metadata(site, "static/css/site.css")`,
		`_ = Files.metadata(site, "static")`,
		`_ = Files.metadata(site, ".")`,
		`_ = Files.list(site, "static")`,
		`_ = Files.list(site, ".")`,
		`_ = Files.list(site, "")`,
		`_ = Files.walk(site, "static/docs")`,
		`_ = Files.sub(site, "static")`,
		`_ = Files.read_text(Files.sub(site, "static")!, "css/site.css")`,
		`_ = Files.read_text(Files.sub(Files.sub(site, "static")!, "docs")!, "a/guide.txt")`,
		`_ = Files.read_text(pages.assets, "static/index.html")`,
		`_ = Files.read_text(Files.sub(pages.assets, "static")!, "index.html")`,
		`_ = site |> Files.read_text("page.html")`,
		`_ = site |> Files.sub("static")! |> Files.read_text("index.html")`,
		`_ = Files.read_text(files: site, p: "page.html")`,
		// A path built at run time, and exists and paths, are not checked.
		`_ = Files.read_text(site, "static/${name}.html")`,
		`_ = Files.read_text(site, name)`,
		`_ = Files.exists(site, "nope")`,
		`_ = Files.paths(site)`,
	} {
		if ds := checkShop(t, embedPathFiles(body)); len(ds) > 0 {
			t.Errorf("%s is refused:\n%s", body, strings.Join(ds, "\n"))
		}
	}
}

// Each literal naming what the tree does not hold is an error at the
// literal. Each case fails if the source starts being admitted.
func TestEmbedPaths_ALiteralNamingWhatIsNotEmbeddedIsRefused(t *testing.T) {
	cases := []struct{ body, want string }{
		{`_ = Files.read_text(site, "static/indx.html")`,
			"main.nomi:10:31: `static/indx.html` is not embedded in `site`\n  help: did you mean `static/index.html`?"},
		{`_ = Files.read(site, "nope.bin")`, "main.nomi:10:26: `nope.bin` is not embedded in `site`"},
		{`_ = Files.read(site, "static")`, "main.nomi:10:26: `static` is a directory in `site`, not a file"},
		{`_ = Files.metadata(site, "statc")`,
			"main.nomi:10:30: `statc` is not embedded in `site`\n  help: did you mean `static`?"},
		{`_ = Files.list(site, "page.html")`, "main.nomi:10:26: `page.html` is a file in `site`, not a directory"},
		{`_ = Files.list(site, "static/cs")`,
			"main.nomi:10:26: `static/cs` is not an embedded directory of `site`\n  help: did you mean `static/css`?"},
		{`_ = Files.walk(site, "public")`, "main.nomi:10:26: `public` is not an embedded directory of `site`"},
		{`_ = Files.sub(site, "assets")`, "main.nomi:10:25: `assets` is not an embedded directory of `site`"},
		{`_ = Files.read_text(Files.sub(site, "static")!, "static/index.html")`,
			"main.nomi:10:53: `static/index.html` is not embedded in `static` of `site`"},
		{`_ = Files.list(Files.sub(Files.sub(site, "static")!, "docs")!, "zz")`,
			"main.nomi:10:68: `zz` is not an embedded directory of `docs` of `static` of `site`"},
		{`_ = Files.read_text(pages.assets, "static/index.htm")`,
			"main.nomi:10:39: `static/index.htm` is not embedded in `assets`\n  help: did you mean `static/index.html`?"},
		{`_ = site |> Files.read_text("pages.html")`,
			"main.nomi:10:33: `pages.html` is not embedded in `site`\n  help: did you mean `page.html`?"},
		{`_ = site |> Files.sub("static")! |> Files.read("site.css")`,
			"main.nomi:10:52: `site.css` is not embedded in `static` of `site`"},
		{`_ = Files.read_text(site, "/page.html")`,
			"main.nomi:10:31: `/page.html` is not an embedded path: an embedded path is relative to the root of the embedded tree"},
		{`_ = Files.read_text(site, "../page.html")`,
			"main.nomi:10:31: `../page.html` is not an embedded path: the path leaves the embedded tree"},
	}
	for _, c := range cases {
		ds := checkShop(t, embedPathFiles(c.body))
		if len(ds) == 0 {
			t.Errorf("%s is accepted; want %q", c.body, c.want)
			continue
		}
		if ds[0] != c.want {
			t.Errorf("%s:\n%s\nwant:\n%s", c.body, strings.Join(ds, "\n"), c.want)
		}
	}
}

// A literal path at or under a pattern's base that does not exist yet is
// not checked, since the files are not there to check it against; the
// rest of the tree still is. Each refused case fails if the source starts
// being admitted.
func TestEmbedPaths_APathUnderABaseThatDoesNotExistIsNotChecked(t *testing.T) {
	files := func(body string) map[string]string {
		f := embedPathFiles(body)
		f["main.nomi"] = strings.Replace(f["main.nomi"], "embed site `static page.html`", "embed site `static web/dist`", 1)
		return f
	}
	for _, body := range []string{
		`_ = Files.read_text(site, "web/dist/index.html")`,
		`_ = Files.read_text(Files.sub(site, "web/dist")!, "index.html")`,
		`_ = Files.walk(Files.sub(site, "web")!, "dist/assets")`,
		`_ = Files.list(site, "web/dist/assets")`,
		`_ = Files.list(site, "web")`,
		`_ = Files.read_text(site, "static/index.html")`,
	} {
		if ds := checkShop(t, files(body)); len(ds) > 0 {
			t.Errorf("%s is refused:\n%s", body, strings.Join(ds, "\n"))
		}
	}
	for _, c := range []struct{ body, want string }{
		{`_ = Files.read_text(site, "static/indx.html")`,
			"main.nomi:10:31: `static/indx.html` is not embedded in `site`\n  help: did you mean `static/index.html`?"},
		{`_ = Files.read(site, "web")`, "main.nomi:10:26: `web` is a directory in `site`, not a file"},
		{`_ = Files.list(site, "public")`, "main.nomi:10:26: `public` is not an embedded directory of `site`"},
	} {
		ds := checkShop(t, files(c.body))
		if len(ds) == 0 {
			t.Errorf("%s is accepted; want %q", c.body, c.want)
			continue
		}
		if ds[0] != c.want {
			t.Errorf("%s:\n%s\nwant:\n%s", c.body, strings.Join(ds, "\n"), c.want)
		}
	}
}
