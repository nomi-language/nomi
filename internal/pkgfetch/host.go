package pkgfetch

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nomi-language/nomi/internal/pkgcache"
)

// host is how nomi reads the repositories of one host: the URL of its
// root, the form of the archive URL of a tag or a commit, and how many
// elements a repository's path there has.
type host struct {
	// base is the host's root URL, `https://github.com`.
	base string
	// archive is the URL of the tar.gz of repository path p at ref, a tag
	// or a commit (pkgcache.IsCommit). Each host's answered for a public
	// repository's tag and for its commit (internal/pkgfetch package doc,
	// "Hosts").
	archive func(base, p, ref string) string
	// depth is the number of path elements of a repository on the host,
	// or 0 when it varies (GitLab's groups) and `nomi add` finds the
	// repository by asking for ever-shorter prefixes.
	depth int
	// bare is set when the advertisement's URL has no `.git` (sourcehut).
	bare bool
	// git is set for a host read through git alone, at base.
	git bool
}

// knownHosts are the hosts nomi reads over HTTPS without git: their tag
// advertisement and their archive of a tag. Any other host is read through
// git.
var knownHosts = map[string]host{
	"github.com": {base: "https://github.com", depth: 2, archive: func(base, p, ref string) string {
		if pkgcache.IsCommit(ref) {
			return base + "/" + p + "/archive/" + ref + ".tar.gz"
		}
		return base + "/" + p + "/archive/refs/tags/" + ref + ".tar.gz"
	}},
	"gitlab.com": {base: "https://gitlab.com", archive: func(base, p, ref string) string {
		return base + "/" + p + "/-/archive/" + ref + "/" + path.Base(p) + "-" + ref + ".tar.gz"
	}},
	"codeberg.org": {base: "https://codeberg.org", depth: 2, archive: func(base, p, ref string) string {
		return base + "/" + p + "/archive/" + ref + ".tar.gz"
	}},
	"bitbucket.org": {base: "https://bitbucket.org", depth: 2, archive: func(base, p, ref string) string {
		return base + "/" + p + "/get/" + ref + ".tar.gz"
	}},
	"git.sr.ht": {base: "https://git.sr.ht", depth: 2, bare: true, archive: func(base, p, ref string) string {
		return base + "/" + p + "/archive/" + ref + ".tar.gz"
	}},
}

// hostTable is the hosts nomi reads without git. A Go test replaces it to
// serve repositories from a server of its own.
var hostTable = knownHosts

// testHostsEnv maps hosts to a test's server, `example.com=http://127.0.0.1:5000`,
// pairs separated by commas: each host's repositories are read there with
// GitHub's archive form, and `nomi add` probes their paths. Tests set it
// for a `nomi` they start; nothing else needs it.
const testHostsEnv = "NOMI_TEST_PACKAGE_HOSTS"

// hostOf answers the host repo is on, and whether nomi reads it without
// git.
func hostOf(repo string) (string, host, bool) {
	name, _, _ := strings.Cut(repo, "/")
	if v := os.Getenv(testHostsEnv); v != "" {
		for _, pair := range strings.Split(v, ",") {
			if h, url, ok := strings.Cut(pair, "="); ok && h == name {
				return name, host{base: strings.TrimSuffix(url, "/"), archive: knownHosts["github.com"].archive}, true
			}
		}
	}
	h, ok := hostTable[name]
	return name, h, ok
}

// repoPath is repo without its host.
func repoPath(repo string) string {
	_, p, _ := strings.Cut(repo, "/")
	return p
}

// statusError is an HTTP answer other than 200.
type statusError struct {
	url    string
	status string
	code   int
}

func (e *statusError) Error() string { return "reading " + e.url + ": " + e.status }

// refused reports whether err is a host's answer that the repository is
// private or not there: 401, 403 or 404 (410 too). git, with the user's
// credentials, may still read it.
func refused(err error) bool {
	var s *statusError
	if !errors.As(err, &s) {
		return false
	}
	switch s.code {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return true
	}
	return false
}

var httpClient = &http.Client{Timeout: 10 * time.Minute}

// get answers the body of url. The caller closes it.
func get(url string) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nomi")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, errors.Unwrap(err))
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &statusError{url: url, status: resp.Status, code: resp.StatusCode}
	}
	return resp.Body, nil
}

