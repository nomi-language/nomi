package frontend_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const embedManifest = "[module]\nname = \"site\"\nentry_points = [\"main\"]\n"

// embedFiles is a project whose main.nomi holds decl, beside a static
// folder, a hidden file, a nested module and a template.
func embedFiles(decl string) map[string]string {
	return map[string]string{
		"nomi.toml":           embedManifest,
		"main.nomi":           "import std::embed.Files\n\n" + decl + "\n\nfn main() {\n    _ = Files.paths(site)\n}\n",
		"static/index.html":   "<h1>hi</h1>\n",
		"static/css/site.css": "body {}\n",
		"static/.keep":        "",
		"static/_draft.html":  "draft\n",
		"empty/.keep":         "",
		"tools/nomi.toml":     "[module]\nname = \"tools\"\n",
		"tools/gen.nomi":      "fn main() {}\n",
		"page.html":           "<p>page</p>\n",
	}
}

func TestEmbed_AcceptedPatterns(t *testing.T) {
	for _, decl := range []string{
		"embed site `static`",
		"embed site `static/*.html page.html`",
		"embed site `all:static`",
		"embed site `static/.keep`",
		"embed site `\n    static\n    page.html\n    `",
		"pub embed site `static static/index.html`",
		"embed site `*.html`",
	} {
		if ds := checkShop(t, embedFiles(decl)); len(ds) > 0 {
			t.Errorf("%s is refused:\n%s", decl, strings.Join(ds, "\n"))
		}
	}
}

// Each malformed pattern, and each pattern that names nothing to embed, is
// an error at the pattern. Each case fails if the source starts being
// admitted.
func TestEmbed_EachPatternErrorRefuses(t *testing.T) {
	cases := []struct{ decl, want string }{
		{"embed site `static/*.png`", "main.nomi:3:13: the `embed` pattern `static/*.png` matches no file"},
		{"embed site `/static`", "main.nomi:3:13: the `embed` pattern `/static` starts with `/`\n  help: a pattern names files from the module root; remove the `/`"},
		{"embed site `static/`", "main.nomi:3:13: the `embed` pattern `static/` ends with `/`\n  help: a directory's name embeds every file under it; remove the `/`"},
		{"embed site `../x`", "main.nomi:3:13: the `embed` pattern `../x` names `..`\n  help: an `embed` reads only files in its own module, named from the module root"},
		{"embed site `./static`", "main.nomi:3:13: the `embed` pattern `./static` names `.`"},
		{"embed site `static//a`", "main.nomi:3:13: the `embed` pattern `static//a` has an empty name between two `/`"},
		{"embed site `static/**`", "main.nomi:3:13: the `embed` pattern `static/**` holds `**`\n  help: a directory's name embeds every file under it: write the directory"},
		{`embed site ` + "`static\\a`", "main.nomi:3:13: the `embed` pattern `static\\a` holds `\\`"},
		{"embed site `static/[a`", "main.nomi:3:13: the `embed` pattern `static/[a` is not a valid pattern"},
		{"embed site `empty`", "main.nomi:3:13: cannot embed `empty`: it holds no file to embed\n  help: files whose names start with `.` or `_` are left out of a directory; write `all:empty` to embed them too"},
		{"embed site `tools`", "main.nomi:3:13: cannot embed `tools`: it is another module, with its own nomi.toml"},
		{"embed site `tools/gen.nomi`", "main.nomi:3:13: cannot embed files under `tools`: it is another module, with its own nomi.toml"},
		{"embed site `all:`", "main.nomi:3:13: an `embed` pattern after `all:` is empty"},
		{"embed site `static static/*.txt`", "main.nomi:3:20: the `embed` pattern `static/*.txt` matches no file"},
	}
	for _, c := range cases {
		ds := checkShop(t, embedFiles(c.decl))
		if len(ds) == 0 {
			t.Errorf("%s is accepted; want %q", c.decl, c.want)
			continue
		}
		if !strings.HasPrefix(ds[0], c.want) {
			t.Errorf("%s:\n%s\nwant:\n%s", c.decl, strings.Join(ds, "\n"), c.want)
		}
	}
}

// A pattern whose base does not exist (a frontend's build folder before its
// first build) is accepted: its tree holds the base as an empty directory,
// and the pattern is a warning, not an error (vmhost's Program.Warnings).
func TestEmbed_APatternWhoseBaseDoesNotExistIsAccepted(t *testing.T) {
	for _, decl := range []string{
		"embed site `nope`",
		"embed site `web/dist`",
		"embed site `web/dist/*.js`",
		"embed site `static web/dist`",
	} {
		if ds := checkShop(t, embedFiles(decl)); len(ds) > 0 {
			t.Errorf("%s is refused:\n%s", decl, strings.Join(ds, "\n"))
		}
	}
}

// A symbolic link is never embedded: named, or met in a directory.
func TestEmbed_ASymbolicLinkIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symbolic link needs a privilege Windows does not give a test")
	}
	for _, c := range []struct{ decl, want string }{
		{"embed site `link.html`", "main.nomi:3:13: cannot embed `link.html`: it is a symbolic link"},
		{"embed site `linked`", "main.nomi:3:13: cannot embed `linked/to.html`: it is a symbolic link"},
	} {
		files := embedFiles(c.decl)
		dir := t.TempDir()
		for name, text := range files {
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink("page.html", filepath.Join(dir, "link.html")); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "linked"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../page.html", filepath.Join(dir, "linked", "to.html")); err != nil {
			t.Fatal(err)
		}
		ds := checkDir(t, dir)
		if len(ds) == 0 || !strings.HasPrefix(ds[0], c.want) {
			t.Errorf("%s:\n%s\nwant %q", c.decl, strings.Join(ds, "\n"), c.want)
		}
	}
}

// A pattern names files from the module root, as an import path does,
// whatever directory the declaring file is in: moving the file keeps its
// patterns valid, and a nested file's `static` is the root's.
func TestEmbed_PatternsAreFromTheModuleRoot(t *testing.T) {
	project := func(declDir string) map[string]string {
		module := "pages"
		if declDir != "" {
			module = strings.ReplaceAll(declDir, "/", "::") + "::pages"
		}
		files := map[string]string{
			"nomi.toml":     embedManifest,
			"main.nomi":     "import {\n    std::embed.Files\n    " + module + "\n}\n\nfn main() {\n    _ = Files.paths(pages.site)\n}\n",
			"assets/x.css":  "body {}\n",
			"static/a.html": "a\n",
		}
		files[strings.TrimPrefix(declDir+"/pages.nomi", "/")] = "pub embed site `assets static`\n"
		return files
	}
	for _, dir := range []string{"", "web", "web/deep"} {
		if ds := checkShop(t, project(dir)); len(ds) > 0 {
			t.Errorf("pages.nomi in %q is refused:\n%s", dir, strings.Join(ds, "\n"))
		}
	}
	// A nested file's `static` names the root's static, never web/static,
	// and naming web/static so is an error, not a folder yet to be made.
	files := project("web")
	delete(files, "static/a.html")
	files["web/static/b.html"] = "b\n"
	ds := checkShop(t, files)
	want := "web/pages.nomi:1:24: the `embed` pattern `static` matches no file\n  help: a pattern names files from the module root, as an import path does"
	if len(ds) == 0 || ds[0] != want {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(ds, "\n"), want)
	}
}
