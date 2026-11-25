package analysis

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/pkgcache"
)

// A workspace's nomi-workspace.toml is read by internal/pkgcache, which
// finds the workspace that governs a package (pkgcache.WorkspaceOf), and
// checked with this package's reporting of unknown keys and finding of
// lines, the ones nomi.toml's errors use, which pkgcache cannot import:
// this package installs them as pkgcache's WorkspaceReader when it starts,
// with ReadManifestFile, so the lookup reads the text an editor holds. A
// problem in the file is then a ManifestError at its line, and a build of
// a member that meets one stops with that one `manifest` error
// (manifestTypeError), as a build whose nomi.toml is in error does.
func init() {
	pkgcache.SetWorkspaceReader(pkgcache.WorkspaceReader{Read: ReadManifestFile, Keys: workspaceKeys, Place: placeManifestProblem})
}

// WorkspaceKeys is the keys a nomi-workspace.toml may hold outside any
// table, and its tables, as its schema declares them (manifestTables):
// what the language server completes in the file.
func WorkspaceKeys() (keys, tables []string) {
	all := manifestTables(workspaceSchema.shape)
	return all[""].keys, topTables(all)
}

// workspaceKeys is the error for the first key of data, the text of the
// nomi-workspace.toml at path, that the file may not hold
// (pkgcache.WorkspaceFile's), at its line, or nil.
func workspaceKeys(path string, data []byte) error {
	md, err := toml.Decode(string(data), &pkgcache.WorkspaceFile{})
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return unknownManifestKey(data, md.Undecoded(), md, path, nil, workspaceSchema)
}

// AddWorkspaceMember is the fix that adds entry to the members list of
// data, the text of the nomi-workspace.toml at path, written as the
// effects lists are (writeListValue): a list written one entry per line
// gets it on a line of its own at its end, and any other is written again
// with it last. A package under the workspace that it does not list
// (pkgcache.UnlistedError) offers it.
func AddWorkspaceMember(path string, data []byte, entry string) (*parser.Fix, error) {
	entries, err := pkgcache.ParseWorkspace(data)
	if err != nil {
		return nil, err
	}
	members := entries.Members
	if slices.Contains(members, entry) {
		return nil, fmt.Errorf("members lists %s", entry)
	}
	lines := strings.Split(string(data), "\n")
	for _, run := range ManifestTableLayout(data).Body {
		if slices.Equal(run.Key, []string{"members"}) {
			edited := slices.Concat(lines[:run.First-1], writeListValue(lines, run, members, append(slices.Clone(members), entry)), lines[run.Last:])
			title := fmt.Sprintf("Add %q to the workspace's members", entry)
			return &parser.Fix{Title: title, Path: path, Edits: []parser.Edit{lineEdit(data, []byte(strings.Join(edited, "\n")))}}, nil
		}
	}
	return nil, errors.New("no line writes members")
}

// placeManifestProblem is problem, an error pkgcache found in data, the
// text of the file at path, at the line that writes it: its entry's when
// it names one (ManifestEntryLine), else its key's.
func placeManifestProblem(path string, data []byte, problem *pkgcache.ManifestProblem) error {
	line := ManifestKeyLine(data, problem.Key...)
	if problem.Value != "" {
		line = ManifestEntryLine(data, problem.Value, problem.Key...)
	}
	return (&ManifestError{Path: path, Line: line}).with("%s", problem.Error())
}