// maxAdvertisement bounds a tag advertisement.
const maxAdvertisement = 64 << 20

// ref is one ref an advertisement or `git ls-remote` names: its name and
// the object it names.
type ref struct{ name, object string }

// httpRefs reads the refs the host h advertises for the repository at
// path p over git's smart HTTP protocol, version 0: no Git-Protocol header
// is sent, so every server answers the one form.
func httpRefs(h host, p string) ([]ref, error) {
	url := h.base + "/" + p
	if !h.bare {
		url += ".git"
	}
	url += "/info/refs?service=git-upload-pack"
	body, err := get(url)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxAdvertisement))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	refs, err := parseAdvertisement(data)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	return refs, nil
}

// parseAdvertisement reads the refs of a git-upload-pack advertisement:
// pkt-lines, each a four-digit hex length that counts itself, `0000` a
// flush; the service line, then `<object> <ref>` per line, the first with
// its capabilities after a NUL.
func parseAdvertisement(data []byte) ([]ref, error) {
	var refs []ref
	for len(data) > 0 {
		if len(data) < 4 {
			return nil, errors.New("the tag advertisement ends inside a line")
		}
		n, err := strconv.ParseUint(string(data[:4]), 16, 16)
		if err != nil {
			return nil, fmt.Errorf("the tag advertisement has a line length %q", data[:4])
		}
		if n == 0 {
			data = data[4:]
			continue
		}
		if n < 4 || int(n) > len(data) {
			return nil, errors.New("the tag advertisement ends inside a line")
		}
		line := string(data[4:n])
		data = data[n:]
		line = strings.TrimSuffix(line, "\n")
		if strings.HasPrefix(line, "#") {
			continue
		}
		line, _, _ = strings.Cut(line, "\x00")
		object, name, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("the tag advertisement has a line %q", line)
		}
		refs = append(refs, ref{name: name, object: object})
	}
	return refs, nil
}

// tagCommits is the commit each version tag among refs names: a tag
// object's peeled commit (`<tag>^{}`), else the object a lightweight tag
// names. A tag that is not a version (IsVersion) is left out.
func tagCommits(refs []ref) map[string]string {
	direct, peeled := map[string]string{}, map[string]string{}
	for _, r := range refs {
		tag, ok := strings.CutPrefix(r.name, "refs/tags/")
		if !ok {
			continue
		}
		if t, ok := strings.CutSuffix(tag, "^{}"); ok {
			peeled[t] = r.object
		} else {
			direct[tag] = r.object
		}
	}
	out := map[string]string{}
	for tag, object := range direct {
		if !pkgcache.IsVersion(tag) {
			continue
		}
		if c, ok := peeled[tag]; ok {
			object = c
		}
		out[tag] = object
	}
	return out
}

// transport is how git reaches a repository: over HTTPS, the URL a
// repository's name stands for, or over SSH, which a host's private
// repositories often need, since a developer's access to them is often an
// SSH key with no credential helper for HTTPS.
type transport int

const (
	overHTTPS transport = iota
	overSSH
)

// url is the URL git reads repo from over t. Over HTTPS it is under its
// host's root for a host in the table, which is https://<repo> for each
// known host, else https://<repo>. Over SSH it is ssh://git@<repo>: the
// URL form rather than git's `git@host:path`, so the transport is named
// and nothing in it reads as a local path; `git` is the user every forge
// serves repositories to over SSH.
func (t transport) url(repo string) string {
	if t == overSSH {
		return "ssh://git@" + repo
	}
	if _, h, ok := hostOf(repo); ok {
		return h.base + "/" + repoPath(repo)
	}
	return "https://" + repo
}

// noGitError is a repository nomi can read only through git, with no git
// on PATH. cause is the host's refusal that sent it to git, if any, so a
// probe of a prefix that is no repository reads as refused.
type noGitError struct {
	repo, why string
	cause     error
}

func (e *noGitError) Error() string {
	return fmt.Sprintf("repository %s %s, and git is not on PATH", e.repo, e.why)
}

func (e *noGitError) Unwrap() error { return e.cause }

