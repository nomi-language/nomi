package pkgcache

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
)

// CacheEnv names the package cache's directory, which replaces
// os.UserCacheDir()/nomi/packages.
const CacheEnv = "NOMI_PACKAGE_CACHE"

// Root is the package cache: CacheEnv, else os.UserCacheDir()/nomi/packages.
// Under `go test` it is an error unless CacheEnv is set, so no test reads
// or writes the real cache.
func Root() (string, error) {
	if v := os.Getenv(CacheEnv); v != "" {
		return v, nil
	}
	if testing.Testing() {
		return "", errors.New(CacheEnv + " is unset under `go test`; set it to a t.TempDir(): a test must never read or write the real package cache")
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("no package cache: %s is unset and %v", CacheEnv, err)
	}
	return filepath.Join(dir, "nomi", "packages"), nil
}

// escape writes each upper-case letter of s as `!` and its lower-case
// letter, so two names that differ only in case are two directories on a
// file system that does not tell them apart. `!` is in no repository path
// or tag (CheckRepo, IsVersion).
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if 'A' <= r && r <= 'Z' {
			b.WriteByte('!')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// RepoDir is where the package cache holds repository repo extracted at
// tag: `<host>/<path>@<tag>`, escaped. Every package of the repository at
// that tag is a directory in it.
func RepoDir(cache, repo, tag string) string {
	return filepath.Join(cache, filepath.FromSlash(escape(repo))+"@"+escape(tag))
}

// filesSuffix names the file beside a repository's directory that lists
// its files (Install). A tag holds no `+`, so it is no tag's directory.
const filesSuffix = "+files"

// RepoFile is one entry of an extracted repository: its slash path from
// the repository's root and, for a regular file, the SHA-256 of its bytes,
// in hex. Link says the archive held a link there, symbolic or hard, which
// was not installed and has no Sum.
type RepoFile struct {
	Path, Sum string
	Link      bool
}

// linkLine starts the line of the files list that records a link. A sum is
// hex, so it is never this word.
const linkLine = "link"

// writeRepoFiles writes the list of a repository's files beside its
// directory dir: the commit its tag named, then each entry, sorted by
// path, `<sum> <path>` for a regular file and `link <path>` for a link.
// It is written before dir is renamed into place, and Lookup finds a
// repository Present only with both.
func writeRepoFiles(dir, commit string, files []RepoFile) error {
	var b strings.Builder
	b.WriteString("commit " + commit + "\n")
	for _, f := range files {
		sum := f.Sum
		if f.Link {
			sum = linkLine
		}
		b.WriteString(sum + " " + f.Path + "\n")
	}
	return artifactcache.WriteFile(dir+filesSuffix, []byte(b.String()))
}

// ReadRepoFiles reads the list Install wrote beside dir.
func ReadRepoFiles(dir string) (commit string, files []RepoFile, err error) {
	f, err := os.Open(dir + filesSuffix)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(nil, 1<<20)
	for s.Scan() {
		line := s.Text()
		if c, ok := strings.CutPrefix(line, "commit "); ok && commit == "" {
			commit = c
			continue
		}
		sum, p, ok := strings.Cut(line, " ")
		if !ok {
			return "", nil, fmt.Errorf("%s: a line is a sum and a path: %q", dir+filesSuffix, line)
		}
		if sum == linkLine {
			files = append(files, RepoFile{Path: p, Link: true})
			continue
		}
		files = append(files, RepoFile{Path: p, Sum: sum})
	}
	return commit, files, s.Err()
}

// FilesHash is the hash nomi.lock holds for the package in subdir ("" for
// the root) of a repository whose files are files, sorted by path: the
// SHA-256 of a line per regular file under subdir, `<sum>  <path>`, the
// path from subdir. Paths carry no repository or version, a file's mode
// is not hashed, and a link is not a regular file.
func FilesHash(files []RepoFile, subdir string) string {
	h := sha256.New()
	for _, f := range files {
		if rel, ok := under(f.Path, subdir); ok && !f.Link {
			fmt.Fprintf(h, "%s  %s\n", f.Sum, rel)
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// LinkIn answers the path, from the repository's root, of the first link
// files records in the package in subdir ("" for the root, whose directory
// is the whole repository), or "" when its directory holds none. A link
// is not installed, so a package that holds one is missing a file.
func LinkIn(files []RepoFile, subdir string) string {
	for _, f := range files {
		if _, ok := under(f.Path, subdir); ok && f.Link {
			return f.Path
		}
	}
	return ""
}

// under answers p, a slash path from a repository's root, as a path from
// subdir ("" for the root), and whether it is in subdir's directory.
func under(p, subdir string) (string, bool) {
	if subdir == "" {
		return p, true
	}
	return strings.CutPrefix(p, subdir+"/")
}

// Held is what the package cache holds of a package's repository at its
// tag (Lookup).
type Held int

const (
	// Absent: neither the repository's tree nor the list of its files.
	Absent Held = iota
	// Present: the tree, its list, and the package's directory in the tree.
	Present
	// NoDirectory: the tree and its list, and the list holds no file under
	// the package's directory: the repository has no such package at the
	// tag.
	NoDirectory
	// Incomplete: the tree without its list, the list without its tree, or
	// a tree that lacks the package's directory although its list holds
	// files under it. Something removed part of what a fetch installed:
	// `rm -rf` of the cache removes the list, a file in a writable
	// directory, and cannot remove the read-only tree.
	Incomplete
)

// Lookup answers what the cache holds of m's repository at its tag, and
// m's directory there when it is Present, or "". A repository is renamed
// into place whole after its list is written (Install), so a tree with its
// list is never a partial extraction. A hit marks the repository used (the
// internal/artifactcache package doc, "Pruning").
func Lookup(cache string, m Package) (string, Held) {
	repo := RepoDir(cache, m.Repo, m.Version)
	info, treeErr := os.Lstat(repo)
	listInfo, listErr := os.Stat(repo + filesSuffix)
	tree := treeErr == nil && info.IsDir()
	list := listErr == nil && !listInfo.IsDir()
	switch {
	case treeErr != nil && listErr != nil:
		return "", Absent
	case !tree || !list:
		return "", Incomplete
	}
	dir := filepath.Join(repo, filepath.FromSlash(m.Subdir))
	if !isDir(dir) {
		_, files, err := ReadRepoFiles(repo)
		if err != nil || filesUnder(files, m.Subdir) {
			return "", Incomplete
		}
		return "", NoDirectory
	}
	artifactcache.Touch(repo, info)
	artifactcache.Touch(repo+filesSuffix, listInfo)
	return dir, Present
}

// filesUnder reports whether files records a regular file in subdir's
// directory, which Install would have put in the tree.
func filesUnder(files []RepoFile, subdir string) bool {
	for _, f := range files {
		if _, ok := under(f.Path, subdir); ok && !f.Link {
			return true
		}
	}
	return false
}

// RemoveIncomplete removes the tree the cache holds of m's repository when
// Lookup still finds the entry Incomplete, so Install can put a fetched
// copy in its place. It renames the tree aside first, to a temporary name
// Prune removes if this process stops, so no reader sees part of one. The
// list stays: Install replaces it. A fetch calls it right before it
// installs, and checks again here, so a copy another process installed in
// the meantime stays.
func RemoveIncomplete(cache string, m Package) error {
	if _, held := Lookup(cache, m); held != Incomplete {
		return nil
	}
	repo := RepoDir(cache, m.Repo, m.Version)
	aside := filepath.Join(filepath.Dir(repo),
		fmt.Sprintf("%s%s-%d-%d", artifactcache.TempPrefix, filepath.Base(repo), os.Getpid(), time.Now().UnixNano()))
	if err := os.Rename(repo, aside); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return artifactcache.RemoveTree(aside)
}

// Install puts tmp, repository repo extracted at tag whose tag named
// commit and whose regular files are files, into the cache: the tree made
// read-only, the list of its files written beside its place, and tmp
// renamed there. tmp is a directory in the cache's directory for repo
// (filepath.Dir of RepoDir), so the rename stays on one file system. When
// another process put the repository there first, that copy stays.
func Install(cache, repo, tag, commit, tmp string, files []RepoFile) error {
	dir := RepoDir(cache, repo, tag)
	_ = filepath.WalkDir(tmp, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			defer os.Chmod(p, 0o555)
			return nil
		}
		return os.Chmod(p, 0o444)
	})
	if err := writeRepoFiles(dir, commit, files); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		if isDir(dir) {
			return nil
		}
		return err
	}
	return nil
}

// Foreign answers the path of the first entry under dir that the package
// cache does not write (artifactcache.ForeignEntry), or "": below the
// directories of hosts and paths there are only `<path>@<tag>` trees and
// the `+files` list beside each.
func Foreign(dir string) (string, error) {
	return artifactcache.ForeignEntry(dir, func(p string, e fs.DirEntry) (string, error) {
		name := e.Name()
		switch {
		case !strings.Contains(name, "@"):
			if e.IsDir() {
				return Foreign(p)
			}
		case strings.HasSuffix(name, filesSuffix):
			if e.Type().IsRegular() {
				return "", nil
			}
		case e.IsDir():
			return "", nil
		}
		return p, nil
	})
}

// pruneEvery is how often a process that fetches prunes the package cache.
const pruneEvery = 24 * time.Hour

// Prune removes from the package cache the repositories nothing has
// resolved for artifactcache.Unused, and extractions left unfinished for
// an hour, at most once a day (artifactcache.Due), or now when force is
// set, and answers what it removed. A repository's entries are
// `<path>@<tag>` and its list beside it, at any depth under its host, so
// the walk goes down to them.
func Prune(cache string, force bool) artifactcache.Pruned {
	var pruned artifactcache.Pruned
	if !isDir(cache) || !artifactcache.Due(cache, pruneEvery, force) {
		return pruned
	}
	now := time.Now()
	var walk func(dir string)
	walk = func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			p := filepath.Join(dir, name)
			entry := strings.Contains(name, "@") || strings.HasPrefix(name, artifactcache.TempPrefix)
			if !entry {
				if e.IsDir() {
					walk(p)
				}
				continue
			}
			if info, err := e.Info(); err == nil && artifactcache.Expired(name, info, artifactcache.Unused, now) {
				pruned.Remove(p)
			}
		}
	}
	walk(cache)
	return pruned
}
