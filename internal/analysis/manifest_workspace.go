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

// internal/pkgcache reads the nomi.toml of each package the main package
// reaches, each dependency at any depth and each other member of its
// workspace, and the workspace's nomi-workspace.toml, and checks them with
// this package's code, which it cannot import: this package installs it as
// pkgcache's ManifestReader when it starts. Check is ParseManifestIn, the
// one schema and its checks, so a dependency's nomi.toml is held to what
// the project's own is held to; Read is ReadManifestFile, so the
// workspace's lookup reads the text an editor holds; Keys and Place are
// the reporting of unknown keys and the finding of lines nomi.toml's
// errors use. A problem in a file is then a ManifestError at its line, and
// a build that meets one stops with that one `manifest` error
// (manifestTypeError), as a build whose nomi.toml is in error does.
func init() {
	pkgcache.SetManifestReader(pkgcache.ManifestReader{
		Check: func(dir string, data []byte) error { _, err := ParseManifestIn(dir, data); return err },
		Read:  ReadManifestFile,
		Keys:  workspaceKeys,
		Place: placeManifestProblem,
	})
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
// text of the file at path, at the line that writes it (manifestLine).
func placeManifestProblem(path string, data []byte, problem *pkgcache.ManifestProblem) error {
	return (&ManifestError{Path: path, Line: manifestLine(data, problem.Key, problem.Value)}).with("%s", problem.Error())
}

// manifestLine is the line of data, a manifest's text, that writes the
// place pkgcache names with a key path and a value
// (pkgcache.ManifestProblem's Key and Value): the line of value in the
// list at key when value is set (ManifestEntryLine), else the key's.
func manifestLine(data []byte, key []string, value string) int {
	if value != "" {
		return ManifestEntryLine(data, value, key...)
	}
	return ManifestKeyLine(data, key...)
}