// refusedError is the error for repo, which its known host refused with
// err, when git, which reads a private repository with the user's
// credentials, is not on PATH.
func refusedError(repo, host string, err error) error {
	var s *statusError
	errors.As(err, &s)
	return &noGitError{repo: repo, why: "was refused by " + host + " (" + s.status + "), as a private repository is, which nomi reads through git", cause: err}

}

// gitError is a git command that failed: what it said and its exit
// status.
type gitError struct {
	args, detail string
	code         int
}

func (e *gitError) Error() string { return "git " + e.args + ": " + e.detail }

// gitAccessError is a repository git read neither over HTTPS, for want of
// credentials, nor over SSH.
type gitAccessError struct {
	repo       string
	https, ssh error
}

func (e *gitAccessError) Error() string {
	indent := func(err error) string { return strings.ReplaceAll(err.Error(), "\n", "\n\t    ") }
	return fmt.Sprintf("git could not read repository %s over HTTPS or over SSH:\n\t%s\n\t%s\n"+
		"git reads a private repository over SSH with a key the host accepts, or over HTTPS with a credential helper (`git help credentials`)",
		e.repo, indent(e.https), indent(e.ssh))
}

func (e *gitAccessError) Unwrap() []error { return []error{e.https, e.ssh} }

// wantsCredentials reports whether err is git failing over HTTPS for want
// of credentials: it had none to send, since it may not prompt and no
// credential helper answered, or the host refused the ones it sent. git
// says so only in its message, exiting 128 as for every fatal error, so
// this reads git's message, which the git command runs in English
// (gitEnv), and as narrowly as git words it: a repository that is not
// there, or a host git cannot reach, is not this, and SSH would fail too.
func wantsCredentials(err error) bool {
	var g *gitError
	if !errors.As(err, &g) || g.code != 128 {
		return false
	}
	for _, s := range []string{
		"fatal: could not read Username for '", // no credentials, GIT_TERMINAL_PROMPT=0
		"fatal: could not read Password for '",
		"fatal: Authentication failed for '", // credentials the host refused
	} {
		if strings.Contains(g.detail, s) {
			return true
		}
	}
	return false
}

// gitEnv is the environment each git command runs with, after the
// process's own: it never prompts on the terminal, so a probe of a path
// that is no repository fails instead of waiting, and its messages are
// its own English, which wantsCredentials reads.
var gitEnv = []string{"GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}

// gitCommand runs git with args in dir, with the process's environment
// and env after it, and answers its standard output, or a *gitError. git
// reads the user's configuration, so their credential helpers and their
// `insteadOf` rewrites apply. A test replaces it to answer as git would.
var gitCommand = func(dir string, env []string, args ...string) ([]byte, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		code := -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		return nil, &gitError{args: strings.Join(args, " "), detail: detail, code: code}
	}
	return stdout.Bytes(), nil
}

// refusesCommit reports whether err is git failing to fetch a commit by
// its id because the server does not serve one no ref names, or names no
// such commit: git says so only in its message, which runs in English
// (gitEnv), as `not our ref` from a server of protocol version 0 and
// `unadvertised object` or `not our ref` from git itself.
func refusesCommit(err error) bool {
	var g *gitError
	if !errors.As(err, &g) {
		return false
	}
	for _, s := range []string{"not our ref", "unadvertised object", "couldn't find remote ref", "no such remote ref"} {
		if strings.Contains(g.detail, s) {
			return true
		}
	}
	return false
}

// commitRefusedError is a commit git could not fetch by its id.
type commitRefusedError struct {
	repo, commit string
	err          error
}

func (e *commitRefusedError) Error() string {
	return fmt.Sprintf("the server of %s does not serve commit %s: the commit is not in the repository, or the server serves a commit by its id "+
		"only when a branch or tag names it, so it needs git's protocol version 2 or uploadpack.allowReachableSHA1InWant; "+
		"push a branch or tag that reaches the commit, or take a tag instead (%v)", e.repo, e.commit, e.err)
}

func (e *commitRefusedError) Unwrap() error { return e.err }

