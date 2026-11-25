package pkgfetch

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/pkgcache"
)

// Options says what one fetch may do.
type Options struct {
	// Offline refuses the network: a package the cache lacks is an error that
	// names it.
	Offline bool
	// Update lets the fetch select versions the lock does not hold
	// (pkgcache.Resolve, from the lock's versions) and write nomi.lock: the
	// build list, and the commit and hash of each package nomi.lock does
	// not hold yet. `nomi fetch`, `add`, `remove` and `update` set it.
	Update bool
	// Log receives one line per repository fetched. Nil is silent.
	Log io.Writer
}

// offlineError is a fetch --offline refused.
type offlineError struct{ what string }

func (e *offlineError) Error() string {
	return e.what + " is not in the package cache, and --offline forbids fetching it"
}

// fetcher is one fetch's state.
type fetcher struct {
	offline bool
	update  bool
	log     io.Writer

	// cache is the package cache, or cacheErr why there is none.
	cache    string
	cacheErr error

	// ws is the workspace the project is in (pkgcache.WorkspaceOf), or
	// nil; root is the directory that holds nomi.lock, the workspace's
	// root or the project's own, and lock its nomi.lock as read.
	ws   *pkgcache.Workspace
	root string
	lock *pkgcache.Lock
	// from is the versions an updating fetch's selection starts from
	// (pkgcache.Resolve): the lock's, which a command changes where it means
	// to change them, and none for Reselect.
	from map[string]string
	// found is each package this fetch verified that nomi.lock does not
	// hold, with its commit and hash, which an updating fetch locks.
	found map[pkgcache.Package]pkgcache.Locked
	// tags is each repository's version tags and their commits, as its host
	// answered them, and viaGit the repositories read through git, each
	// with the transport git read it over. hostVia is overSSH for a host
	// one of whose repositories git read over SSH (gitRead), and ssh is the
	// SSH command once worked out (sshCommand).
	tags    map[string]map[string]string
	viaGit  map[string]transport
	hostVia map[string]transport
	ssh     string
}

func newFetcher(root string, opts Options) (*fetcher, error) {
	ws, err := pkgcache.WorkspaceOf(root)
	if err != nil {
		return nil, err
	}
	if ws != nil {
		root = ws.Root
	}
	lock, err := pkgcache.ReadLock(root)
	if err != nil {
		return nil, err
	}
	cache, cacheErr := pkgcache.Root()
	return &fetcher{
		offline:  opts.Offline,
		update:   opts.Update,
		log:      opts.Log,
		cache:    cache,
		cacheErr: cacheErr,
		ws:       ws,
		root:     root,
		lock:     lock,
		from:     lock.Versions(),
		found:    map[pkgcache.Package]pkgcache.Locked{},
		tags:     map[string]map[string]string{},
		viaGit:   map[string]transport{},
		hostVia:  map[string]transport{},
	}, nil
}

// Fetch puts every package the project at root needs into the package cache,
// verifying each against nomi.lock, and answers the project's package graph.
// With Update it selects versions from the lock's and the requirements and
// writes nomi.lock, for every member of root's workspace when root is in
// one (pkgcache.Resolve), into the nomi.lock at the workspace's root: a
// version the lock holds above what the requirements ask for stays.
func Fetch(root string, opts Options) (*pkgcache.Graph, error) {
	f, err := newFetcher(root, opts)
	if err != nil {
		return nil, err
	}
	return f.fetch(root)
}

// Reselect is Fetch with Update and the selection made from the
// requirements alone, as with no nomi.lock (`nomi fetch --update`): a
// repository only a dependency names that the lock holds higher returns
// to the highest version a dependency asks for. It answers each package
// of the build list that changed from the lock (Change).
func Reselect(root string, opts Options) (*pkgcache.Graph, []Change, error) {
	opts.Update = true
	f, err := newFetcher(root, opts)
	if err != nil {
		return nil, nil, err
	}
	f.from = nil
	g, err := f.fetch(root)
	if err != nil {
		return nil, nil, err
	}
	return g, f.changes(g), nil
}

