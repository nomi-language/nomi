package modcache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// LockName is the file beside nomi.toml that locks the project's build
// list.
const LockName = "nomi.lock"

// lockHeader is the comment nomi.lock starts with.
const lockHeader = "# nomi.lock locks each module this project fetches: its repository, its\n" +
	"# directory there (. for the root), the tag (or `commit` for a module taken\n" +
	"# at a commit), the commit and the hash of its files. nomi fetch, add,\n" +
	"# remove and update write it; commit it.\n"

// Locked is one module of nomi.lock: the version selected for it, the
// commit its tag named when it was locked, the hash of its files (Hash),
// and whether the project's nomi.toml names it.
type Locked struct {
	Module
	Commit string
	Hash   string
	Direct bool
}

// Lock is a project's nomi.lock, one line per module of its build list
// fetched from a repository: `<repo> <subdir> <version> <commit> <hash>`,
// with `direct` after it for a module the project's nomi.toml names, and
// `.` as the subdir of a repository's root. The version of a module taken
// at a commit is the word `commit` (commitColumn), which no tag can be. A module loaded from a
// directory has no line. Lines are sorted by repository and subdir, so a
// change of the build list reads as a diff of the lines that moved.
type Lock struct {
	Modules []Locked
}

// Find answers the locked module id.
func (l *Lock) Find(id ID) (Locked, bool) {
	if l == nil {
		return Locked{}, false
	}
	for _, m := range l.Modules {
		if m.ID == id {
			return m, true
		}
	}
	return Locked{}, false
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

// commitColumn is what nomi.lock writes as the version of a module taken
// at a commit: a tag is a version, vX.Y.Z, so no tag is this word.
const commitColumn = "commit"

// hashText is a module hash as nomi.lock writes it (FilesHash).
var hashText = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ParseLock reads data, a nomi.lock's text. A line that is not a comment
// and not blank is a module's: its repository, its subdir or `.`, a
// version, the tag's commit, its hash, and `direct` or nothing. A module
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
		l.Modules = append(l.Modules, Locked{Module: Module{ID: id, Version: version}, Commit: f[3], Hash: f[4], Direct: len(f) == 6})
	}
	return l, nil
}

// Bytes is l as nomi.lock writes it: the header, then a line per module,
// sorted by repository and subdir.
func (l *Lock) Bytes() []byte {
	mods := append([]Locked(nil), l.Modules...)
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].Repo != mods[j].Repo {
			return mods[i].Repo < mods[j].Repo
		}
		return mods[i].Subdir < mods[j].Subdir
	})
	var b strings.Builder
	b.WriteString(lockHeader)
	for _, m := range mods {
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
