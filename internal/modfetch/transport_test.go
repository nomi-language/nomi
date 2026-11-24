package modfetch

import (
	"archive/tar"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

// git's messages as git 2.46 writes them under LC_ALL=C, for the tests of
// wantsCredentials and of the transport git reads a repository over.
const (
	noUsername = "fatal: could not read Username for 'https://git.example': terminal prompts disabled"
	noPassword = "fatal: could not read Password for 'https://ada@git.example': terminal prompts disabled"
	authFailed = "remote: Invalid username or password.\nfatal: Authentication failed for 'https://git.example/owner/a/'"
	notFound   = "remote: Repository not found.\nfatal: repository 'https://git.example/owner/a/' not found"
	noHost     = "fatal: unable to access 'https://git.example/owner/a/': Could not resolve host: git.example"
	noKey      = "git@git.example: Permission denied (publickey).\r\nfatal: Could not read from remote repository.\n\n" +
		"Please make sure you have the correct access rights\nand the repository exists."
)

// HTTPS failed for want of credentials only when git had none to send or
// the host refused the ones it sent, and git exited 128 saying so.
func TestWantsCredentials_ReadsGitsMessage(t *testing.T) {
	for _, tc := range []struct {
		detail string
		code   int
		want   bool
	}{
		{noUsername, 128, true},
		{noPassword, 128, true},
		{authFailed, 128, true},
		{notFound, 128, false},
		{noHost, 128, false},
		{noKey, 128, false},
		{noUsername, 1, false},
	} {
		err := &gitError{args: "ls-remote --tags https://git.example/owner/a", detail: tc.detail, code: tc.code}
		if got := wantsCredentials(err); got != tc.want {
			t.Errorf("wantsCredentials(%q, exit %d) = %v, want %v", tc.detail, tc.code, got, tc.want)
		}
	}
	if wantsCredentials(os.ErrNotExist) {
		t.Error("an error that is not git's wants credentials")
	}
}

// fakeGit answers the git commands nomi runs as git would: each URL a
// repository is read by answers its tag or fails with the message in
// fails, exit 128. calls holds each command that names a URL, `ls-remote
// <url>` or `fetch <url>`, and ssh the GIT_SSH_COMMAND of each over SSH.
type fakeGit struct {
	repos map[string]map[string]string // URL -> files at v1.0.0
	fails map[string]string            // URL -> git's message
	calls []string
	ssh   []string
	dirs  map[string]string // fetch directory -> the URL fetched into it
}

const fakeCommit = "1111111111111111111111111111111111111111"

func useFakeGit(t *testing.T, g *fakeGit) *fakeGit {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("GIT_SSH_COMMAND", "")
	t.Setenv("GIT_SSH", "")
	g.dirs = map[string]string{}
	saved := gitCommand
	gitCommand = g.run
	t.Cleanup(func() { gitCommand = saved })
	return g
}

func (g *fakeGit) run(dir string, env []string, args ...string) ([]byte, error) {
	fail := func(detail string, code int) error {
		return &gitError{args: strings.Join(args, " "), detail: detail, code: code}
	}
	var url string
	switch args[0] {
	case "init":
		return nil, nil
	case "config":
		return nil, fail("", 1)
	case "archive":
		return tarOf("v1.0.0/", g.repos[g.dirs[dir]]), nil
	case "ls-remote":
		url = args[2]
	case "fetch":
		url = args[4]
	}
	g.calls = append(g.calls, args[0]+" "+url)
	if strings.HasPrefix(url, "ssh://") {
		for _, e := range env {
			if v, ok := strings.CutPrefix(e, "GIT_SSH_COMMAND="); ok {
				g.ssh = append(g.ssh, v)
			}
		}
	}
	if msg, ok := g.fails[url]; ok {
		return nil, fail(msg, 128)
	}
	if _, ok := g.repos[url]; !ok {
		return nil, fail("fatal: '"+url+"' does not appear to be a git repository", 128)
	}
	if args[0] == "fetch" {
		g.dirs[dir] = url
		return nil, nil
	}
	return []byte(fakeCommit + "\trefs/tags/v1.0.0\n"), nil
}

func tarOf(prefix string, files map[string]string) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: prefix + name, Mode: 0o644, Size: int64(len(files[name]))})
		_, _ = tw.Write([]byte(files[name]))
	}
	_ = tw.Close()
	return buf.Bytes()
}