func (f *fetcher) fetch(root string) (*pkgcache.Graph, error) {
	var g *pkgcache.Graph
	var err error
	if f.update {
		g, err = pkgcache.Resolve(root, f.materialize, f.from)
	} else {
		g, err = pkgcache.Walk(root, f.materialize)
	}
	if f.cacheErr == nil {
		pkgcache.Prune(f.cache)
	}
	if err != nil || g == nil {
		return nil, err
	}
	if f.update {
		return g, f.writeLock(g)
	}
	return g, nil
}

// locked answers m's commit and hash: the ones this fetch verified, or the
// ones nomi.lock holds for m at its version.
func (f *fetcher) locked(m pkgcache.Package) (pkgcache.Locked, bool) {
	if l, ok := f.found[m]; ok {
		return l, true
	}
	if l, ok := f.lock.Find(m.ID); ok && l.Version == m.Version {
		return l, true
	}
	return pkgcache.Locked{}, false
}

// writeLock writes nomi.lock as g's build list: each fetched package at its
// selected version, with the commit and hash this fetch verified or
// nomi.lock held, `direct` when the project, or a member of its workspace,
// names it, when that differs from the file.
func (f *fetcher) writeLock(g *pkgcache.Graph) error {
	lock := &pkgcache.Lock{}
	for _, d := range g.Deps {
		if d.Local || d.Dir == "" {
			continue
		}
		l, ok := f.locked(d.Package)
		if !ok {
			return fmt.Errorf("no verified hash for package %s", d.Package)
		}
		l.Direct = d.Direct
		lock.Packages = append(lock.Packages, l)
	}
	path := filepath.Join(f.root, pkgcache.LockName)
	data := lock.Bytes()
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	if len(lock.Packages) == 0 {
		if _, err := os.Stat(path); err != nil {
			return nil
		}
	}
	return writeFileAtomic(path, data, 0o644)
}

// materialize answers m's directory in the package cache, fetching its
// repository first when the cache lacks it, and checks its files against
// nomi.lock either way. A package is a directory of its repository at the
// tag that holds a nomi.toml and no link (pkgcache.LinkIn): a link is not
// installed, so the package would be missing it. A neighbor a package names
// by its directory is materialized the same way.
func (f *fetcher) materialize(m pkgcache.Package) (string, error) {
	if f.cacheErr != nil {
		return "", f.cacheErr
	}
	repoDir := pkgcache.RepoDir(f.cache, m.Repo, m.Version)
	dir := pkgcache.Cached(f.cache, m)
	if dir == "" {
		if _, ok := f.locked(m); !ok && !f.update {
			return "", noEntry(m)
		}
		if _, err := os.Stat(repoDir); err != nil {
			if err := f.download(m); err != nil {
				var off *offlineError
				if errors.As(err, &off) {
					return "", err
				}
				return "", fmt.Errorf("fetching %s: %w", m, err)
			}
			dir = pkgcache.Cached(f.cache, m)
		}
		if dir == "" {
			return "", fmt.Errorf("repository %s has no directory %s at %s", m.Repo, m.Subdir, m.Version)
		}
	}
	commit, files, err := pkgcache.ReadRepoFiles(repoDir)
	if err != nil {
		return "", err
	}
	if err := f.verify(m, commit, pkgcache.FilesHash(files, m.Subdir), false); err != nil {
		return "", err
	}
	if link := pkgcache.LinkIn(files, m.Subdir); link != "" {
		return "", fmt.Errorf("%s holds %s, a link: nomi installs only files and directories, so a package's directory may hold no link", m, link)
	}
	if info, err := os.Stat(filepath.Join(dir, pkgcache.ManifestName)); err != nil || info.IsDir() {
		return "", fmt.Errorf("%s has no nomi.toml, so it is not a Nomi package", m)
	}
	return dir, nil
}

