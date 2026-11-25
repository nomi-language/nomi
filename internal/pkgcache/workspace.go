package pkgcache

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Workspace is several packages developed together under one directory,
// with one nomi.lock (WorkspaceOf).
type Workspace struct {
	// Root is the directory that holds the workspace's nomi-workspace.toml
	// (Decl) and the nomi.lock of every member.
	Root string
	// Decl is the workspace's nomi-workspace.toml.
	Decl string
	// Members are the root directories of its packages, sorted.
	Members []string
	// exclude is its exclude entries as written: the directories whose
	// packages, and every package below them, are on their own.
	exclude []string
}

// WorkspaceEntries is what ParseWorkspace reads of a nomi-workspace.toml:
// its members and exclude entries as written.
type WorkspaceEntries struct {
	Members, Exclude []string
}

// ParseWorkspace reads data, a nomi-workspace.toml's text, for its
// entries. members is required, with at least one entry, and exclude is
// optional; each entry of either is listed once and is a clean slash path
// below the file's directory in which `*` is the only pattern character
// (entryProblem). Which directories the entries name is the disk's to
// answer (expand), and the keys the file may hold the installed reader's
// (ManifestReader).
func ParseWorkspace(data []byte) (*WorkspaceEntries, error) {
	var raw WorkspaceFile
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, err
	}
	switch {
	case !md.IsDefined("members"):
		return nil, &ManifestProblem{Key: []string{"members"}, Msg: "a workspace needs members, the directories of its packages from here, such as members = [\"billing\", \"lib/*\"]"}
	case len(raw.Members) == 0:
		return nil, &ManifestProblem{Key: []string{"members"}, Msg: "members lists no package: write each member's directory from here, such as members = [\"billing\", \"lib/*\"]"}
	}
	for _, list := range []struct {
		key     string
		entries []string
	}{{"members", raw.Members}, {"exclude", raw.Exclude}} {
		seen := map[string]bool{}
		for _, e := range list.entries {
			at := func(format string, args ...any) error {
				return &ManifestProblem{Key: []string{list.key}, Value: e, Msg: list.key + ": " + fmt.Sprintf(format, args...)}
			}
			if problem := entryProblem(e); problem != "" {
				return nil, at("%q %s", e, problem)
			}
			if seen[e] {
				return nil, at("lists %s twice", e)
			}
			seen[e] = true
		}
	}
	return &WorkspaceEntries{Members: raw.Members, Exclude: raw.Exclude}, nil
}