// moduleFiles is a module named name that names each of deps on
// git.example at v1.0.0.
func moduleFiles(name string, deps ...string) map[string]string {
	toml := "[module]\nname = \"" + name + "\"\n"
	if len(deps) > 0 {
		toml += "\n[dependencies]\n"
		for _, d := range deps {
			toml += d + " = { repo = \"git.example/owner/" + d + "\", version = \"v1.0.0\" }\n"
		}
	}
	return map[string]string{"nomi.toml": toml, name + ".nomi": "pub fn one() -> Int\n    1\nend\n"}
}

const (
	httpsA = "https://git.example/owner/a"
	httpsB = "https://git.example/owner/b"
	sshA   = "ssh://git@git.example/owner/a"
	sshB   = "ssh://git@git.example/owner/b"
)

func useGitHost(t *testing.T) {
	useHosts(t, map[string]host{"git.example": {base: "https://git.example", git: true, depth: 2}})
}

// A repository git reads over HTTPS is read over HTTPS alone: its tags
// and its tag's files, with no SSH try.
func TestGitTransport_HTTPSThatWorksIsTheOnlyTry(t *testing.T) {
	g := useFakeGit(t, &fakeGit{repos: map[string]map[string]string{httpsA: moduleFiles("a")}})
	useGitHost(t)
	app, _ := setup(t)
	if _, err := Add(app, "", "git.example/owner/a", Options{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ls-remote " + httpsA, "fetch " + httpsA}; !slices.Equal(g.calls, want) {
		t.Errorf("git ran %q, want %q", g.calls, want)
	}
}

// When HTTPS wants credentials git has none for, the repository is read
// over SSH, its tags and its tag's files alike, with an ssh that never
// prompts; and the rest of the run reads that host over SSH first, so
// another of its repositories pays no failed HTTPS try.
func TestGitTransport_SSHWhenHTTPSWantsCredentials(t *testing.T) {
	g := useFakeGit(t, &fakeGit{
		repos: map[string]map[string]string{sshA: moduleFiles("a", "b"), sshB: moduleFiles("b")},
		fails: map[string]string{httpsA: noUsername, httpsB: noUsername},
	})
	useGitHost(t)
	app, _ := setup(t)
	if _, err := Add(app, "", "git.example/owner/a", Options{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"ls-remote " + httpsA, "ls-remote " + sshA, "fetch " + sshA, "ls-remote " + sshB, "fetch " + sshB}
	if !slices.Equal(g.calls, want) {
		t.Errorf("git ran\n%q\nwant\n%q", g.calls, want)
	}
	for _, c := range g.ssh {
		if c != "ssh -o BatchMode=yes" {
			t.Errorf("git ran ssh as %q, want ssh -o BatchMode=yes", c)
		}
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, "git.example/owner/b . v1.0.0 "+fakeCommit) {
		t.Errorf("nomi.lock lacks b, read over SSH:\n%s", lock)
	}

	// The user's own ssh command is kept, and made not to prompt.
	g.calls, g.ssh = nil, nil
	t.Setenv("GIT_SSH_COMMAND", "ssh -i ~/.ssh/work")
	app, _ = setup(t)
	if _, err := Add(app, "", "git.example/owner/b", Options{}); err != nil {
		t.Fatal(err)
	}
	if len(g.ssh) == 0 || g.ssh[0] != "ssh -i ~/.ssh/work -o BatchMode=yes" {
		t.Errorf("git ran ssh as %q, want the user's command with BatchMode", g.ssh)
	}
}

// When SSH fails too, the error names the repository, says both were
// tried, holds git's two messages, and says what gives git access.
func TestGitTransport_BothFailingSayWhatGivesAccess(t *testing.T) {
	useFakeGit(t, &fakeGit{fails: map[string]string{httpsA: noUsername, sshA: noKey}})
	useGitHost(t)
	app, _ := setup(t)
	before := readFile(t, filepath.Join(app, "nomi.toml"))
	_, err := Add(app, "", "git.example/owner/a@v1.0.0", Options{})
	want := "git could not read repository git.example/owner/a over HTTPS or over SSH:\n" +
		"\tgit ls-remote --tags " + httpsA + ": " + noUsername + "\n" +
		"\tgit ls-remote --tags " + sshA + ": git@git.example: Permission denied (publickey).\r\n" +
		"\t    fatal: Could not read from remote repository.\n\t    \n" +
		"\t    Please make sure you have the correct access rights\n\t    and the repository exists.\n" +
		"git reads a private repository over SSH with a key the host accepts, or over HTTPS with a credential helper (`git help credentials`)"
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("Add when neither transport reads the repository: %v\nwant it to end\n%s", err, want)
	}
	if after := readFile(t, filepath.Join(app, "nomi.toml")); after != before {
		t.Errorf("a failed Add changed nomi.toml:\n%s", after)
	}
}

// A failure over HTTPS that is not for want of credentials is the error,
// with no SSH try: SSH would not find the repository either.
func TestGitTransport_NoSSHTryWhenHTTPSFailsOtherwise(t *testing.T) {
	g := useFakeGit(t, &fakeGit{fails: map[string]string{httpsA: notFound}})
	useGitHost(t)
	app, _ := setup(t)
	_, err := Add(app, "", "git.example/owner/a@v1.0.0", Options{})
	if want := "git ls-remote --tags " + httpsA + ": " + notFound; err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("Add of a repository HTTPS does not find: %v\nwant it to end %s", err, want)
	}
	if want := []string{"ls-remote " + httpsA}; !slices.Equal(g.calls, want) {
		t.Errorf("git ran %q, want %q", g.calls, want)
	}
}