// hasGit reports whether git is on PATH.
func hasGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// gitRead runs git for repo in dir with the arguments args makes of the
// repository's URL, over the transport this run reads repo by, so its tag
// listing and its tag's fetch go the same way. A repository not read yet
// is tried over HTTPS, and when that fails for want of credentials
// (wantsCredentials), over SSH; a host one of whose repositories was read
// over SSH is read over SSH for the rest of the run, so each of its
// repositories pays no failed HTTPS try. A user's `insteadOf` rewrite of
// the HTTPS URL applies to the first try, as git applies it.
func (f *fetcher) gitRead(repo, dir string, args func(url string) []string) ([]byte, error) {
	host, _, _ := strings.Cut(repo, "/")
	t, read := f.viaGit[repo]
	if !read {
		t = f.hostVia[host]
	}
	out, err := f.git(dir, t, args(t.url(repo)))
	if err != nil && !read && t == overHTTPS && wantsCredentials(err) {
		var sshErr error
		if out, sshErr = f.git(dir, overSSH, args(overSSH.url(repo))); sshErr != nil {
			return nil, &gitAccessError{repo: repo, https: err, ssh: sshErr}
		}
		t, err = overSSH, nil
		f.hostVia[host] = overSSH
	}
	if err != nil {
		return nil, err
	}
	f.viaGit[repo] = t
	return out, nil
}

// git runs git with args in dir over t: over SSH with sshCommand.
func (f *fetcher) git(dir string, t transport, args []string) ([]byte, error) {
	env := gitEnv
	if t == overSSH {
		env = append(slices.Clip(gitEnv), "GIT_SSH_COMMAND="+f.sshCommand())
	}
	return gitCommand(dir, env, args...)
}

// sshCommand is the GIT_SSH_COMMAND git reads a repository over SSH with:
// the command git itself would run (GIT_SSH_COMMAND, core.sshCommand,
// GIT_SSH, else ssh) with `-o BatchMode=yes`, so ssh asks for no
// passphrase, no password and no confirmation of a host key: a key no
// agent holds, or a host known_hosts lacks, fails with ssh's own message.
func (f *fetcher) sshCommand() string {
	if f.ssh != "" {
		return f.ssh
	}
	base := os.Getenv("GIT_SSH_COMMAND")
	if base == "" {
		if out, err := gitCommand("", gitEnv, "config", "--get", "core.sshCommand"); err == nil {
			base = strings.TrimSpace(string(out))
		}
	}
	if p := os.Getenv("GIT_SSH"); base == "" && p != "" {
		base = "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
	}
	f.ssh = cmp.Or(base, "ssh") + " -o BatchMode=yes"
	return f.ssh
}

// gitRefs reads repo's tags with `git ls-remote --tags`.
func (f *fetcher) gitRefs(repo string) ([]ref, error) {
	out, err := f.gitRead(repo, "", func(url string) []string { return []string{"ls-remote", "--tags", url} })
	if err != nil {
		return nil, err
	}
	var refs []ref
	for _, line := range strings.Split(string(out), "\n") {
		if object, name, ok := strings.Cut(strings.TrimSpace(line), "\t"); ok {
			refs = append(refs, ref{name: name, object: object})
		}
	}
	return refs, nil
}

// gitArchive fetches ref of repo, a tag or a commit, at depth 1 into a
// temporary repository and answers `git archive` of it, a tar whose one
// top directory is `ref/`. The caller closes the reader. A commit is
// fetched by its id, which a server allows for a commit a branch or tag
// reaches when it speaks git's protocol version 2, as git asks by
// default, or sets uploadpack.allowReachableSHA1InWant; one that refuses
// is a commitRefusedError.
func (f *fetcher) gitArchive(repo, ref string) (io.ReadCloser, error) {
	tmp, err := os.MkdirTemp("", "nomi-git-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if _, err := gitCommand(tmp, gitEnv, "init", "-q"); err != nil {
		return nil, err
	}
	want := "refs/tags/" + ref
	if pkgcache.IsCommit(ref) {
		want = ref
	}
	if _, err := f.gitRead(repo, tmp, func(url string) []string {
		return []string{"fetch", "-q", "--depth", "1", url, want}
	}); err != nil {
		if pkgcache.IsCommit(ref) && refusesCommit(err) {
			return nil, &commitRefusedError{repo: repo, commit: ref, err: err}
		}
		return nil, err
	}
	out, err := gitCommand(tmp, gitEnv, "archive", "--format=tar", "--prefix="+ref+"/", "FETCH_HEAD")
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(out)), nil
}
