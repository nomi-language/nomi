package modfetch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/internal/modfetch/modfetchtest"
)

// A module taken at a commit: `nomi add <repo>@<commit>` writes the
// entry's `commit`, fetches the commit's archive with no tag listing, and
// locks it with `commit` in the version column. A plain fetch then reads
// it from the cache with no request. Update leaves it as it is and says
// so. A short commit, and a commit no archive holds, are refused, and a
// refused Add leaves nomi.toml as it was.
func TestAdd_AtACommit(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	files := greetFiles("dev", "hello, ${name} (dev)")
	commit := s.Commit("example.com/greet", files)
	app, cache := setup(t)

	added, err := Add(app, "", "example.com/greet@"+commit, Options{})
	if err != nil {
		t.Fatalf("Add at a commit: %v", err)
	}
	if added.Module.Version != commit || added.Held {
		t.Errorf("Add at a commit answered %+v", added)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.HasSuffix(manifest, "greet = { repo = \"example.com/greet\", commit = \""+commit+"\" }\n") {
		t.Errorf("nomi.toml after Add at a commit:\n%s", manifest)
	}
	want := "example.com/greet . commit " + commit + " " + modfetchtest.Hash(files, "") + " direct\n"
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, want) {
		t.Errorf("nomi.lock lacks\n%s\nit is\n%s", want, lock)
	}
	if _, err := os.Stat(filepath.Join(modcache.RepoDir(cache, "example.com/greet", commit), "greet.nomi")); err != nil {
		t.Errorf("the commit is not in the module cache: %v", err)
	}

	before := s.Requests()
	if _, err := Fetch(app, Options{Offline: true}); err != nil {
		t.Fatalf("an offline fetch of a locked commit: %v", err)
	}
	if n := s.Requests() - before; n != 0 {
		t.Errorf("a fetch with a current lock made %d requests", n)
	}

	updated, err := Update(app, "", Options{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Changes) != 0 || len(updated.AtCommit) != 1 || updated.AtCommit[0].Name != "greet" {
		t.Errorf("Update of a project with an entry at a commit answered %+v", updated)
	}

	manifest := readFile(t, filepath.Join(app, "nomi.toml"))
	for _, bad := range []struct{ arg, want string }{
		{"example.com/greet@" + commit[:7], "example.com/greet@" + commit[:7] + `: "` + commit[:7] + `" is a short commit: write all 40 hex digits of it`},
		{"example.com/greet@" + strings.Repeat("9", 40), "fetching example.com/greet " + strings.Repeat("9", 40) + ": repository example.com/greet was refused by example.com (404 Not Found)"},
	} {
		if _, err := Add(app, "", bad.arg, Options{}); err == nil || !strings.HasPrefix(err.Error(), bad.want) {
			t.Errorf("Add %s: %v\nwant %s", bad.arg, err, bad.want)
		}
	}
	if after := readFile(t, filepath.Join(app, "nomi.toml")); after != manifest {
		t.Errorf("a refused Add changed nomi.toml:\n%s", after)
	}
}

// A fetched module's own entry at a commit is honored as the project's
// is: its repository is taken at that commit, and a requirement on the
// same repository at a tag elsewhere in the graph is an error naming both
// requirers.
func TestFetch_ADependencysCommitEntryIsHonored(t *testing.T) {
	s := serve(t)
	strutil := s.Commit("example.com/strutil", strutilFiles)
	s.Tag("example.com/strutil", "v1.0.0", strutilFiles)
	s.Tag("example.com/loud", "v1.0.0", map[string]string{
		"nomi.toml": "[module]\nname = \"loud\"\n\n[dependencies]\n" +
			"strutil = { repo = \"example.com/strutil\", commit = \"" + strutil + "\" }\n",
		"loud.nomi": "pub fn one() -> Int\n    1\nend\n",
	})
	app, _ := setup(t)
	if _, err := Add(app, "", "example.com/loud@v1.0.0", Options{}); err != nil {
		t.Fatal(err)
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, "example.com/strutil . commit "+strutil+" ") {
		t.Errorf("nomi.lock does not take strutil at loud's commit:\n%s", lock)
	}
	_, err := Add(app, "", "example.com/strutil@v1.0.0", Options{})
	want := "app requires example.com/strutil v1.0.0 and example.com/loud v1.0.0 requires example.com/strutil at commit " + strutil +
		": a build takes a repository at one point, so its requirements are all at tags or all at one commit"
	if err == nil || err.Error() != want {
		t.Errorf("a tag and a commit of one repository: %v\nwant %s", err, want)
	}
}

// A repository read through git is fetched at a commit by its id, at
// depth 1, when a branch or tag reaches it; a commit the server does not
// serve is an error that says what the server needs.
func TestAdd_AtACommitThroughGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repos := t.TempDir()
	src := filepath.Join(repos, "tools")
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	first := map[string]string{
		"nomi.toml": "[module]\nname = \"tools\"\n",
		"kit.nomi":  "pub fn one() -> Int\n    1\nend\n",
	}
	for name, text := range first {
		writeFile(t, filepath.Join(src, name), text)
	}
	run("init", "-q")
	run("add", ".")
	run("commit", "-q", "-m", "one")
	commit := run("rev-parse", "HEAD")
	writeFile(t, filepath.Join(src, "kit.nomi"), "pub fn one() -> Int\n    2\nend\n")
	run("commit", "-q", "-am", "two")
	useHosts(t, map[string]host{"git.example": {base: "file://" + filepath.ToSlash(repos), git: true}})
	app, cache := setup(t)

	if _, err := Add(app, "", "git.example/tools@"+commit, Options{}); err != nil {
		t.Fatalf("Add at a commit through git: %v", err)
	}
	want := "git.example/tools . commit " + commit + " " + modfetchtest.Hash(first, "") + " direct\n"
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.HasSuffix(lock, want) {
		t.Errorf("nomi.lock is\n%s\nwant it to end\n%s", lock, want)
	}
	if got := readFile(t, filepath.Join(modcache.RepoDir(cache, "git.example/tools", commit), "kit.nomi")); got != first["kit.nomi"] {
		t.Errorf("the fetched kit.nomi is %q", got)
	}

	missing := strings.Repeat("e", 40)
	_, err := Add(app, "", "git.example/tools@"+missing, Options{})
	if err == nil || !strings.Contains(err.Error(), "the server of git.example/tools does not serve commit "+missing+": ") {
		t.Errorf("Add at a commit the server does not serve: %v", err)
	}
}
