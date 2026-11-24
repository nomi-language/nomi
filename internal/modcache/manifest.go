package modcache

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/semver"
)

// ManifestName is the file that makes a directory a Nomi module.
const ManifestName = "nomi.toml"

// DependencyEntry is one entry of nomi.toml's [dependencies] table as it
// decodes: `repo`, `version` or `commit`, and an optional `subdir` name a
// module fetched from its repository, and `dir` a directory that holds the
// module, relative to the manifest's directory. analysis's manifest
// declares the table with this type, so the keys it checks are the ones
// this package reads.
type DependencyEntry struct {
	Repo    string `toml:"repo"`
	Subdir  string `toml:"subdir"`
	Version string `toml:"version"`
	Commit  string `toml:"commit"`
	Dir     string `toml:"dir"`
}

// ID is a fetched module's identity in the graph: the repository that
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

// IsZero reports whether id names no repository: a module loaded from a
// directory whose entry names none.
func (id ID) IsZero() bool { return id.Repo == "" }

// Module is a module at a version: a tag of its repository, vX.Y.Z, or
// for a module taken at a commit, the commit's 40 hex digits (IsCommit),
// which no tag can be.
type Module struct {
	ID
	Version string
}

// String is m as `nomi` names it in its output: its path and version.
func (m Module) String() string { return m.ID.String() + " " + m.Version }

// Requirement is one checked entry of a [dependencies] table.
type Requirement struct {
	// Name is the entry's key: the name the module's files import the
	// dependency by.
	Name string
	// ID is the module's repository and subdirectory, zero for an entry
	// with only a dir.
	ID
	// Version is the tag a consumer of this module takes the dependency at,
	// or the commit for an entry with a `commit` (IsCommit), "" for an
	// entry with only a dir.
	Version string
	// Dir is the dependency's directory as the entry writes it, or "". In
	// a module on disk it is a path from the manifest's directory, and it
	// stands for the module ID names, when there is one, wherever the
	// graph reaches it. In a fetched module it names a neighbor in the
	// same repository at the same tag.
	Dir string
}

// ModuleFile is what this package reads of a nomi.toml: the module's
// name, the lowest nomi release its source needs ([module].nomi, "" when
// it states none), its [dependencies] table, sorted by name, and its
// [indirect] table, sorted by repository.
type ModuleFile struct {
	Name     string
	Nomi     string
	Requires []Requirement
	Indirect []Floor
}

// Floor is one entry of a module's [indirect] table: the lowest version
// the module takes a repository at that it reaches only through its
// dependencies. It is a requirement on the repository in selection, by
// the module that writes it, and it makes no module of the repository
// needed: a repository no requirement reaches is not in the build
// whatever its floor. `nomi update` writes the project's.
type Floor struct {
	Repo, Version string
}

// Require answers the requirement named name.
func (f *ModuleFile) Require(name string) (Requirement, bool) {
	for _, r := range f.Requires {
		if r.Name == name {
			return r, true
		}
	}
	return Requirement{}, false
}

// ManifestProblem is an error in the part of a nomi.toml this package
// reads, at the key path Key (`dependencies`, `sqlite`, `version`), so a
// caller holding the text can say its line.
type ManifestProblem struct {
	Key []string
	Msg string
}

func (p *ManifestProblem) Error() string {
	if len(p.Key) > 0 && (p.Key[0] == "dependencies" || p.Key[0] == "indirect") {
		return "[" + p.Key[0] + "] " + p.Msg
	}
	return "[module] " + p.Msg
}

// moduleName is the form of a name an import path can start with: a
// [dependencies] key, and the [module].name `nomi new` and `nomi init`
// write.
var moduleName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// IsModuleName reports whether name has the form of a name an import path
// can start with: lowercase letters, digits and `_`, not starting with a
// digit.
func IsModuleName(name string) bool { return moduleName.MatchString(name) }

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

// Line is the compatible line of the version v: its major version from v1
// on (v1, v2), and below v1 its minor version (v0.1, v0.2), since by
// convention each 0.x line may break the one before it. A prerelease is in
// the line of its release. `nomi update` moves a repository within its
// line, and two requirements on one repository in different lines are an
// error (walk).
func Line(v string) string {
	if semver.Major(v) == "v0" {
		return semver.MajorMinor(v)
	}
	return semver.Major(v)
}

