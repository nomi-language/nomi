package pkgcache

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/semver"
)

// ManifestName is the file that makes a directory a package.
const ManifestName = "nomi.toml"

// WorkspaceName is the file that makes a directory a workspace's root
// (WorkspaceOf).
const WorkspaceName = "nomi-workspace.toml"

// DependencyEntry is one entry of nomi.toml's [dependencies] table as it
// decodes: `repo`, `version` or `commit`, and an optional `subdir` name a
// package fetched from its repository, and `dir` a directory that holds the
// package, relative to the manifest's directory. `effects` and
// `effects_in_tests` are the most effects the package, and the packages
// the project reaches through it, may have, which analysis checks and
// vmhost holds the program to. analysis's manifest declares the table with
// this type, so the keys it checks are the ones this package reads.
type DependencyEntry struct {
	Repo           string    `toml:"repo"`
	Subdir         string    `toml:"subdir"`
	Version        string    `toml:"version"`
	Commit         string    `toml:"commit"`
	Dir            string    `toml:"dir"`
	Effects        *[]string `toml:"effects"`
	EffectsInTests *[]string `toml:"effects_in_tests"`
}

// WorkspaceFile is nomi-workspace.toml as it decodes: members, the
// directories of the workspace's packages, and exclude, directories whose
// packages are on their own, each relative to the file's directory, in
// which `*` matches within one path segment. analysis declares the file's
// keys with this type, so the keys it checks are the ones this package
// reads.
type WorkspaceFile struct {
	Members []string `toml:"members"`
	Exclude []string `toml:"exclude"`
}

// rawManifest is a nomi.toml as this package decodes it, before
// ParseManifest checks it.
type rawManifest struct {
	Package struct {
		Name string `toml:"name"`
		Nomi string `toml:"nomi"`
	} `toml:"package"`
	Dependencies map[string]DependencyEntry `toml:"dependencies"`
}

// ID is a fetched package's identity in the graph: the repository that
// holds it, `host/path` with no scheme, and its directory there,
// slash-separated, "" for the repository's root.
type ID struct {
	Repo, Subdir string
}

// String is id as one path: the repository, then its subdirectory.
func (id ID) String() string {
	if id.Subdir == "" {
		return id.Repo
	}
	return id.Repo + "/" + id.Subdir
}

// IsZero reports whether id names no repository: a package loaded from a
// directory whose entry names none.
func (id ID) IsZero() bool { return id.Repo == "" }

// Package is a package at a version: a tag of its repository, vX.Y.Z, or
// for a package taken at a commit, the commit's 40 hex digits (IsCommit),
// which no tag can be.
type Package struct {
	ID
	Version string
}

// String is m as `nomi` names it in its output: its path and version.
func (m Package) String() string { return m.ID.String() + " " + m.Version }

// Requirement is one checked entry of a [dependencies] table.
type Requirement struct {
	// Name is the entry's key: the name the package's files import the
	// dependency by.
	Name string
	// ID is the package's repository and subdirectory, zero for an entry
	// with only a dir.
	ID
	// Version is the tag a consumer of this package takes the dependency at,
	// or the commit for an entry with a `commit` (IsCommit), "" for an
	// entry with only a dir.
	Version string
	// Dir is the dependency's directory as the entry writes it, or "". In
	// a package on disk it is a path from the manifest's directory, and it
	// stands for the package ID names, when there is one, wherever the
	// graph reaches it. In a fetched package it names a neighbor in the
	// same repository at the same tag.
	Dir string
	// Effects and EffectsInTests are the entry's `effects` and
	// `effects_in_tests` keys, each nil when the entry has no such key and
	// empty, not nil, for `[]`. The graph reads neither; they are the
	// project's limits on what it reaches through the entry
	// (analysis.DependenciesEffects), which the writers of [dependencies]
	// keep (analysis.WriteRequirements).
	Effects, EffectsInTests []string
}

// Same reports whether r and o are the same entry, their effects keys
// included.
func (r Requirement) Same(o Requirement) bool {
	same := func(a, b []string) bool { return (a == nil) == (b == nil) && slices.Equal(a, b) }
	return r.Name == o.Name && r.ID == o.ID && r.Version == o.Version && r.Dir == o.Dir &&
		same(r.Effects, o.Effects) && same(r.EffectsInTests, o.EffectsInTests)
}

// Manifest is what pkgcache reads of a nomi.toml: the package's
// name, the lowest nomi release its source needs ([package].nomi, "" when
// it states none), and its [dependencies] table, sorted by name.
type Manifest struct {
	Name     string
	Nomi     string
	Requires []Requirement
}

