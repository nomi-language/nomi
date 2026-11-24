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

// Each known host's archive URL is the form its own server answered for a
// public repository's tag and for one of its commits (internal/modfetch
// package doc, "Hosts").
func TestKnownHosts_ArchiveURLs(t *testing.T) {
	for _, tc := range []struct{ repo, tag, want string }{
		{"github.com/BurntSushi/toml", "v1.5.0", "https://github.com/BurntSushi/toml/archive/refs/tags/v1.5.0.tar.gz"},
		{"gitlab.com/gitlab-org/cli", "v1.99.0", "https://gitlab.com/gitlab-org/cli/-/archive/v1.99.0/cli-v1.99.0.tar.gz"},
		{"codeberg.org/forgejo/forgejo", "v9.0.3", "https://codeberg.org/forgejo/forgejo/archive/v9.0.3.tar.gz"},
		{"bitbucket.org/atlassian/atlaskit-mk-2", "v1.0.0", "https://bitbucket.org/atlassian/atlaskit-mk-2/get/v1.0.0.tar.gz"},
		{"git.sr.ht/~sircmpwn/scdoc", "v1.9.7", "https://git.sr.ht/~sircmpwn/scdoc/archive/v1.9.7.tar.gz"},
		{"github.com/BurntSushi/toml", "d733fc535e4a9f3c454e421a87b90ade5d49bf31",
			"https://github.com/BurntSushi/toml/archive/d733fc535e4a9f3c454e421a87b90ade5d49bf31.tar.gz"},
		{"gitlab.com/gitlab-org/cli", "6f80896221c5793e432dd3656557efdf491e5fbd",
			"https://gitlab.com/gitlab-org/cli/-/archive/6f80896221c5793e432dd3656557efdf491e5fbd/cli-6f80896221c5793e432dd3656557efdf491e5fbd.tar.gz"},
		{"codeberg.org/forgejo/forgejo", "769ed880327588f0a040f25d6c56dfcdc0eef641",
			"https://codeberg.org/forgejo/forgejo/archive/769ed880327588f0a040f25d6c56dfcdc0eef641.tar.gz"},
		{"bitbucket.org/atlassian/atlaskit-mk-2", "be4cdd0b1f9566847c274d0d05b9549881e0ae4a",
			"https://bitbucket.org/atlassian/atlaskit-mk-2/get/be4cdd0b1f9566847c274d0d05b9549881e0ae4a.tar.gz"},
		{"git.sr.ht/~sircmpwn/scdoc", "24be30ef17d5ca0162f11b35258c8fa2413d556a",
			"https://git.sr.ht/~sircmpwn/scdoc/archive/24be30ef17d5ca0162f11b35258c8fa2413d556a.tar.gz"},
	} {
		_, h, ok := hostOf(tc.repo)
		if !ok {
			t.Fatalf("%s is on no known host", tc.repo)
		}
		if got := h.archive(h.base, repoPath(tc.repo), tc.tag); got != tc.want {
			t.Errorf("the archive of %s at %s is %s, want %s", tc.repo, tc.tag, got, tc.want)
		}
	}
}

// The tag advertisement is read as the hosts send it: the service line
// with or without its newline, a flush, the first ref with capabilities
// after a NUL, annotated tags peeled to their commits, lightweight tags as
// they are, and tags that are not versions left out.
func TestParseAdvertisement_ReadsTheTagsAndTheirCommits(t *testing.T) {
	c1, c2, tagObject := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("9", 40)
	lines := []string{
		"# service=git-upload-pack", // no newline, as Bitbucket sends it
		"",
		c1 + " HEAD\x00multi_ack side-band-64k\n",
		c1 + " refs/heads/main\n",
		tagObject + " refs/tags/v1.0.0\n",
		c1 + " refs/tags/v1.0.0^{}\n",
		c2 + " refs/tags/v1.1.0-rc.1\n",
		c2 + " refs/tags/release-2026\n",
		c2 + " refs/tags/v1.2\n",
		"",
	}
	var data strings.Builder
	for _, l := range lines {
		if l == "" {
			data.WriteString("0000")
			continue
		}
		data.WriteString(pkt(l))
	}
	refs, err := parseAdvertisement([]byte(data.String()))
	if err != nil {
		t.Fatal(err)
	}
	tags := tagCommits(refs)
	if len(tags) != 2 || tags["v1.0.0"] != c1 || tags["v1.1.0-rc.1"] != c2 {
		t.Errorf("the tags are %v, want v1.0.0 at its peeled commit and the prerelease", tags)
	}
	if _, err := parseAdvertisement([]byte("00zz")); err == nil {
		t.Error("a line length that is not hex was read")
	}
}

