package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/pkgfetch/pkgfetchtest"
	"github.com/nomi-language/nomi/std"
)

// memberMain is a member's main.nomi, which names nothing outside std.
const memberMain = "import std::io\n\nfn main()\n    io.print(\"hi\")\nend\n"

// memberBuildErrors writes a workspace whose nomi-workspace.toml is ws and
// whose member billing holds memberMain, runs setup on the root, builds
// billing's main.nomi and answers its errors.
func memberBuildErrors(t *testing.T, ws string, setup func(root string)) (root string, errs []analysis.TypeError) {
	t.Helper()
	pkgfetchtest.Cache(t)
	root = filepath.Join(t.TempDir(), "ws")
	writeTestFile(t, filepath.Join(root, pkgcache.WorkspaceName), ws)
	billing := filepath.Join(root, "billing")
	writeTestFile(t, filepath.Join(billing, "nomi.toml"), "[package]\nname = \"billing\"\nentry_points = [\"main\"]\n")
	writeTestFile(t, filepath.Join(billing, "main.nomi"), memberMain)
	if setup != nil {
		setup(root)
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(memberMain))
	lib := loadStd()
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, billing, std.MakeLoader())
	if fa == nil {
		t.Fatal("no analysis")
	}
	return root, fa.TypeErrors
}

// A problem in nomi-workspace.toml stops a member's build with one
// `manifest` error at its line in the file, as a nomi.toml in error does.
// Its keys are checked by the reporting nomi.toml's keys use.
func TestWorkspaceFile_AProblemStopsAMembersBuild(t *testing.T) {
	for _, c := range []struct {
		name, ws string
		setup    func(root string)
		want     string // after the file's path
	}{
		{"an unknown key", "member = [\"billing\"]\n", nil,
			":1: nomi-workspace.toml has no key `member`; did you mean `members`?"},
		{"an unknown table", "members = [\"billing\"]\n\n[package]\nname = \"ws\"\n", nil,
			":3: nomi-workspace.toml has no [package] table; it has no tables, only the keys `members`, `exclude`"},
		{"an [indirect] table", "members = [\"billing\"]\n\n[indirect]\n\"example.com/kit\" = \"v1.0.0\"\n", nil,
			":3: nomi-workspace.toml has no [indirect] table; it has no tables, only the keys `members`, `exclude`"},
		{"an unknown key, with no table to list", "members = [\"billing\"]\nfloors = 1\n", nil,
			":2: nomi-workspace.toml has no key `floors`; its keys are `members`, `exclude`"},
		{"no members", "exclude = [\"vendor\"]\n", nil,
			": a workspace needs members, the directories of its packages from here, such as members = [\"billing\", \"lib/*\"]"},
		{"a literal entry with no nomi.toml", "members = [\n    \"billing\",\n    \"shop\",\n]\n", nil,
			":3: members: shop holds no nomi.toml; a member is a package's directory"},
		{"a nomi.toml beside it", "\nmembers = [\"billing\"]\n",
			func(root string) { writeTestFile(t, filepath.Join(root, "nomi.toml"), "[package]\nname = \"ws\"\n") },
			":2: this directory holds a nomi.toml too: a workspace's root is not a package; move the package into a directory of its own and name it in members"},
		{"a member's own workspace file", "members = [\"billing\", \"shop\"]\n",
			func(root string) {
				writeTestFile(t, filepath.Join(root, "shop", "nomi.toml"), "[package]\nname = \"shop\"\n")
				writeTestFile(t, filepath.Join(root, "shop", pkgcache.WorkspaceName), "members = [\"x\"]\n")
			},
			":1: members: shop holds a nomi-workspace.toml of its own; a member is a package, and holds no workspace"},
		{"a member excluded", "members = [\"billing\"]\nexclude = [\n    \"vendor/*\",\n    \"bill*\",\n]\n", nil,
			":4: exclude: bill* takes in billing, which members names; a package is a member or excluded, not both"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, errs := memberBuildErrors(t, c.ws, c.setup)
			want := filepath.Join(root, pkgcache.WorkspaceName) + c.want
			if len(errs) != 1 || errs[0].Code != analysis.ManifestCode || !strings.HasPrefix(errs[0].Message, want) {
				t.Fatalf("the member's errors are %+v\nwant one %s error: %s", errs, analysis.ManifestCode, want)
			}
		})
	}
}