// Require answers the requirement named name.
func (f *Manifest) Require(name string) (Requirement, bool) {
	for _, r := range f.Requires {
		if r.Name == name {
			return r, true
		}
	}
	return Requirement{}, false
}

// ManifestProblem is an error in the part of a nomi.toml or a
// nomi-workspace.toml this package reads, at the key path Key
// (`dependencies`, `sqlite`, `version`), so a caller holding the text can
// say its line: the line of Value in the list at Key when Value is set.
// The message of a key in a table starts with the table, Key's first part.
type ManifestProblem struct {
	Key   []string
	Value string
	Msg   string
}

func (p *ManifestProblem) Error() string {
	if len(p.Key) > 1 {
		return "[" + p.Key[0] + "] " + p.Msg
	}
	return p.Msg
}

// PackageManifestError is a problem in the nomi.toml of a package the
// main package reaches: a dependency, at any depth, or a member of its
// workspace. It stops the walk as a problem in the main package's own
// nomi.toml does, and says which entry names the package.
type PackageManifestError struct {
	// Err is the problem, at its line in the package's nomi.toml when a
	// reader is installed (ManifestReader).
	Err error
	// Fetched is the package as fetched, from its repository at a version
	// into the package cache; zero for a package on disk, whose files are
	// the reader's to edit.
	Fetched Package
	// By is the file that names the package: the nomi.toml whose
	// [dependencies] has Entry as a key, or with Member set, the
	// nomi-workspace.toml whose members list holds Entry.
	By, Entry string
	Member    bool
	// Main is the main package's nomi.toml and Top the key of its
	// [dependencies] entry the walk reached By through, when By is another
	// package's nomi.toml; both "" when By is Main.
	Main, Top string
}

func (e *PackageManifestError) Error() string { return e.Err.Error() + e.NamedBy() }

// NamedBy is what Error adds to Err's message: the entry that names the
// package, and for a fetched package, that it is not the reader's to edit.
func (e *PackageManifestError) NamedBy() string {
	var b strings.Builder
	fetched := !e.Fetched.ID.IsZero()
	switch {
	case e.Member:
		fmt.Fprintf(&b, "; members entry %q of %s names this package", e.Entry, e.By)
	case fetched:
		at := "at " + e.Fetched.Version
		if IsCommit(e.Fetched.Version) {
			at = "at commit " + e.Fetched.Version
		}
		what := e.Fetched.Repo + " " + at
		if e.Fetched.Subdir != "" {
			what = "the package in " + e.Fetched.Subdir + " of " + what
		}
		fmt.Fprintf(&b, "; this is %s, which [dependencies] %s of %s names", what, e.Entry, e.By)
	default:
		fmt.Fprintf(&b, "; [dependencies] %s of %s names this package", e.Entry, e.By)
	}
	if e.Top != "" {
		fmt.Fprintf(&b, ", which the project reaches through its [dependencies] %s", e.Top)
	}
	if fetched {
		b.WriteString("; it was fetched into the package cache, so it is not yours to edit: take another version of it, or tell its authors")
	}
	return b.String()
}

func (e *PackageManifestError) Unwrap() error { return e.Err }

// EntryIn is the entry of the file at path that leads to the package, as a
// ManifestProblem's Key and Value name a place: By's entry that names it,
// or the main package's entry the walk reached By through; false when path
// is neither file.
func (e *PackageManifestError) EntryIn(path string) (key []string, value string, ok bool) {
	same := func(a string) bool { return a != "" && (filepath.Clean(a) == filepath.Clean(path) || sameDir(a, path)) }
	switch {
	case same(e.By) && e.Member:
		return []string{"members"}, e.Entry, true
	case same(e.By):
		return []string{"dependencies", e.Entry}, "", true
	case same(e.Main):
		return []string{"dependencies", e.Top}, "", true
	}
	return nil, "", false
}

// packageName is the form of a name an import path can start with: a
// [dependencies] key, and the [package].name `nomi new` and `nomi init`
// write.
var packageName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// IsPackageName reports whether name has the form of a name an import path
// can start with: lowercase letters, digits and `_`, not starting with a
// digit.
func IsPackageName(name string) bool { return packageName.MatchString(name) }