// entryProblem says what is wrong with e as an entry of members or
// exclude, or "": an entry is a clean slash path below the workspace's
// root, in which `*` matches within one path segment. No entry matches
// across segments: members would then mean walking every directory below
// the root at each lookup.
func entryProblem(e string) string {
	switch {
	case e == "" || e == "." || e == ".." || path.IsAbs(e) || strings.ContainsAny(e, `:\`) ||
		path.Clean(e) != e || strings.HasPrefix(e, "../"):
		return "is not a directory below the workspace's root: write a clean slash path from the root, such as billing or lib/sql/*"
	case strings.ContainsAny(e, "?[]"):
		return "holds a pattern character other than `*`, which matches within one path segment"
	case strings.Contains(e, "**"):
		return "holds `**`: `*` matches within one path segment, and an entry matches no more segments than it writes"
	}
	return ""
}

// excludes reports whether the exclude entry e names the directory rel,
// a slash path from the workspace's root, or a directory above it.
func excludes(e, rel string) bool {
	es, rs := strings.Split(e, "/"), strings.Split(rel, "/")
	if len(es) > len(rs) {
		return false
	}
	for i, seg := range es {
		if ok, _ := path.Match(seg, rs[i]); !ok {
			return false
		}
	}
	return true
}

// workspaceDecl is a nomi-workspace.toml as readWorkspace reads it: its
// path and text, and its entries.
type workspaceDecl struct {
	path string
	data []byte
	*WorkspaceEntries
}

// problem is p, found in d, as an error: at its line when a reader is
// installed, else naming the file.
func (d *workspaceDecl) problem(p *ManifestProblem) error {
	if place := manifestReader().Place; place != nil {
		return place(d.path, d.data, p)
	}
	return fmt.Errorf("%s: %w", d.path, p)
}

// errUnreadable is a directory, or a nomi-workspace.toml, the lookup could
// not read (WorkspaceOf).
var errUnreadable = errors.New("cannot read")

// readWorkspace reads the nomi-workspace.toml in dir, or answers nil when
// dir has none (declOf).
func readWorkspace(dir string) (*workspaceDecl, error) {
	file := filepath.Join(dir, WorkspaceName)
	read := manifestReader().Read
	if read == nil {
		read = os.ReadFile
	}
	data, err := read(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || isDir(file) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %s: %v", errUnreadable, file, err)
	}
	return declOf(file, data)
}

// declOf is data, the text of the nomi-workspace.toml at file, as a
// declaration: every key one the file may hold (ManifestReader.Keys),
// then ParseWorkspace's checks. A directory that holds a nomi.toml too is
// an error: a workspace's root is not a package.
func declOf(file string, data []byte) (*workspaceDecl, error) {
	if keys := manifestReader().Keys; keys != nil {
		if err := keys(file, data); err != nil {
			return nil, err
		}
	}
	d := &workspaceDecl{path: file, data: data}
	var err error
	d.WorkspaceEntries, err = ParseWorkspace(data)
	var problem *ManifestProblem
	switch {
	case errors.As(err, &problem):
		return nil, d.problem(problem)
	case err != nil:
		return nil, fmt.Errorf("%s: %w", file, err)
	case isFile(filepath.Join(filepath.Dir(file), ManifestName)):
		return nil, d.problem(&ManifestProblem{Key: []string{"members"}, Msg: "this directory holds a nomi.toml too: a workspace's root is not a package; " +
			"move the package into a directory of its own and name it in members"})
	}
	return d, nil
}

// CheckWorkspace is the workspace that data, the text of the
// nomi-workspace.toml at path, declares, as the lookup reads it from the
// disk (WorkspaceOf), with each problem at its line: what the language
// server reports in the file while an editor holds it.
func CheckWorkspace(path string, data []byte) (*Workspace, error) {
	d, err := declOf(path, data)
	if err != nil {
		return nil, err
	}
	ws, err := expand(filepath.Dir(path), d, "")
	if err != nil {
		return nil, err
	}
	return ws, ws.strayLock()
}

// EntryDirs is the directories under root that entry, an entry of a
// nomi-workspace.toml's members or exclude, names, sorted: the directory it
// writes, or each one its `*` matches within a path segment. expand takes
// the members among them, and the language server's hover on an entry
// names them.
func EntryDirs(root, entry string) []string {
	pattern := filepath.Join(root, filepath.FromSlash(entry))
	matches := []string{pattern}
	if strings.Contains(entry, "*") {
		matches, _ = filepath.Glob(pattern)
	}
	var dirs []string
	for _, m := range matches {
		if isDir(m) {
			dirs = append(dirs, m)
		}
	}
	sort.Strings(dirs)
	return dirs
}

// expand is the workspace dir declares with d: each members entry's
// directories, sorted. An entry with no `*` names one directory, which must
// hold a nomi.toml; an entry with `*` names each directory it matches that
// holds one, and skips the rest. Every directory named holds a package,
// and none holds a nomi-workspace.toml. Each member's nomi.toml but that
// of main, the package the lookup is for ("" for none), whose own is its
// caller's to check, is held to the whole schema (readReached). An
// exclude entry with no `*` names a directory, and no exclude entry names
// a member or a directory above one.
func expand(dir string, d *workspaceDecl, main string) (*Workspace, error) {
	ws := &Workspace{Root: dir, Decl: d.path, exclude: d.Exclude}
	seen := map[string]bool{}
	for _, entry := range d.Members {
		at := func(format string, args ...any) error {
			return d.problem(&ManifestProblem{Key: []string{"members"}, Value: entry, Msg: "members: " + fmt.Sprintf(format, args...)})
		}
		var dirs []string
		for _, m := range EntryDirs(dir, entry) {
			if isFile(filepath.Join(m, ManifestName)) {
				dirs = append(dirs, m)
			}
		}
		if len(dirs) == 0 && !strings.Contains(entry, "*") {
			return nil, at("%s holds no nomi.toml; a member is a package's directory", entry)
		}
		for _, member := range dirs {
			rel := filepath.ToSlash(relTo(dir, member))
			if isFile(filepath.Join(member, WorkspaceName)) {
				return nil, at("%s holds a %s of its own; a member is a package, and holds no workspace", rel, WorkspaceName)
			}
			if main == "" || !sameDir(member, main) {
				if _, err := readReached(member); err != nil {
					if tooOld := (*NomiVersionError)(nil); errors.As(err, &tooOld) {
						tooOld.Package += " (a member of this workspace)"
						return nil, tooOld
					}
					return nil, &PackageManifestError{Err: err, By: d.path, Entry: entry, Member: true}
				}
			}
			if !seen[member] {
				seen[member] = true
				ws.Members = append(ws.Members, member)
			}
		}
	}
	sort.Strings(ws.Members)
	for _, e := range d.Exclude {
		at := func(format string, args ...any) error {
			return d.problem(&ManifestProblem{Key: []string{"exclude"}, Value: e, Msg: "exclude: " + fmt.Sprintf(format, args...)})
		}
		if !strings.Contains(e, "*") && !isDir(filepath.Join(dir, filepath.FromSlash(e))) {
			return nil, at("%s is no directory", e)
		}
		for _, m := range ws.Members {
			if rel := filepath.ToSlash(relTo(dir, m)); excludes(e, rel) {
				return nil, at("%s takes in %s, which members names; a package is a member or excluded, not both", e, rel)
			}
		}
	}
	return ws, nil
}

// Excludes reports whether dir, an absolute directory, is one an exclude
// entry of w names or below one: a package there is on its own.
func (w *Workspace) Excludes(dir string) bool {
	rel, err := filepath.Rel(w.Root, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	rel = filepath.ToSlash(rel)
	return slices.ContainsFunc(w.exclude, func(e string) bool { return excludes(e, rel) })
}

// WorkspaceAt answers the workspace whose root is dir: the one the
// nomi-workspace.toml in dir declares, or nil when dir holds none. The
// commands that act on a whole workspace at its root ask it (cmd/nomi's
// root commands, frontend.WorkspaceMembers).
func WorkspaceAt(dir string) (*Workspace, error) {
	if !isFile(filepath.Join(dir, WorkspaceName)) {
		return nil, nil
	}
	return WorkspaceOf(dir)
}

// has reports whether the directory info describes is one of w's members.
func (w *Workspace) has(info os.FileInfo) bool {
	for _, m := range w.Members {
		if mi, err := os.Stat(m); err == nil && os.SameFile(info, mi) {
			return true
		}
	}
	return false
}

// UnlistedError is a package under a workspace whose members do not name
// it: the nearest nomi-workspace.toml above a package decides which
// workspace it is in (WorkspaceOf), so it is in that one or in error.
type UnlistedError struct {
	// Manifest is the package's nomi.toml, Decl the workspace's
	// nomi-workspace.toml, and Entry the package's directory from the
	// workspace's root, as a members entry writes it.
	Manifest, Decl, Entry string
}

func (e *UnlistedError) Error() string { return e.Manifest + ":1: " + e.Message() }

// Message is the error's message without its place, the first line of the
// package's nomi.toml.
func (e *UnlistedError) Message() string {
	return fmt.Sprintf("this package is under the workspace %s, which does not list it; add %q to its members, or to its exclude if it keeps a nomi.lock of its own",
		e.Decl, e.Entry)
}

// WorkspaceOf answers the workspace that governs the package rooted at
// root, or nil: NearestWorkspace's when root is its member. Every place
// that finds a project's lock asks it: walk and internal/pkgfetch's
// fetcher read the lock at the workspace's root.
//
// A member that holds a nomi.lock of its own is an error: the
// workspace's root holds the one lock.
func WorkspaceOf(root string) (*Workspace, error) {
	ws, member, err := NearestWorkspace(root)
	if err != nil || !member {
		return nil, err
	}
	return ws, ws.strayLock()
}

// NearestWorkspace answers the workspace whose nomi-workspace.toml is
// nearest above the package rooted at root, or at root, and whether root
// is its member. The walk goes from root upward through every parent
// directory to the file system's root, with no bound (not an editor's
// folder, not a repository's top), and stops at the nearest
// nomi-workspace.toml, with nothing further up read: the workspace it
// declares governs root when root is its own directory or one of its
// members entries names root. A package an exclude entry names, or one
// below a directory an exclude entry names, is on its own: the answer is
// the workspace, and not a member. A package neither list names is an
// *UnlistedError, at any depth below the workspace's root. With no
// nomi-workspace.toml above it, or a directory the walk cannot read, root
// is on its own too, and the answer is nil. A directory that holds
// neither a nomi.toml nor a nomi-workspace.toml is no package, and in no
// workspace. Like the nearest nomi.toml (analysis.ProjectRoot), the
// answer is read from the disk each time. The language server's hover on
// a package's [package] says which of these it is.
func NearestWorkspace(root string) (ws *Workspace, member bool, err error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, false, err
	}
	info, err := os.Stat(abs)
	if err != nil || !isFile(filepath.Join(abs, ManifestName)) && !isFile(filepath.Join(abs, WorkspaceName)) {
		return nil, false, nil
	}
	for dir := abs; ; {
		d, err := readWorkspace(dir)
		switch {
		case errors.Is(err, errUnreadable):
			return nil, false, nil
		case err != nil:
			return nil, false, err
		case d != nil:
			ws, err := expand(dir, d, abs)
			if err != nil {
				return nil, false, err
			}
			if dir != abs && !ws.has(info) {
				if ws.Excludes(abs) {
					return ws, false, nil
				}
				return nil, false, &UnlistedError{Manifest: filepath.Join(abs, ManifestName), Decl: ws.Decl, Entry: filepath.ToSlash(relTo(dir, abs))}
			}
			return ws, true, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, false, nil
		}
		dir = parent
	}
}

// strayLock is the error for a member of w that holds a nomi.lock of its
// own, or nil.
func (w *Workspace) strayLock() error {
	for _, m := range w.Members {
		if lock := filepath.Join(m, LockName); isFile(lock) {
			return fmt.Errorf("%s: %s is a member of the workspace at %s, whose nomi.lock locks every member's packages; delete this nomi.lock, and `nomi fetch` writes the workspace's",
				lock, filepath.ToSlash(relTo(w.Root, m)), w.Root)
		}
	}
	return nil
}
