package lsp

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/pkgcache"
)

// A workspace's nomi-workspace.toml is a manifest document as a package's
// nomi.toml is (isManifestURI): its text is the edited manifest the
// analysis reads (analysis.SetEditedManifest), its own errors are
// published at their lines, and an edit, a save or a close rechecks the
// open documents, members among them (manifestChanged). What differs is
// what it holds: its errors are the workspace lookup's
// (pkgcache.CheckWorkspace), it completes its own keys (workspaceCompletion),
// and a hover on an entry of members or exclude names the packages it
// takes in (workspaceHover). In a package's nomi.toml, a hover on
// [package] or its name says which workspace the package is in
// (packageHover).

// isWorkspaceURI reports whether uri is a workspace's nomi-workspace.toml.
func isWorkspaceURI(uri string) bool {
	return filepath.Base(uriToPath(uri)) == pkgcache.WorkspaceName
}

// workspaceTextDiagnostics is the error the workspace lookup finds in
// text, the nomi-workspace.toml at path, at its line, as `nomi check` in a
// member reports it; none for text that declares a workspace.
func workspaceTextDiagnostics(path, text string) []protocol.Diagnostic {
	_, err := pkgcache.CheckWorkspace(path, []byte(text))
	if err == nil {
		return nil
	}
	line, message := analysis.ManifestErrorAt(path, []byte(text), err)
	return []protocol.Diagnostic{manifestDiagnostic(text, line, message)}
}

// workspaceCompletion is what completes at pos in text, a
// nomi-workspace.toml: where a key is written outside any table, each key
// and table of the file's schema (analysis.WorkspaceKeys) that the text
// does not write yet, as `key = []` or `[table]`.
func workspaceCompletion(text string, pos protocol.Position) []protocol.CompletionItem {
	at := manifestPlaceAt(text, pos)
	if at.table != nil || at.key != "" || strings.Contains(at.lineText[:at.col], "=") {
		return nil
	}
	start := at.col
	for start > 0 && !strings.ContainsRune(" \t[", rune(at.lineText[start-1])) {
		start--
	}
	if start > 0 && at.lineText[start-1] == '[' {
		start--
	}
	replace := at.lines.utf16Range(posRange(at.line, start+1, at.line, at.col+1))
	data := []byte(text)
	keys, tables := analysis.WorkspaceKeys()
	var items []protocol.CompletionItem
	add := func(label, insert string, kind protocol.CompletionItemKind) {
		if analysis.ManifestKeyLine(data, label) > 0 {
			return
		}
		items = append(items, protocol.CompletionItem{Label: label, Kind: &kind, TextEdit: protocol.TextEdit{Range: replace, NewText: insert}})
	}
	for _, k := range keys {
		add(k, k+" = []", protocol.CompletionItemKindProperty)
	}
	for _, t := range tables {
		add(t, "["+t+"]", protocol.CompletionItemKindModule)
	}
	return items
}

// workspaceHover is the hover at pos in the nomi-workspace.toml at uri: on
// an entry of members, each package directory it takes in, with the
// package's name; on an entry of exclude, each directory it names, whose
// packages, and those below it, are on their own.
func (s *Server) workspaceHover(uri string, pos protocol.Position) *protocol.Hover {
	text, _ := s.contentForURI(protocol.DocumentUri(uri))
	at := manifestPlaceAt(text, pos)
	entry, start, end, ok := at.quoted()
	if !ok || at.table != nil || at.key != "members" && at.key != "exclude" {
		return nil
	}
	root := filepath.Dir(uriToPath(uri))
	var lines []string
	for _, dir := range pkgcache.EntryDirs(root, entry) {
		rel, _ := filepath.Rel(root, dir)
		rel = filepath.ToSlash(rel)
		name := ""
		if mf, err := pkgcache.ReadManifest(dir); err == nil && mf != nil {
			name = mf.Name
		}
		switch {
		case name != "":
			lines = append(lines, fmt.Sprintf("%s: package %s", rel, name))
		case at.key == "exclude":
			lines = append(lines, rel)
		}
	}
	var md string
	switch {
	case len(lines) == 0 && at.key == "members":
		md = fmt.Sprintf("`%s` names no package's directory.", entry)
	case len(lines) == 0:
		md = fmt.Sprintf("`%s` names no directory.", entry)
	case at.key == "members":
		md = "Members:\n\n```text\n" + strings.Join(lines, "\n") + "\n```"
	default:
		md = "Excluded, with every package below it: each keeps a nomi.lock of its own.\n\n```text\n" + strings.Join(lines, "\n") + "\n```"
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: md},
		Range:    ptrRange(at.quotedRange(start, end)),
	}
}

// packageHover is the hover at at, a place in text, the nomi.toml at
// path, when it is on the [package] header or its name: which workspace
// the package is in (pkgcache.NearestWorkspace), a member of, excluded
// from or under without being listed; nil elsewhere, and for a package
// under no workspace.
func packageHover(path, text string, at manifestPlace) *protocol.Hover {
	data := []byte(text)
	if at.line != analysis.ManifestKeyLine(data, "package") && at.line != analysis.ManifestKeyLine(data, "package", "name") {
		return nil
	}
	root := filepath.Dir(path)
	ws, member, err := pkgcache.NearestWorkspace(root)
	var unlisted *pkgcache.UnlistedError
	var md string
	switch {
	case errors.As(err, &unlisted):
		md = fmt.Sprintf("Under the workspace at `%s`, whose members do not list it: add `%s` to its members, or to its exclude.", filepath.Dir(unlisted.Decl), unlisted.Entry)
	case err != nil || ws == nil:
		return nil
	case member:
		md = fmt.Sprintf("A member of the workspace at `%s` (%s), whose nomi.lock locks every member's packages.", ws.Root, pkgcache.WorkspaceName)
	default:
		md = fmt.Sprintf("Excluded from the workspace at `%s` (%s): it keeps a nomi.lock of its own.", ws.Root, pkgcache.WorkspaceName)
	}
	l := at.lineText
	first := len(l) - len(strings.TrimLeft(l, " \t"))
	rng := at.lines.utf16Range(posRange(at.line, first+1, at.line, len(strings.TrimRight(l, " \t\r"))+1))
	return &protocol.Hover{Contents: protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: md}, Range: &rng}
}