// hostName is a repository host: lowercase letters, digits, `-` and at
// least one dot.
var hostName = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+$`)

// pathElement is one element of a repository path or a subdirectory:
// letters, digits, `.`, `_`, `~` and `-`.
var pathElement = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// CheckRepo reports what is wrong with repo as a repository: `host/path`,
// fetched over HTTPS, with no scheme and no `.git`.
func CheckRepo(repo string) error {
	if strings.Contains(repo, "://") {
		return fmt.Errorf("%q is not a repository: write it with no scheme, as host/path, such as github.com/owner/name", repo)
	}
	elems := strings.Split(repo, "/")
	if len(elems) < 2 || !hostName.MatchString(elems[0]) {
		return fmt.Errorf("%q is not a repository: it is a host and a path, such as github.com/owner/name", repo)
	}
	if err := checkElements(elems[1:]); err != nil {
		return fmt.Errorf("%q is not a repository: %v", repo, err)
	}
	if strings.HasSuffix(repo, ".git") {
		return fmt.Errorf("%q is not a repository: leave out the .git", repo)
	}
	return nil
}

// CheckSubdir reports what is wrong with dir as a directory inside a
// repository: a clean slash path, never empty, `.` or with `..`.
func CheckSubdir(dir string) error {
	if dir == "" || dir == "." || path.IsAbs(dir) || path.Clean(dir) != dir {
		return fmt.Errorf("%q is not a directory in a repository: it is a clean slash path below the root, such as lib/sql", dir)
	}
	if err := checkElements(strings.Split(dir, "/")); err != nil {
		return fmt.Errorf("%q is not a directory in a repository: %v", dir, err)
	}
	return nil
}

func checkElements(elems []string) error {
	for _, e := range elems {
		switch {
		case e == "" || e == "." || e == "..":
			return errors.New("an element of the path is empty, `.` or `..`")
		case !pathElement.MatchString(e):
			return fmt.Errorf("%q holds a character other than letters, digits, `.`, `_`, `~` and `-`", e)
		}
	}
	return nil
}

// IsVersion reports whether v is a version a tag can be: vX.Y.Z, or a
// prerelease vX.Y.Z-..., with no build suffix.
func IsVersion(v string) bool { return semver.IsValid(v) && semver.Canonical(v) == v }

// fullCommit is a commit as an entry writes it: all 40 hex digits of its
// SHA-1, lowercase.
var fullCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

// IsCommit reports whether v names a commit, as an entry's `commit` does:
// 40 lowercase hex digits. A tag starts with v, so no version is one.
func IsCommit(v string) bool { return fullCommit.MatchString(v) }

// shortCommit is the start of a commit's hex digits, as git abbreviates
// them.
var shortCommit = regexp.MustCompile(`^[0-9a-fA-F]{4,39}$`)

// CommitProblem says what is wrong with c as a commit an entry or `nomi
// add` names, or "" when it is one.
func CommitProblem(c string) string {
	switch {
	case IsCommit(c):
		return ""
	case shortCommit.MatchString(c):
		return fmt.Sprintf("%q is a short commit: write all 40 hex digits of it", c)
	case fullCommit.MatchString(strings.ToLower(c)):
		return fmt.Sprintf("write the commit in lowercase, %s", strings.ToLower(c))
	}
	return fmt.Sprintf("%q is not a commit: a commit is its 40 hex digits", c)
}

// Line is the compatible line of the version v, in three tiers: from v1
// on its major version (v1, v2); from v0.1 on its minor version (v0.1,
// v0.2), since each 0.x line may break the one before it; and below v0.1
// the release itself (v0.0.3), since each 0.0.x release may break the one
// before it. A prerelease is in the line of its release: v0.0.4-rc.1 in
// v0.0.4's, v0.2.0-rc.1 in v0.2. `nomi update` moves a repository within
// its line, and two requirements on one repository in different lines are
// an error (walk).
func Line(v string) string {
	line, _ := lineOf(v)
	return line
}

// LineRule says which versions make a line in v's tier (Line), as `nomi
// update` gives it for not leaving the line: "below v0.1.0 each release is
// a line of its own", "below v1.0.0 each minor version is a line of its
// own" or "from v1.0.0 each major version is a line of its own".
func LineRule(v string) string {
	_, rule := lineOf(v)
	return rule
}

// lineOf is v's line and its tier's rule (Line, LineRule).
func lineOf(v string) (line, rule string) {
	switch major, minor := semver.Major(v), semver.MajorMinor(v); {
	case major != "v0":
		return major, "from v1.0.0 each major version is a line of its own"
	case minor != "v0.0":
		return minor, "below v1.0.0 each minor version is a line of its own"
	}
	return releaseOf(v), "below v0.1.0 each release is a line of its own"
}

