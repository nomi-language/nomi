// Package pkgfetchtest serves repositories to tests of fetching packages:
// an in-process HTTP server that answers git's tag advertisement and
// GitHub's archive of a tag or of a commit, so no test reads the network. A Go test in
// internal/pkgfetch points its host table at the server; a `nomi` a test
// starts reaches it through Env, the NOMI_TEST_PACKAGE_HOSTS value.
package pkgfetchtest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Server is the repositories a test publishes, served over HTTP.
type Server struct {
	// URL is the server's root, which stands for every host it serves.
	URL string

	mu       sync.Mutex
	repos    map[string]map[string]tag // repo path (no host) -> tag -> files
	commits  map[string]map[string]tag // repo path (no host) -> commit -> files
	requests int
}

type tag struct {
	commit string
	files  map[string]string
	links  map[string]string
}

// New starts a server, closed when t ends.
func New(t testing.TB) *Server {
	s := &Server{repos: map[string]map[string]tag{}, commits: map[string]map[string]tag{}}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// Env is the NOMI_TEST_PACKAGE_HOSTS value that sends each of hosts to s.
func (s *Server) Env(hosts ...string) string {
	pairs := make([]string, len(hosts))
	for i, h := range hosts {
		pairs[i] = h + "=" + s.URL
	}
	return strings.Join(pairs, ",")
}

// Requests is how many requests s has answered.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// Tag publishes repo (`host/path`) at version with files, by their slash
// paths from the repository's root, and answers the commit the tag names.
// Publishing a tag again replaces its files, as a moved tag or a
// compromised host would, under a new commit.
func (s *Server) Tag(repo, version string, files map[string]string) string {
	return s.tag(repo, version, files, nil)
}

// Commit publishes a commit of repo with files that no tag names, and
// answers the commit, which its archive is served by.
func (s *Server) Commit(repo string, files map[string]string) string {
	return s.tag(repo, "", files, nil)
}

// TagWithLinks is Tag with symbolic links too, each a path and its target.
func (s *Server) TagWithLinks(repo, version string, files, links map[string]string) string {
	return s.tag(repo, version, files, links)
}

// Retag replaces the files of repo at version and keeps the commit the
// tag names, as a host serving other bytes for the same tag would.
func (s *Server) Retag(repo, version string, files map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := repoPath(repo)
	old := s.repos[p][version]
	s.repos[p][version] = tag{commit: old.commit, files: files}
}

func (s *Server) tag(repo, version string, files, links map[string]string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := repoPath(repo)
	if s.repos[p] == nil {
		s.repos[p] = map[string]tag{}
	}
	h := sha1.New()
	fmt.Fprintf(h, "%s %s %d\n", repo, version, len(s.repos[p]))
	for _, name := range sorted(files) {
		fmt.Fprintf(h, "%s %s\n", name, files[name])
	}
	commit := hex.EncodeToString(h.Sum(nil))
	t := tag{commit: commit, files: files, links: links}
	if version != "" {
		s.repos[p][version] = t
	}
	if s.commits[p] == nil {
		s.commits[p] = map[string]tag{}
	}
	s.commits[p][commit] = t
	return commit
}

func repoPath(repo string) string {
	_, p, _ := strings.Cut(repo, "/")
	return p
}

func sorted(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests++
	s.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/")
	if repo, ok := strings.CutSuffix(p, ".git/info/refs"); ok && r.URL.Query().Get("service") == "git-upload-pack" {
		s.advertise(w, repo)
		return
	}
	if repo, rest, ok := strings.Cut(p, "/archive/refs/tags/"); ok {
		if version, ok := strings.CutSuffix(rest, ".tar.gz"); ok {
			s.mu.Lock()
			t, ok := s.repos[repo][version]
			s.mu.Unlock()
			s.archive(w, repo, version, t, ok)
			return
		}
	}
	if repo, rest, ok := strings.Cut(p, "/archive/"); ok {
		if commit, ok := strings.CutSuffix(rest, ".tar.gz"); ok {
			s.mu.Lock()
			t, ok := s.commits[repo][commit]
			s.mu.Unlock()
			s.archive(w, repo, commit, t, ok)
			return
		}
	}
	http.NotFound(w, r)
}

// pkt is s as a pkt-line.
func pkt(s string) string { return fmt.Sprintf("%04x%s", len(s)+4, s) }

// advertise answers the tags of repo as git-upload-pack's version 0
// advertisement: each tag an annotated tag object, peeled to its commit.
func (s *Server) advertise(w http.ResponseWriter, repo string) {
	s.mu.Lock()
	tags, ok := s.repos[repo]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "Repository not found.", http.StatusNotFound)
		return
	}
	var b strings.Builder
	b.WriteString(pkt("# service=git-upload-pack\n"))
	b.WriteString("0000")
	head := strings.Repeat("0", 40)
	b.WriteString(pkt(head + " HEAD\x00multi_ack thin-pack side-band ofs-delta shallow no-progress\n"))
	names := make([]string, 0, len(tags))
	for name := range tags {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		object := fmt.Sprintf("%x", sha1.Sum([]byte("tag "+name)))
		b.WriteString(pkt(object + " refs/tags/" + name + "\n"))
		b.WriteString(pkt(tags[name].commit + " refs/tags/" + name + "^{}\n"))
	}
	b.WriteString("0000")
	w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
	_, _ = w.Write([]byte(b.String()))
}

// archive answers t, repo at ref, a tag or a commit, as GitHub does: a
// tar.gz whose one top directory is `<name>-<ref without v>/`, or Not
// Found when ok is false.
func (s *Server) archive(w http.ResponseWriter, repo, version string, t tag, ok bool) {
	if !ok {
		http.NotFound(w, nil)
		return
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	top := path.Base(repo) + "-" + strings.TrimPrefix(version, "v") + "/"
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": t.commit}})
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: top, Mode: 0o755})
	for _, name := range sorted(t.files) {
		_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: top + name, Mode: 0o644, Size: int64(len(t.files[name]))})
		_, _ = tw.Write([]byte(t.files[name]))
	}
	for _, name := range sorted(t.links) {
		_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: top + name, Linkname: t.links[name], Mode: 0o777})
	}
	_ = tw.Close()
	_ = zw.Close()
	w.Header().Set("Content-Type", "application/x-gzip")
	_, _ = w.Write(buf.Bytes())
}
