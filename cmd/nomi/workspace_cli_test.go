package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A workspace's commands: `nomi fetch` in a member's subdirectory fetches
// for every member and writes the one nomi.lock at the workspace's root,
// `nomi run` in a member takes the workspace's version of a package it
// names at a lower one, and `nomi add` at the root, which is no package,
// is an error that names the members.
func TestPackages_AWorkspaceSharesOneLock(t *testing.T) {
	c := newModuleCLI(t)
	for _, v := range []string{"v1.0.0", "v1.1.0"} {
		c.host.Tag("example.com/kit", v, map[string]string{
			"nomi.toml": "[package]\nname = \"kit\"\n",
			"kit.nomi":  "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		})
	}
	root := filepath.Join(t.TempDir(), "ws")
	mustWrite(t, filepath.Join(root, "nomi-workspace.toml"), "members = [\"billing\", \"shop\"]\n")
	for name, v := range map[string]string{"billing": "v1.0.0", "shop": "v1.1.0"} {
		mustWrite(t, filepath.Join(root, name, "nomi.toml"), "[package]\nname = \""+name+"\"\nentry_points = [\"main\"]\n\n[dependencies]\n"+
			"kit = { repo = \"example.com/kit\", version = \""+v+"\" }\n")
		mustWrite(t, filepath.Join(root, name, "main.nomi"), "import\n    std::io\n    kit\nend\n\nfn main()\n    io.print(kit.version())\nend\n")
	}
	sub := filepath.Join(root, "billing", "docs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// The workspace's root is outside the working directory, so it is
	// named in full, as the process spells it: through a symlink on macOS,
	// with a short name on Windows. It is the same directory.
	out := c.must(sub, "fetch")
	if at, ok := strings.CutPrefix(out, "1 package in the package cache for "); !ok || !sameFile(t, at, root) {
		t.Errorf("nomi fetch in a member printed %q, want the workspace's root %s", out, root)
	}
	if lock := mustRead(t, filepath.Join(root, "nomi.lock")); !strings.Contains(lock, "example.com/kit . v1.1.0 ") {
		t.Errorf("the workspace's nomi.lock:\n%s", lock)
	}
	if _, err := os.Stat(filepath.Join(root, "billing", "nomi.lock")); err == nil {
		t.Error("nomi fetch wrote a nomi.lock in the member")
	}
	if out := c.must(filepath.Join(root, "billing"), "run", "main.nomi"); out != "v1.1.0" {
		t.Errorf("nomi run in billing printed %q, want the workspace's v1.1.0", out)
	}

	_, stderr, err := c.run(root, "add", "example.com/kit")
	want := "nomi add: . is a workspace's root, which is not a package; run nomi add in the member whose nomi.toml it changes: billing and shop"
	if err == nil || stderr != want {
		t.Errorf("nomi add at the workspace's root: %v\n%s\nwant %s", err, stderr, want)
	}
}

// The workspace takes one version of a repository, so `nomi add
// <repo>@<version>` in a member moves every member's entry of the
// repository to the version, into another major version too, edits each of
// those members' nomi.toml, says which, and writes the one lock at the
// root. `nomi add` of a repository no other member names, and `nomi
// remove`, change only the member they run in.
func TestPackages_AddAcrossALineMovesEveryMember(t *testing.T) {
	c := newModuleCLI(t)
	for _, v := range []string{"v1.0.0", "v2.0.0"} {
		c.host.Tag("example.com/kit", v, map[string]string{
			"nomi.toml": "[package]\nname = \"kit\"\n",
			"kit.nomi":  "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		})
	}
	c.host.Tag("example.com/extra", "v1.0.0", map[string]string{"nomi.toml": "[package]\nname = \"extra\"\n", "extra.nomi": "pub fn x() -> Int\n    1\nend\n"})
	root := filepath.Join(t.TempDir(), "ws")
	mustWrite(t, filepath.Join(root, "nomi-workspace.toml"), "members = [\"billing\", \"docs\", \"shop\"]\n")
	kit := "kit = { repo = \"example.com/kit\", version = \"v1.0.0\" }\n"
	for _, name := range []string{"billing", "shop"} {
		mustWrite(t, filepath.Join(root, name, "nomi.toml"), "[package]\nname = \""+name+"\"\nentry_points = [\"main\"]\n\n[dependencies]\n"+kit)
		mustWrite(t, filepath.Join(root, name, "main.nomi"), "import\n    std::io\n    kit\nend\n\nfn main()\n    io.print(kit.version())\nend\n")
	}
	docs := "[package]\nname = \"docs\"\n"
	mustWrite(t, filepath.Join(root, "docs", "nomi.toml"), docs)
	c.must(root, "fetch")

	out := c.must(filepath.Join(root, "billing"), "add", "example.com/kit@v2.0.0")
	if want := "added kit, example.com/kit v2.0.0 (import kit)\nmoved kit in shop to v2.0.0 with it, since the workspace takes one version of example.com/kit\n"; !strings.HasPrefix(out, want) {
		t.Errorf("nomi add across a line in a member printed\n%s\nwant it to start\n%s", out, want)
	}
	for _, name := range []string{"billing", "shop"} {
		if got := mustRead(t, filepath.Join(root, name, "nomi.toml")); !strings.Contains(got, "kit = { repo = \"example.com/kit\", version = \"v2.0.0\" }\n") {
			t.Errorf("%s/nomi.toml after the add:\n%s", name, got)
		}
		if _, err := os.Stat(filepath.Join(root, name, "nomi.lock")); err == nil {
			t.Errorf("nomi add wrote a nomi.lock in %s", name)
		}
	}
	if got := mustRead(t, filepath.Join(root, "docs", "nomi.toml")); got != docs {
		t.Errorf("docs/nomi.toml, which names no kit, changed:\n%s", got)
	}
	if lock := mustRead(t, filepath.Join(root, "nomi.lock")); !strings.Contains(lock, "example.com/kit . v2.0.0 ") || strings.Contains(lock, "v1.0.0") {
		t.Errorf("the workspace's nomi.lock:\n%s", lock)
	}
	if out := c.must(filepath.Join(root, "shop"), "run", "main.nomi"); out != "v2.0.0" {
		t.Errorf("nomi run in shop printed %q", out)
	}

	shop := mustRead(t, filepath.Join(root, "shop", "nomi.toml"))
	if out := c.must(filepath.Join(root, "docs"), "add", "example.com/extra@v1.0.0"); strings.Contains(out, "moved") {
		t.Errorf("nomi add of a repository no other member names printed\n%s", out)
	}
	if out := c.must(filepath.Join(root, "docs"), "remove", "extra"); out != "removed extra (example.com/extra v1.0.0)" {
		t.Errorf("nomi remove in docs printed %q", out)
	}
	if got := mustRead(t, filepath.Join(root, "shop", "nomi.toml")); got != shop {
		t.Errorf("adding and removing in docs changed shop/nomi.toml:\n%s", got)
	}
	if got := mustRead(t, filepath.Join(root, "docs", "nomi.toml")); got != docs {
		t.Errorf("docs/nomi.toml after add and remove:\n%s", got)
	}
}

// rootWorkspace writes a workspace of two members, zeta (an app, named
// first in members) and libs/alpha (a library zeta names by its
// directory), and an excluded package, vendor/old, whose code does not
// check, whose test fails and whose file is not formatted. It answers the
// workspace's root.
func rootWorkspace(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "ws")
	mustWrite(t, filepath.Join(root, "nomi-workspace.toml"), "members = [\"zeta\", \"libs/*\"]\nexclude = [\"vendor\"]\n")
	mustWrite(t, filepath.Join(root, "zeta", "nomi.toml"), "[package]\nname = \"zeta\"\nentry_points = [\"main\"]\n\n[dependencies]\nalpha = { dir = \"../libs/alpha\" }\n")
	mustWrite(t, filepath.Join(root, "zeta", "main.nomi"), "import\n    std::io\n    alpha::loud\nend\n\nfn main()\n    io.print(loud.shout(\"hi\"))\nend\n\ntest \"zeta\"\n    assert loud.shout(\"a\") == \"A\"\nend\n")
	mustWrite(t, filepath.Join(root, "libs", "alpha", "nomi.toml"), "[package]\nname = \"alpha\"\n")
	mustWrite(t, filepath.Join(root, "libs", "alpha", "loud.nomi"), "pub fn shout(s: String) -> String\n    String.uppercase(s)\nend\n\ntest \"alpha\"\n    assert shout(\"b\") == \"B\"\nend\n")
	mustWrite(t, filepath.Join(root, "vendor", "old", "nomi.toml"), "[package]\nname = \"old\"\n")
	mustWrite(t, filepath.Join(root, "vendor", "old", "old.nomi"), "fn broken() -> Int\n  \"x\"\nend\n\ntest \"old\"\n    assert 1 == 2\nend\n")
	return root
}

// At a workspace's root, with no path or with the root named, `nomi
// check`, `test`, `fmt` and `effects` act on every member in the
// workspace's order (its members sorted by directory), with one report
// and one exit status, and on no excluded package: vendor/old would fail
// each of them. A failing member fails the run, and the others still run.
func TestPackages_WorkspaceRootCommandsVisitEachMember(t *testing.T) {
	c := newModuleCLI(t)
	root := rootWorkspace(t)
	for _, args := range [][]string{{"check"}, {"check", "."}} {
		if out := c.must(root, args...); out != "ok libs/alpha/loud.nomi\nok zeta/main.nomi" {
			t.Errorf("nomi %s at the root printed\n%s", strings.Join(args, " "), out)
		}
	}
	if out := c.must(root, "test"); !strings.HasSuffix(out, "test result: ok. 2 passed, 0 failed") ||
		strings.Index(out, ":: alpha") > strings.Index(out, ":: zeta") || strings.Contains(out, "vendor") {
		t.Errorf("nomi test at the root printed\n%s", out)
	}
	for _, args := range [][]string{{"fmt", "-check"}, {"fmt", "-l", "."}} {
		if out := c.must(root, args...); out != "" {
			t.Errorf("nomi %s at the root printed %q; want nothing, vendor/old being excluded", strings.Join(args, " "), out)
		}
	}
	out := c.must(root, "effects", "--check")
	if strings.Index(out, "libs/alpha:\n") != 0 || !strings.Contains(out, "\n\nzeta:\n") || strings.Contains(out, "vendor") {
		t.Errorf("nomi effects --check at the root printed\n%s", out)
	}
	out = c.must(root, "effects", "--format", "json")
	if strings.Count(out, `"project": `) != 2 || strings.Index(out, `"project": "alpha"`) > strings.Index(out, `"project": "zeta"`) {
		t.Errorf("nomi effects --format json at the root printed\n%s", out)
	}

	// A member that fails fails the run; the other is still reported.
	mustWrite(t, filepath.Join(root, "libs", "alpha", "loud.nomi"), "pub fn shout(s: String) -> String\n    String.uppercase(s)\nend\n\ntest \"alpha\"\n    assert shout(\"b\") == \"b\"\nend\n")
	out, _, err := c.run(root, "test")
	if err == nil || !strings.Contains(out, "FAIL libs/alpha/loud.nomi :: alpha") || !strings.Contains(out, "ok zeta/main.nomi :: zeta") {
		t.Errorf("nomi test at the root with a failing member: %v\n%s", err, out)
	}
	mustWrite(t, filepath.Join(root, "zeta", "main.nomi"), "fn main()\n    1 + \"x\"\nend\n")
	out, stderr, err := c.run(root, "check")
	if err == nil || out != "ok libs/alpha/loud.nomi" || !strings.Contains(stderr, "FAIL zeta/main.nomi") {
		t.Errorf("nomi check at the root with a failing member: %v\n%s\n%s", err, out, stderr)
	}
}

// At a workspace's root, which is no package, `nomi run`, `build`, `tool`
// and `types` with no package named are an error that lists the members.
func TestPackages_WorkspaceRootNamesTheMembers(t *testing.T) {
	c := newModuleCLI(t)
	root := rootWorkspace(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run"}, "nomi run: . is a workspace's root, which is not a package; name a member's directory or program: libs/alpha and zeta"},
		{[]string{"run", "."}, "nomi run: . is a workspace's root, which is not a package; name a member's directory or program: libs/alpha and zeta"},
		{[]string{"build"}, "nomi build: . is a workspace's root, which is not a package; name a member's directory or program: libs/alpha and zeta"},
		{[]string{"tool", "zeta", "gen"}, "nomi tool: . is a workspace's root, which is not a package; run nomi tool in the member it runs for: libs/alpha and zeta"},
		{[]string{"types"}, "nomi types: . is a workspace's root, which is not a package; name a member's directory or files: libs/alpha and zeta"},
	} {
		_, stderr, err := c.run(root, tc.args...)
		if err == nil || stderr != tc.want {
			t.Errorf("nomi %s at the root: %v\n%s\nwant %s", strings.Join(tc.args, " "), err, stderr, tc.want)
		}
	}
	if out := c.must(filepath.Join(root, "zeta"), "run", "."); out != "HI" {
		t.Errorf("nomi run . in a member printed %q", out)
	}
}

// `nomi new` under a workspace's root adds the new package's directory to
// members and says so, and `nomi init` in a directory under it does the
// same; a directory members names already is left as it is, and the root
// itself is no package.
func TestPackages_NewAndInitJoinTheWorkspace(t *testing.T) {
	c := newModuleCLI(t)
	root := rootWorkspace(t)
	out := c.must(root, "new", "gamma")
	if want := "wrote gamma/nomi.toml, gamma/main.nomi and gamma/AGENTS.md\nadded gamma to the members of nomi-workspace.toml\nnext: cd gamma && nomi run main.nomi"; out != want {
		t.Errorf("nomi new at the root printed\n%s\nwant\n%s", out, want)
	}
	out = c.must(filepath.Join(root, "libs"), "new", "beta")
	if strings.Contains(out, "members") {
		t.Errorf("nomi new in a directory libs/* names printed\n%s", out)
	}
	tools := filepath.Join(root, "tools", "x")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	if out := c.must(tools, "init", "x"); !strings.Contains(out, "added tools/x to the members of ") {
		t.Errorf("nomi init under the root printed\n%s", out)
	}
	if got, want := mustRead(t, filepath.Join(root, "nomi-workspace.toml")), "members = [\"zeta\", \"libs/*\", \"gamma\", \"tools/x\"]\nexclude = [\"vendor\"]\n"; got != want {
		t.Errorf("nomi-workspace.toml after new and init:\n%s\nwant\n%s", got, want)
	}
	if out := c.must(filepath.Join(root, "gamma"), "run", "main.nomi"); out != "hello" {
		t.Errorf("the new member printed %q", out)
	}
	_, stderr, err := c.run(root, "init", "w")
	if err == nil || !strings.HasPrefix(stderr, "nomi init: . is a workspace's root, which is not a package") {
		t.Errorf("nomi init at the root: %v\n%s", err, stderr)
	}
}

// `nomi tool` in a member runs the tool in the member's own project: the
// member's names, the dependency its nomi.toml names by a directory, and
// the dependency's tool, as outside a workspace.
func TestToolCommand_InAWorkspaceMember(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ws")
	if err := os.CopyFS(root, os.DirFS(toolFixture)); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "nomi-workspace.toml"), "members = [\"rates\", \"csvdata\"]\n")
	rates := filepath.Join(root, "rates")
	out, err := runNomiInDir(t, t.TempDir(), rates, "", "tool", "csvdata", "rows", "data/rates.csv", "data/names.csv")
	if want := "data/rates.csv: 3 rows\ndata/names.csv: 3 rows\n"; err != nil || out != want {
		t.Errorf("nomi tool in a member: %v\n%s\nwant:\n%s", err, out, want)
	}
	out, err = runNomiInDir(t, t.TempDir(), rates, "", "tool", "csvdata", "generate", "--check")
	if err != nil || out != "up to date\n" {
		t.Errorf("nomi tool csvdata generate --check in a member: %v\n%s", err, out)
	}
}