// releaseOf is v without its prerelease: the release v leads up to.
func releaseOf(v string) string { return strings.TrimSuffix(v, semver.Prerelease(v)) }

// ParseManifest reads data, a nomi.toml's text, for its package name,
// the nomi it needs and its [dependencies] table: the part of the schema
// this package reads, which analysis's check of the whole file runs too
// (ManifestReader.Check, analysis.ParseManifestIn), and which the writers
// of [dependencies] check alone. An entry holds `repo` and
// `version` or `commit`, with `subdir` when the package is not the
// repository's root, or `dir`, or all of them; two entries may not name one
// package, and no entry may take the package's own name or `std`. A
// workspace's declaration is readWorkspace's to read.
func ParseManifest(data []byte) (*Manifest, error) {
	var raw rawManifest
	_, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, err
	}
	// The release the package needs is read first: a package written for a
	// later nomi may write the rest in a form this one does not read.
	if err := NomiNeeded(raw.Package.Nomi); err != nil {
		err.Package = "package " + cmp.Or(raw.Package.Name, "with no name")
		return nil, err
	}
	if raw.Package.Nomi != "" && !IsVersion(raw.Package.Nomi) {
		return nil, &ManifestProblem{Key: []string{"package", "nomi"}, Msg: fmt.Sprintf("nomi: %q is not a nomi release, such as v0.2.0", raw.Package.Nomi)}
	}
	f := &Manifest{Name: raw.Package.Name, Nomi: raw.Package.Nomi}
	names := make([]string, 0, len(raw.Dependencies))
	for name := range raw.Dependencies {
		names = append(names, name)
	}
	sort.Strings(names)
	byID := map[ID]string{}
	for _, name := range names {
		e := raw.Dependencies[name]
		at := func(key string, format string, args ...any) error {
			path := []string{"dependencies", name}
			if key != "" {
				path = append(path, key)
			}
			return &ManifestProblem{Key: path, Msg: fmt.Sprintf(format, args...)}
		}
		switch {
		case !IsPackageName(name):
			return nil, at("", "%q is not a dependency name: a name is lowercase letters, digits and `_`, as an import writes it", name)
		case name == "std":
			return nil, at("", "std is the standard library's name; name the dependency another way")
		case name == f.Name:
			return nil, at("", "%s is this package's own name; name the dependency another way", name)
		case e.Repo == "" && e.Subdir != "":
			return nil, at("subdir", "%s has a `subdir` but no `repo`", name)
		case e.Repo == "" && e.Version != "":
			return nil, at("version", "%s has a version but no `repo`", name)
		case e.Repo == "" && e.Commit != "":
			return nil, at("commit", "%s has a commit but no `repo`", name)
		case e.Repo == "" && e.Dir == "":
			return nil, at("", "%s needs `repo` and `version`, or `dir`", name)
		case e.Version != "" && e.Commit != "":
			return nil, at("commit", "%s has a `version` and a `commit`; keep one", name)
		case e.Repo != "" && e.Version == "" && e.Commit == "":
			return nil, at("", "%s needs a `version` or a `commit`", name)
		}
		id := ID{Repo: e.Repo, Subdir: e.Subdir}
		if e.Repo != "" {
			if err := CheckRepo(e.Repo); err != nil {
				return nil, at("repo", "%s: %v", name, err)
			}
			if e.Subdir != "" {
				if err := CheckSubdir(e.Subdir); err != nil {
					return nil, at("subdir", "%s: %v", name, err)
				}
			}
			switch {
			case e.Commit != "":
				if problem := CommitProblem(e.Commit); problem != "" {
					return nil, at("commit", "%s: %s", name, problem)
				}
				e.Version = e.Commit
			case !IsVersion(e.Version):
				return nil, at("version", "%s: %q is not a version a tag names, such as v1.2.3", name, e.Version)
			}
			if other, taken := byID[id]; taken {
				return nil, at("repo", "%s and %s both name %s; keep one", other, name, id)
			}
			byID[id] = name
		}
		r := Requirement{Name: name, ID: id, Version: e.Version, Dir: e.Dir}
		if e.Effects != nil {
			r.Effects = append([]string{}, *e.Effects...)
		}
		if e.EffectsInTests != nil {
			r.EffectsInTests = append([]string{}, *e.EffectsInTests...)
		}
		f.Requires = append(f.Requires, r)
	}
	return f, nil
}