// With real git: a host that asks for credentials over HTTPS, which git
// has none for, and serves the repository over SSH (an ssh that runs git
// upload-pack on a local repository) is read over SSH.
func TestAdd_ThroughGitOverSSHWhenHTTPSWantsCredentials(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in ssh is a shell script")
	}
	for _, kv := range [][2]string{
		{"GIT_CONFIG_GLOBAL", os.DevNull}, {"GIT_CONFIG_NOSYSTEM", "1"},
		{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@example.com"},
		{"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@example.com"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	root := t.TempDir()
	src := filepath.Join(root, "owner", "tools")
	files := moduleFiles("tools")
	for name, text := range files {
		writeFile(t, filepath.Join(src, name), text)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-q", "-m", "tools"}, {"tag", "v1.0.0"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	// ssh git@git.example "git-upload-pack '/owner/tools'" runs git
	// upload-pack on root/owner/tools.
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "ssh"), "#!/bin/sh\nfor last; do :; done\np=${last#*\\'}\np=${p%\\'}\nexec git upload-pack \"$FAKE_SSH_ROOT$p\"\n")
	if err := os.Chmod(filepath.Join(bin, "ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_SSH_ROOT", root)
	t.Setenv("GIT_SSH_COMMAND", filepath.Join(bin, "ssh"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	useHosts(t, map[string]host{"git.example": {base: srv.URL, git: true, depth: 2}})
	// Each URL git was run with, and whether git's own message said it
	// wanted credentials.
	var urls []string
	saved := gitCommand
	gitCommand = func(dir string, env []string, args ...string) ([]byte, error) {
		out, err := saved(dir, env, args...)
		url := map[string]string{"ls-remote": args[len(args)-1], "fetch": args[len(args)-2]}[args[0]]
		if url != "" {
			urls = append(urls, args[0]+" "+strings.TrimPrefix(url, srv.URL+"/")+" wants credentials: "+
				map[bool]string{true: "yes", false: "no"}[wantsCredentials(err)])
		}
		return out, err
	}
	t.Cleanup(func() { gitCommand = saved })
	app, _ := setup(t)
	added, err := Add(app, "", "git.example/owner/tools", Options{})
	if err != nil {
		t.Fatalf("Add over SSH: %v", err)
	}
	want := []string{
		"ls-remote owner/tools wants credentials: yes",
		"ls-remote ssh://git@git.example/owner/tools wants credentials: no",
		"fetch ssh://git@git.example/owner/tools wants credentials: no",
	}
	if !slices.Equal(urls, want) {
		t.Errorf("git ran\n%q\nwant\n%q", urls, want)
	}
	if added.Module.Version != "v1.0.0" {
		t.Errorf("Add answered %+v, want the tag", added.Module)
	}
	if got := readFile(t, filepath.Join(added.Dir, "tools.nomi")); got != files["tools.nomi"] {
		t.Errorf("the fetched tools.nomi is %q", got)
	}
}
