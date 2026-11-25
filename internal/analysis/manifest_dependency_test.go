package analysis_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/pkgfetch/pkgfetchtest"
	"github.com/nomi-language/nomi/std"
)

// appUsingKit is a project app whose main.nomi calls kit.two(), with kit
// named by entry, a [dependencies] value.
func appUsingKit(t *testing.T, base, entry string) (app string) {
	t.Helper()
	app = filepath.Join(base, "app")
	writeTestFile(t, filepath.Join(app, "nomi.toml"), "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\nkit = "+entry+"\n")
	writeTestFile(t, filepath.Join(app, "main.nomi"), kitMain)
	return app
}

// kitMain is app's main.nomi.
const kitMain = "import\n    std::io\n    kit\nend\n\nfn main()\n    io.print(\"${kit.two()}\")\nend\n"

// kitFile is kit's kit.nomi.
const kitFile = "pub fn two() -> Int\n    2\nend\n"

// buildErrors builds the main.nomi of the project at root and answers its
// errors.
func buildErrors(t *testing.T, root string) []analysis.TypeError {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(src)))
	lib := loadStd()
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, root, std.MakeLoader())
	if fa == nil {
		t.Fatal("no analysis")
	}
	return append(entryErrors(fa), analysis.CheckTypes(fa, nodes)...)
}

// wantOneManifestError fails t unless errs is one `manifest` error whose
// place and message (placedMessage) are want.
func wantOneManifestError(t *testing.T, errs []analysis.TypeError, want string) analysis.TypeError {
	t.Helper()
	if len(errs) != 1 || errs[0].Code != analysis.ManifestCode || placedMessage(errs[0]) != want {
		t.Fatalf("the errors are %+v\nwant one %s error:\n%s", errs, analysis.ManifestCode, want)
	}
	return errs[0]
}

// placedMessage is e's file, line and message, as `path:line: message`,
// then the message of each related location after a `; `: the entry that
// names a reached package.
func placedMessage(e analysis.TypeError) string {
	s := fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Message)
	for _, r := range e.Related {
		s += "; " + r.Message
	}
	return s
}

// A dependency's nomi.toml is held to the schema and the checks the
// project's own is held to, by the same code: a problem in it stops the
// build with one `manifest` error at its line in that file, which says
// which [dependencies] entry names the package. One named by `dir` is the
// reader's to edit, and gets the fixes the project's own nomi.toml gets,
// which here are none.
func TestDependencyManifest_ADirDependencyIsCheckedAsTheProjectIs(t *testing.T) {
	for _, c := range []struct {
		name, manifest string
		want           string // after kit's nomi.toml path
	}{
		{"a misspelled table", "[package]\nname = \"kit\"\n\n[dependancies]\nother = { dir = \"../other\" }\n",
			":4: nomi.toml has no [dependancies] table; did you mean [dependencies]?"},
		{"no package name", "[package]\nentry_points = [\"main\"]\n",
			":1: [package] name is required (non-empty, non-whitespace)"},
		{"an unknown key", "[package]\nname = \"kit\"\nauthors = [\"me\"]\n",
			":3: [package] has no key `authors`; its keys are `name`, `nomi`, `entry_points`"},
		{"an [effects.self] of the wrong shape", "[package]\nname = \"kit\"\n\n[effects.self]\neffects = [\"fs\"]\n",
			":5: [effects.self] effects: \"fs\" is not a std module; write one as an import does, such as \"std::fs\""},
	} {
		t.Run(c.name, func(t *testing.T) {
			pkgfetchtest.Cache(t)
			base := t.TempDir()
			app := appUsingKit(t, base, "{ dir = \"../kit\" }")
			kit := filepath.Join(base, "kit")
			writeTestFile(t, filepath.Join(kit, "nomi.toml"), c.manifest)
			writeTestFile(t, filepath.Join(kit, "kit.nomi"), kitFile)
			te := wantOneManifestError(t, buildErrors(t, app), filepath.Join(kit, "nomi.toml")+c.want+
				"; [dependencies] kit of "+filepath.Join(app, "nomi.toml")+" names this package")
			if te.Fix != nil {
				t.Errorf("the error carries the fix %+v; the project's own nomi.toml gets none for it", te.Fix)
			}
		})
	}
}

