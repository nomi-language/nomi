package pkgcache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

// LockName is the file beside nomi.toml that locks the project's build
// list.
const LockName = "nomi.lock"

// lockHeader is the comment nomi.lock starts with.
const lockHeader = "# nomi.lock locks each package this project fetches: its repository, its\n" +
	"# directory there (. for the root), the tag (or `commit` for a package taken\n" +
	"# at a commit), the commit and the hash of its files. nomi fetch, add,\n" +
	"# remove and update write it; commit it.\n"

// Locked is one package of nomi.lock: the version selected for it, the
// commit its tag named when it was locked, the hash of its files (Hash),
// and whether the project's nomi.toml names it.
type Locked struct {
	Package
	Commit string
	Hash   string
	Direct bool
}

// Lock is a project's nomi.lock, one line per package of its build list
// fetched from a repository: `<repo> <subdir> <version> <commit> <hash>`,
// with `direct` after it for a package the project's nomi.toml names, and
// `.` as the subdir of a repository's root. The version of a package taken
// at a commit is the word `commit` (commitColumn), which no tag can be. A package loaded from a
// directory has no line. Lines are sorted by repository and subdir, so a
// change of the build list reads as a diff of the lines that moved.
type Lock struct {
	Packages []Locked
}

// Find answers the locked package id.
func (l *Lock) Find(id ID) (Locked, bool) {
	if l == nil {
		return Locked{}, false
	}
	for _, m := range l.Packages {
		if m.ID == id {
			return m, true
		}
	}
	return Locked{}, false
}

// Versions is the version l takes each repository at, as this nomi takes
// it (Takes), a commit among them: where a selection that reads the lock
// starts (Walk), and where a command that writes it starts (Resolve), so a
// version the lock holds above what the requirements ask for stays.
func (l *Lock) Versions() map[string]string {
	out := map[string]string{}
	for _, m := range l.Packages {
		if v := Takes(m.Repo, m.Version); IsCommit(v) || semver.Compare(v, out[m.Repo]) > 0 {
			out[m.Repo] = v
		}
	}
	return out
}

// ReadLock reads the nomi.lock in dir: an empty lock when there is none.
func ReadLock(dir string) (*Lock, error) {
	path := filepath.Join(dir, LockName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Lock{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	l, err := ParseLock(data)
	if err != nil {
		return nil, fmt.Errorf("%s:%w", path, err)
	}
	return l, nil
}

// commitName is a git object name: SHA-1, or SHA-256 in a repository that
// uses it.
var commitName = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// commitColumn is what nomi.lock writes as the version of a package taken
// at a commit: a tag is a version, vX.Y.Z, so no tag is this word.
const commitColumn = "commit"

// hashText is a package hash as nomi.lock writes it (FilesHash).
var hashText = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ParseLock reads data, a nomi.lock's text. A line that is not a comment
// and not blank is a package's: its repository, its subdir or `.`, a
// version, the tag's commit, its hash, and `direct` or nothing. A package
// listed twice is an error.
func ParseLock(data []byte) (*Lock, error) {
	l := &Lock{}
	seen := map[ID]bool{}
	for i, line := range strings.Split(string(data), "\n") {
		text := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		f := strings.Fields(text)
		bad := func(format string, args ...any) error {
			return fmt.Errorf("%d: %s", i+1, fmt.Sprintf(format, args...))
		}
		if len(f) < 5 || len(f) > 6 || len(f) == 6 && f[5] != "direct" {
			return nil, bad("a line is a repository, its subdir or ., a version, a commit, a sha256: hash and `direct` or nothing: %q", text)
		}
		id := ID{Repo: f[0]}
		if f[1] != "." {
			id.Subdir = f[1]
		}
		if err := CheckRepo(id.Repo); err != nil {
			return nil, bad("%v", err)
		}
		if id.Subdir != "" {
			if err := CheckSubdir(id.Subdir); err != nil {
				return nil, bad("%v", err)
			}
		}
		version := f[2]
		if version == commitColumn {
			version = f[3]
		}
		switch {
		case f[2] == commitColumn && !IsCommit(f[3]):
			return nil, bad("%s at a commit: %q is not a commit's 40 hex digits", id, f[3])
		case f[2] != commitColumn && !IsVersion(f[2]):
			return nil, bad("%s: %q is not a version", id, f[2])
		case !commitName.MatchString(f[3]):
			return nil, bad("%s %s: %q is not a commit", id, f[2], f[3])
		case !hashText.MatchString(f[4]):
			return nil, bad("%s %s: %q is not a sha256: hash", id, f[2], f[4])
		case seen[id]:
			return nil, bad("%s is listed twice", id)
		}
		seen[id] = true
		l.Packages = append(l.Packages, Locked{Package: Package{ID: id, Version: version}, Commit: f[3], Hash: f[4], Direct: len(f) == 6})
	}
	return l, nil
}

// Bytes is l as nomi.lock writes it: the header, then a line per package,
// sorted by repository and subdir.
func (l *Lock) Bytes() []byte {
	pkgs := append([]Locked(nil), l.Packages...)
	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].Repo != pkgs[j].Repo {
			return pkgs[i].Repo < pkgs[j].Repo
		}
		return pkgs[i].Subdir < pkgs[j].Subdir
	})
	var b strings.Builder
	b.WriteString(lockHeader)
	for _, m := range pkgs {
		subdir := m.Subdir
		if subdir == "" {
			subdir = "."
		}
		version := m.Version
		if IsCommit(version) {
			version = commitColumn
		}
		b.WriteString(m.Repo + " " + subdir + " " + version + " " + m.Commit + " " + m.Hash)
		if m.Direct {
			b.WriteString(" direct")
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}