// ParseModuleFile reads data, a nomi.toml's text, for its module name,
// the nomi it needs, its [dependencies] table and its [indirect] table.
// The other tables are analysis's to check (analysis.LoadManifest). An
// entry holds `repo` and `version` or `commit`, with `subdir` when the
// module is not the repository's root, or `dir`, or all of them; two
// entries may not name one module, and no entry may take the module's own
// name or `std`. An [indirect] entry is a repository and a version tag,
// of a repository [dependencies] names no module of.
func ParseModuleFile(data []byte) (*ModuleFile, error) {
	var raw struct {
		Module struct {
			Name string `toml:"name"`
			Nomi string `toml:"nomi"`
		} `toml:"module"`
		Dependencies map[string]DependencyEntry `toml:"dependencies"`
		Indirect     map[string]string          `toml:"indirect"`
	}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, err
	}
	// The release the module needs is read first: a module written for a
	// later nomi may write the rest in a form this one does not read.
	if err := NomiNeeded(raw.Module.Nomi); err != nil {
		err.Module = "module " + cmp.Or(raw.Module.Name, "with no name")
		return nil, err
	}
	if raw.Module.Nomi != "" && !IsVersion(raw.Module.Nomi) {
		return nil, &ManifestProblem{Key: []string{"module", "nomi"}, Msg: fmt.Sprintf("nomi: %q is not a nomi release, such as v0.2.0", raw.Module.Nomi)}
	}
	f := &ModuleFile{Name: raw.Module.Name, Nomi: raw.Module.Nomi}
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
		case !IsModuleName(name):
			return nil, at("", "%q is not a dependency name: a name is lowercase letters, digits and `_`, as an import writes it", name)
		case name == "std":
			return nil, at("", "std is the standard library's name; name the dependency another way")
		case name == f.Name:
			return nil, at("", "%s is this module's own name; name the dependency another way", name)
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
		f.Requires = append(f.Requires, Requirement{Name: name, ID: id, Version: e.Version, Dir: e.Dir})
	}
	repos := make([]string, 0, len(raw.Indirect))
	for repo := range raw.Indirect {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	for _, repo := range repos {
		v := raw.Indirect[repo]
		at := func(format string, args ...any) error {
			return &ManifestProblem{Key: []string{"indirect", repo}, Msg: fmt.Sprintf(format, args...)}
		}
		if err := CheckRepo(repo); err != nil {
			return nil, at("%v", err)
		}
		if !IsVersion(v) {
			return nil, at("%s: %q is not a version a tag names, such as v1.2.3; an [indirect] entry is the lowest version the build takes the repository at", repo, v)
		}
		for _, r := range f.Requires {
			if r.Repo == repo && r.Dir == "" {
				return nil, at("%s: [dependencies] %s names it, which gives its version; an [indirect] entry is for a repository only a dependency names", repo, r.Name)
			}
		}
		f.Indirect = append(f.Indirect, Floor{Repo: repo, Version: v})
	}
	return f, nil
}

// ReadModuleFile is ParseModuleFile of the nomi.toml in dir, or nil and no
// error when dir has none. A module that needs a later nomi is its
// *NomiVersionError, which the caller names the module in.
func ReadModuleFile(dir string) (*ModuleFile, error) {
	path := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	f, err := ParseModuleFile(data)
	var tooOld *NomiVersionError
	if errors.As(err, &tooOld) {
		return nil, tooOld
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// LocalDir is r's directory, absolute, for the module rooted at root.
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

// neighbor is the requirement r, an entry of the module m fetched from its
// repository, as the graph reads it: an entry with a dir names the module
// in that directory of m's repository at m's tag, and one whose dir leaves
// the repository is an error. Answers false for a dir naming m itself.
func neighbor(m Module, r Requirement) (Requirement, bool, error) {
	if r.Dir == "" {
		return r, true, nil
	}
	dir := filepath.ToSlash(r.Dir)
	joined := path.Join(m.Subdir, dir)
	if filepath.IsAbs(r.Dir) || path.IsAbs(dir) || strings.Contains(dir, ":") || joined == ".." || strings.HasPrefix(joined, "../") {
		return Requirement{}, false, fmt.Errorf("%s: %s: [dependencies] %s: dir %q leaves the repository; a fetched module names a module of another repository by `repo` and `version`",
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