// A package under a workspace that its members do not list stops the
// build with one `manifest` error at its nomi.toml, whose fix adds it to
// the members.
func TestWorkspaceFile_AnUnlistedPackageIsAnError(t *testing.T) {
	pkgfetchtest.Cache(t)
	root := filepath.Join(t.TempDir(), "ws")
	ws := "# the store\nmembers = [\n    \"billing\", # the invoices\n]\n"
	writeTestFile(t, filepath.Join(root, pkgcache.WorkspaceName), ws)
	writeTestFile(t, filepath.Join(root, "billing", "nomi.toml"), "[package]\nname = \"billing\"\n")
	shop := filepath.Join(root, "shop")
	writeTestFile(t, filepath.Join(shop, "nomi.toml"), "[package]\nname = \"shop\"\nentry_points = [\"main\"]\n")
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(memberMain))
	lib := loadStd()
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, shop, std.MakeLoader())
	want := filepath.Join(shop, "nomi.toml") + ":1: this package is under the workspace " + filepath.Join(root, pkgcache.WorkspaceName) +
		", which does not list it; add \"shop\" to its members, or to its exclude if it keeps a nomi.lock of its own"
	errs := fa.TypeErrors
	if len(errs) != 1 || errs[0].Code != analysis.ManifestCode || errs[0].Message != want {
		t.Fatalf("the errors are %+v\nwant one %s error: %s", errs, analysis.ManifestCode, want)
	}
	fix := errs[0].Fix
	if fix == nil || fix.Path != filepath.Join(root, pkgcache.WorkspaceName) || fix.Title != "Add \"shop\" to the workspace's members" || len(fix.Edits) != 1 {
		t.Fatalf("the fix is %+v", fix)
	}
	e := fix.Edits[0]
	lines := strings.SplitAfter(ws, "\n")
	at := func(p parser.Pos) int { return len(strings.Join(lines[:p.Line-1], "")) + p.Col - 1 }
	fixed := ws[:at(e.Start)] + e.Text + ws[at(e.End):]
	if want := "# the store\nmembers = [\n    \"billing\", # the invoices\n    \"shop\",\n]\n"; fixed != want {
		t.Errorf("the fix makes\n%s\nwant\n%s", fixed, want)
	}
}

// The language server checks a member's file as the member's own project,
// with the editor's folder the member, and still reads the lock at the
// workspace's root: billing asks for kit v1.0.0, another member's need
// locked v1.1.0, and only v1.1.0, which has the function billing calls,
// is in the package cache.
func TestDocumentManager_AMemberReadsTheWorkspacesLock(t *testing.T) {
	cache := pkgfetchtest.Cache(t)
	kit := map[string]string{
		"nomi.toml": "[package]\nname = \"kit\"\n",
		"kit.nomi":  "pub fn later() -> Int\n    11\nend\n",
	}
	commit := pkgfetchtest.CacheRepo(t, cache, "example.com/kit", "v1.1.0", kit)
	root := filepath.Join(t.TempDir(), "ws")
	writeTestFile(t, filepath.Join(root, pkgcache.WorkspaceName), "members = [\"billing\", \"shop\"]\n")
	writeTestFile(t, filepath.Join(root, "nomi.lock"), string((&pkgcache.Lock{Packages: []pkgcache.Locked{{
		Package: pkgcache.Package{ID: pkgcache.ID{Repo: "example.com/kit"}, Version: "v1.1.0"},
		Commit:  commit, Hash: pkgfetchtest.Hash(kit, ""), Direct: true,
	}}}).Bytes()))
	for name, v := range map[string]string{"billing": "v1.0.0", "shop": "v1.1.0"} {
		writeTestFile(t, filepath.Join(root, name, "nomi.toml"), "[package]\nname = \""+name+"\"\nentry_points = [\"main\"]\n\n[dependencies]\n"+
			"kit = { repo = \"example.com/kit\", version = \""+v+"\" }\n")
	}
	billing := filepath.Join(root, "billing")
	main := filepath.Join(billing, "main.nomi")
	src := "import\n    std::io\n    kit\nend\n\nfn main()\n    io.print(\"${kit.later()}\")\nend\n"
	writeTestFile(t, main, src)
	if _, err := os.Stat(filepath.Join(billing, "nomi.lock")); err == nil {
		t.Fatal("billing holds a nomi.lock")
	}

	lib := loadStd()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	dm.SetEditorFolder(billing)
	dm.Open(fileuri.FromPath(main), src)
	doc := dm.Get(fileuri.FromPath(main))
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analysis to be populated")
	}
	if errs := doc.Analysis.TypeErrors; len(errs) != 0 {
		t.Fatalf("billing's main.nomi has errors %+v; want none, with kit at the workspace's locked v1.1.0", errs)
	}
}