// A dependency of a dependency is checked the same way, and the error says
// which package's entry names it and which of the project's entries leads
// there.
func TestDependencyManifest_ADependencyOfADependency(t *testing.T) {
	pkgfetchtest.Cache(t)
	base := t.TempDir()
	app := filepath.Join(base, "app")
	writeTestFile(t, filepath.Join(app, "nomi.toml"), "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\nmid = { dir = \"../mid\" }\n")
	writeTestFile(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    mid\nend\n\nfn main()\n    io.print(\"${mid.three()}\")\nend\n")
	mid := filepath.Join(base, "mid")
	writeTestFile(t, filepath.Join(mid, "nomi.toml"), "[package]\nname = \"mid\"\n\n[dependencies]\nkit = { dir = \"../kit\" }\n")
	writeTestFile(t, filepath.Join(mid, "mid.nomi"), "import kit\n\npub fn three() -> Int\n    kit.two() + 1\nend\n")
	kit := filepath.Join(base, "kit")
	writeTestFile(t, filepath.Join(kit, "nomi.toml"), "[package]\nname = \"kit\"\n\n[tool]\nout = \"gen\"\n")
	writeTestFile(t, filepath.Join(kit, "kit.nomi"), kitFile)
	wantOneManifestError(t, buildErrors(t, app), filepath.Join(kit, "nomi.toml")+
		":4: nomi.toml has no [tool] table; its tables are [dependencies], [effects], [i18n], [money], [package], and its tools' tables [kit]"+
		"; [dependencies] kit of "+filepath.Join(mid, "nomi.toml")+" names this package, which the project reaches through its [dependencies] mid")
}

// A fetched dependency's nomi.toml is checked as one on disk is. The error
// names the file in the package cache and the repository, directory and
// version it came from, says the reader cannot edit it, and carries no fix.
func TestDependencyManifest_AFetchedDependencyIsCheckedAndOffersNoFix(t *testing.T) {
	cache := pkgfetchtest.Cache(t)
	pkgfetchtest.CacheRepo(t, cache, "example.com/tools", "v1.2.0", map[string]string{
		"README.md":       "tools\n",
		"kit/nomi.toml":   "[package]\nname = \"kit\"\nlicense = \"MIT\"\n",
		"kit/kit.nomi":    kitFile,
		"other/nomi.toml": "[package]\nname = \"other\"\n",
	})
	app := appUsingKit(t, t.TempDir(), "{ repo = \"example.com/tools\", subdir = \"kit\", version = \"v1.2.0\" }")
	kitToml := filepath.Join(pkgcache.RepoDir(cache, "example.com/tools", "v1.2.0"), "kit", "nomi.toml")
	te := wantOneManifestError(t, buildErrors(t, app), kitToml+
		":3: [package] has no key `license`; its keys are `name`, `nomi`, `entry_points`"+
		"; this is the package in kit of example.com/tools at v1.2.0, which [dependencies] kit of "+filepath.Join(app, "nomi.toml")+
		" names; it was fetched into the package cache, so it is not yours to edit: take another version of it, or tell its authors")
	if te.Fix != nil {
		t.Errorf("the error carries the fix %+v; a fetched package's nomi.toml is not the reader's to edit", te.Fix)
	}
}

// A workspace member's nomi.toml is read with the workspace, from any
// member's build, and checked as a dependency's is: a member is a package,
// so one with no [package] name is refused by the check every nomi.toml
// gets.
func TestDependencyManifest_AMemberInErrorStopsAnotherMembersBuild(t *testing.T) {
	for _, c := range []struct {
		name, manifest, want string
	}{
		{"a misspelled table", "[package]\nname = \"shop\"\n\n[dependency]\nkit = { dir = \"../kit\" }\n",
			":4: nomi.toml has no [dependency] table; did you mean [dependencies]?"},
		{"no package name", "[dependencies]\n", ":1: [package] name is required (non-empty, non-whitespace)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, errs := memberBuildErrors(t, "members = [\n    \"billing\",\n    \"shop\",\n]\n", func(root string) {
				writeTestFile(t, filepath.Join(root, "shop", "nomi.toml"), c.manifest)
			})
			wantOneManifestError(t, errs, filepath.Join(root, "shop", "nomi.toml")+c.want+
				"; members entry \"shop\" of "+filepath.Join(root, pkgcache.WorkspaceName)+" names this package")
		})
	}
}

// A dependency's nomi.toml may hold its tools' tables, each named for the
// package or a key of its own [dependencies], and an [effects.self], which
// the effects lists of a project read: the build has no error, and the
// dependency's lists are read.
func TestDependencyManifest_AValidDependencyWithToolTablesAndEffectsBuilds(t *testing.T) {
	pkgfetchtest.Cache(t)
	base := t.TempDir()
	app := appUsingKit(t, base, "{ dir = \"../kit\" }")
	kit := filepath.Join(base, "kit")
	writeTestFile(t, filepath.Join(kit, "nomi.toml"), "[package]\nname = \"kit\"\n\n[dependencies]\nhelper = { dir = \"../helper\" }\n\n"+
		"[kit]\nout = \"gen\"\n\n[helper]\nlevel = 2\n\n[effects.self]\neffects = [\"std::io\"]\n")
	writeTestFile(t, filepath.Join(kit, "kit.nomi"), kitFile)
	writeTestFile(t, filepath.Join(base, "helper", "nomi.toml"), "[package]\nname = \"helper\"\n")
	writeTestFile(t, filepath.Join(base, "helper", "helper.nomi"), "pub fn one() -> Int\n    1\nend\n")
	if errs := buildErrors(t, app); len(errs) != 0 {
		t.Fatalf("the build's errors are %+v; want none", errs)
	}
	m, err := analysis.LoadManifest(kit)
	if err != nil || m.Effects == nil || m.Effects.Self == nil || strings.Join(m.Effects.Self.Effects, ",") != "std::io" {
		t.Fatalf("kit's manifest is %+v, %v; want its [effects.self] std read", m, err)
	}
}