// verify checks hash, the hash of m's files, and commit, the commit its
// tag named, against nomi.lock. A package nomi.lock does not hold is an
// error unless the fetch may update the lock, which then keeps them.
// fetched says the files are a fetch's, not yet installed.
func (f *fetcher) verify(m pkgcache.Package, commit, hash string, fetched bool) error {
	if l, ok := f.lock.Find(m.ID); ok && l.Version == m.Version {
		after := "These files are in the package cache, so the cache or nomi.lock has been changed"
		if fetched {
			after = "So the repository's tag, the host or nomi.lock has been changed. Nothing was installed"
		}
		switch {
		case l.Hash != hash:
			return fmt.Errorf("verifying %s: checksum mismatch\n\tfiles:     %s\n\tnomi.lock: %s\n\n"+
				"SECURITY ERROR\nThe files do not match the hash nomi.lock records for them. %s", m, hash, l.Hash, after)
		case l.Commit != commit:
			return fmt.Errorf("verifying %s: tag %s names commit %s, and nomi.lock records %s\n\n"+
				"SECURITY ERROR\nThe tag has moved since nomi.lock was written. %s", m, m.Version, commit, l.Commit, after)
		}
		return nil
	}
	if !f.update {
		return noEntry(m)
	}
	f.found[m] = pkgcache.Locked{Package: m, Commit: commit, Hash: hash}
	return nil
}

// noEntry is the error for a package nomi.lock does not hold, met by a
// fetch that may not write it. A package taken at the running release's
// version whatever its requirement names (pkgcache.Pinned) says so, since
// nomi.toml may name another.
func noEntry(m pkgcache.Package) error {
	if pin := pkgcache.Pinned(m.Repo); pin != "" && m.Version == pin {
		return fmt.Errorf("nomi.lock has no entry for package %s: this nomi is release %s, and takes the first-party libraries at that version; run `nomi fetch` to add it", m, pin)
	}
	return fmt.Errorf("nomi.lock has no entry for package %s; run `nomi fetch` to add it", m)
}

// tagsOf answers repo's version tags and the commit each names: from its
// host's advertisement, or through git for a host nomi does not know and
// for a known host that refuses (a private repository).
func (f *fetcher) tagsOf(repo string) (map[string]string, error) {
	if t, ok := f.tags[repo]; ok {
		return t, nil
	}
	if f.offline {
		return nil, &offlineError{what: "repository " + repo}
	}
	name, h, known := hostOf(repo)
	var refs []ref
	var err error
	switch {
	case known && !h.git:
		refs, err = httpRefs(h, repoPath(repo))
		if refused(err) {
			if !hasGit() {
				return nil, refusedError(repo, name, err)
			}
			refs, err = f.gitRefs(repo)
		}
	case !hasGit():
		return nil, &noGitError{repo: repo, why: "is on " + name + ", a host nomi reads only through git"}
	default:
		refs, err = f.gitRefs(repo)
	}
	if err != nil {
		return nil, err
	}
	t := tagCommits(refs)
	f.tags[repo] = t
	return t, nil
}

// archive answers the archive of repo at ref, a tag or a commit, and
// whether it is gzipped: from its host, or through git for a repository
// read through git and for a known host that refuses.
func (f *fetcher) archive(repo, ref string) (io.ReadCloser, bool, error) {
	name, h, known := hostOf(repo)
	if _, read := f.viaGit[repo]; known && !h.git && !read {
		body, err := get(h.archive(h.base, repoPath(repo), ref))
		if err == nil {
			return body, true, nil
		}
		if !refused(err) {
			return nil, false, err
		}
		if !hasGit() {
			return nil, false, refusedError(repo, name, err)
		}
	}
	if !hasGit() {
		return nil, false, &noGitError{repo: repo, why: "is on " + name + ", a host nomi reads only through git"}
	}
	r, err := f.gitArchive(repo, ref)
	return r, false, err
}