func pkt(s string) string {
	const hex = "0123456789abcdef"
	n := len(s) + 4
	return string([]byte{hex[n>>12&15], hex[n>>8&15], hex[n>>4&15], hex[n&15]}) + s
}

// On a host whose repositories have a fixed number of path elements, Add
// splits its argument there and asks for no shorter prefix.
func TestAdd_AFixedDepthHostSplitsThePath(t *testing.T) {
	s := modfetchtest.New(t)
	useHosts(t, map[string]host{"example.com": {base: s.URL, archive: knownHosts["github.com"].archive, depth: 2}})
	s.Tag("example.com/owner/repo", "v0.3.0", map[string]string{
		"tools/kit/nomi.toml": "[module]\nname = \"kit\"\n",
	})
	app, _ := setup(t)
	added, err := Add(app, "", "example.com/owner/repo/tools/kit", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if added.Module.ID != (modcache.ID{Repo: "example.com/owner/repo", Subdir: "tools/kit"}) {
		t.Errorf("Add answered %+v", added.Module)
	}
	if _, err := Add(app, "", "example.com/owner", Options{}); err == nil ||
		err.Error() != "example.com/owner names no repository: a repository on example.com has 2 path elements after the host" {
		t.Errorf("Add of a path shorter than a repository: %v", err)
	}
}

// A host nomi does not know is read through git, and with no git on PATH
// that is the error; so is a known host that refuses, as it refuses a
// private repository.
func TestFetch_AHostReadThroughGitNeedsGit(t *testing.T) {
	s := serve(t)
	app, _ := setup(t)
	t.Setenv("PATH", t.TempDir())
	_, err := Add(app, "", "unknown.example/owner/repo@v1.0.0", Options{})
	if err == nil || !strings.Contains(err.Error(), "repository unknown.example/owner/repo is on unknown.example, a host nomi reads only through git, and git is not on PATH") {
		t.Errorf("Add from an unknown host with no git: %v", err)
	}
	_ = s
	_, err = Add(app, "", "example.com/private@v1.0.0", Options{})
	if err == nil || !strings.Contains(err.Error(), "repository example.com/private was refused by example.com (404 Not Found), as a private repository is, which nomi reads through git, and git is not on PATH") {
		t.Errorf("Add of a repository the host refuses, with no git: %v", err)
	}
}

// A repository on a host read through git: its tags from `git ls-remote`,
// its files from a depth-1 fetch of the tag, locked as any module is.
func TestAdd_ThroughGit(t *testing.T) {
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
	files := map[string]string{
		"nomi.toml": "[module]\nname = \"tools\"\n",
		"kit.nomi":  "pub fn one() -> Int\n    1\nend\n",
	}
	for name, text := range files {
		writeFile(t, filepath.Join(src, name), text)
	}
	run("init", "-q")
	run("add", ".")
	run("commit", "-q", "-m", "tools")
	run("tag", "-a", "v1.0.0", "-m", "v1.0.0")
	commit := run("rev-parse", "HEAD")
	useHosts(t, map[string]host{"git.example": {base: "file://" + filepath.ToSlash(repos), git: true}})
	app, cache := setup(t)
	added, err := Add(app, "", "git.example/tools", Options{})
	if err != nil {
		t.Fatalf("Add through git: %v", err)
	}
	if added.Module.Version != "v1.0.0" {
		t.Errorf("Add answered %+v, want the tag", added.Module)
	}
	want := "git.example/tools . v1.0.0 " + commit + " " + modfetchtest.Hash(files, "") + " direct\n"
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.HasSuffix(lock, want) {
		t.Errorf("nomi.lock is\n%s\nwant it to end\n%s", lock, want)
	}
	if got := readFile(t, filepath.Join(modcache.RepoDir(cache, "git.example/tools", "v1.0.0"), "kit.nomi")); got != files["kit.nomi"] {
		t.Errorf("the fetched kit.nomi is %q", got)
	}
}