// ManifestReader is how a process reads a nomi.toml and a
// nomi-workspace.toml beyond what this package checks of them. Check
// answers the first problem in data, the text of the nomi.toml in dir,
// against the whole schema, at its line: every table and key, the
// required [package] name and each table's values, and a package that
// needs a later nomi as its bare *NomiVersionError. Read answers the text
// of the nomi-workspace.toml at path, Keys the first key of data, its
// text, that the file may not hold, at its line, or nil, and Place
// problem, found in that file, at its line. The analysis package holds
// the schema and its checks (analysis.ParseManifestIn), the text an
// editor has open (analysis.ReadManifestFile), and the reporting of
// unknown keys and the finding of lines, and installs its reader
// (SetManifestReader) when it starts, since this package cannot import
// it. With none, a nomi.toml is checked only in the [package] and
// [dependencies] this package reads (ParseManifest), a
// nomi-workspace.toml is read from the disk, a problem names the file and
// not its line, and no key is refused.
type ManifestReader struct {
	Check func(dir string, data []byte) error
	Read  func(path string) ([]byte, error)
	Keys  func(path string, data []byte) error
	Place func(path string, data []byte, problem *ManifestProblem) error
}

var (
	readerMu sync.Mutex
	reader   ManifestReader
)

// SetManifestReader installs r (ManifestReader).
func SetManifestReader(r ManifestReader) {
	readerMu.Lock()
	defer readerMu.Unlock()
	reader = r
}

func manifestReader() ManifestReader {
	readerMu.Lock()
	defer readerMu.Unlock()
	return reader
}

// ReadManifest is ParseManifest of the nomi.toml in dir, or nil and no
// error when dir has none: what this package reads of a package whose
// nomi.toml its caller checks, the main package a walk starts from. The
// front end checks the project's whole (analysis.LoadManifest), and the
// commands that edit [dependencies] check only that table, so an error
// elsewhere in the file never blocks them. A package that needs a later
// nomi is its *NomiVersionError, which the caller names the package in.
func ReadManifest(dir string) (*Manifest, error) { return readManifest(dir, false) }

// readReached is ReadManifest of a package the main package reaches, a
// dependency at any depth or another member of its workspace, held to the
// whole schema and its checks first (ManifestReader.Check), as the main
// package's own nomi.toml is by its caller.
func readReached(dir string) (*Manifest, error) { return readManifest(dir, true) }

// readManifest is the one place a nomi.toml's file is read: ReadManifest,
// and with check readReached.
func readManifest(dir string, check bool) (*Manifest, error) {
	path := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if c := manifestReader().Check; check && c != nil {
		if err := c(dir, data); err != nil {
			return nil, err
		}
	}
	f, err := ParseManifest(data)
	var tooOld *NomiVersionError
	if errors.As(err, &tooOld) {
		tooOld.Path = path
		return nil, tooOld
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// LocalDir is r's directory, absolute, for the package rooted at root.
func (r Requirement) LocalDir(root string) string {
	dir := filepath.FromSlash(r.Dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Clean(dir)
}

// String is r as `nomi add` names it in its output: its repository and
// subdirectory, or its directory.
func (r Requirement) String() string {
	if !r.ID.IsZero() {
		return r.ID.String()
	}
	return strings.TrimSuffix(filepath.ToSlash(r.Dir), "/")
}

// neighbor is the requirement r, an entry of the package m fetched from its
// repository, as the graph reads it: an entry with a dir names the package
// in that directory of m's repository at m's tag, and one whose dir leaves
// the repository is an error. Answers false for a dir naming m itself.
func neighbor(m Package, r Requirement) (Requirement, bool, error) {
	if r.Dir == "" {
		return r, true, nil
	}
	dir := filepath.ToSlash(r.Dir)
	joined := path.Join(m.Subdir, dir)
	if filepath.IsAbs(r.Dir) || path.IsAbs(dir) || strings.Contains(dir, ":") || joined == ".." || strings.HasPrefix(joined, "../") {
		return Requirement{}, false, fmt.Errorf("%s: %s: [dependencies] %s: dir %q leaves the repository; a fetched package names a package of another repository by `repo` and `version`",
			m, ManifestName, r.Name, r.Dir)
	}
	if joined == "." {
		joined = ""
	}
	if joined == m.Subdir {
		return Requirement{}, false, nil
	}
	if joined != "" {
		if err := CheckSubdir(joined); err != nil {
			return Requirement{}, false, fmt.Errorf("%s: %s: [dependencies] %s: dir %q: %v", m, ManifestName, r.Name, r.Dir, err)
		}
	}
	return Requirement{Name: r.Name, ID: ID{Repo: m.Repo, Subdir: joined}, Version: m.Version}, true, nil
}