// download fetches m's repository at m's tag, or at its commit, into the
// package cache: the archive extracted into a temporary directory, every
// package of it nomi.lock holds checked against its hash, made read-only,
// and renamed into place beside the list of its files. A commit needs no
// tag listing: it names itself.
func (f *fetcher) download(m pkgcache.Package) error {
	if f.offline {
		return &offlineError{what: "package " + m.String()}
	}
	commit := m.Version
	if !pkgcache.IsCommit(m.Version) {
		tags, err := f.tagsOf(m.Repo)
		if err != nil {
			return err
		}
		var ok bool
		if commit, ok = tags[m.Version]; !ok {
			return &missingTagError{repo: m.Repo, tag: m.Version}
		}
	}
	body, gz, err := f.archive(m.Repo, m.Version)
	if err != nil {
		return err
	}
	defer body.Close()
	if f.log != nil {
		fmt.Fprintf(f.log, "nomi: fetching %s %s\n", m.Repo, m.Version)
	}
	repoDir := pkgcache.RepoDir(f.cache, m.Repo, m.Version)
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(repoDir), artifactcache.TempPrefix+filepath.Base(repoDir)+"-")
	if err != nil {
		return err
	}
	defer pkgcache.RemoveTree(tmp)
	files, err := extract(body, gz, tmp)
	if err != nil {
		return fmt.Errorf("extracting %s at %s: %w", m.Repo, m.Version, err)
	}
	for _, l := range f.lock.Packages {
		if l.Repo == m.Repo && l.Version == m.Version {
			if err := f.verify(l.Package, commit, pkgcache.FilesHash(files, l.Subdir), true); err != nil {
				return err
			}
		}
	}
	return pkgcache.Install(f.cache, m.Repo, m.Version, commit, tmp, files)
}

// missingTagError is a version a repository has no tag for.
type missingTagError struct{ repo, tag string }

func (e *missingTagError) Error() string {
	return fmt.Sprintf("repository %s has no tag %s", e.repo, e.tag)
}

// maxExtracted bounds the bytes one repository's archive extracts to.
const maxExtracted = 4 << 30

// extract writes the archive r, a tar, gzipped when gz, into dest, the
// entries of its one top directory, whatever its name, at their paths
// below it, and answers each regular file with its SHA-256, sorted by
// path. Only regular files and directories are installed: a link,
// symbolic or hard, is answered as a link and not installed, which is an
// error only for a package whose directory holds it (materialize), and a
// device and a path that escapes are refused. Modes are not kept.
func extract(r io.Reader, gz bool, dest string) ([]pkgcache.RepoFile, error) {
	if gz {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		r = zr
	}
	tr := tar.NewReader(r)
	top := ""
	var files []pkgcache.RepoFile
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		first, rest, _ := strings.Cut(strings.TrimPrefix(hdr.Name, "./"), "/")
		switch {
		case top == "":
			top = first
		case first != top:
			return nil, fmt.Errorf("the archive has two top directories, %s and %s", top, first)
		}
		rest = strings.TrimSuffix(rest, "/")
		if rest == "" {
			continue
		}
		if path.Clean(rest) != rest || path.IsAbs(rest) || rest == ".." || strings.HasPrefix(rest, "../") ||
			strings.ContainsAny(rest, "\\\x00\n") {
			return nil, fmt.Errorf("the archive holds %q, a path that escapes its directory", hdr.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(rest))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, err
			}
		case tar.TypeReg:
			if total += hdr.Size; total > maxExtracted {
				return nil, fmt.Errorf("the archive extracts to more than %d bytes", int64(maxExtracted))
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				return nil, err
			}
			h := sha256.New()
			_, err = io.Copy(io.MultiWriter(out, h), io.LimitReader(tr, hdr.Size))
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return nil, err
			}
			files = append(files, pkgcache.RepoFile{Path: rest, Sum: hex.EncodeToString(h.Sum(nil))})
		case tar.TypeSymlink, tar.TypeLink:
			files = append(files, pkgcache.RepoFile{Path: rest, Link: true})
		default:
			return nil, fmt.Errorf("the archive holds %s, which is not a file or a directory", rest)
		}
	}
	if top == "" {
		return nil, errors.New("the archive is empty")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
