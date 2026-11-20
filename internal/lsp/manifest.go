package lsp

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/capability"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/vmhost"
)

// manifestDelay is how long an edited nomi.toml waits for the next edit
// before the open documents are checked against it again.
const manifestDelay = 300 * time.Millisecond

// manifestEdits holds each edited nomi.toml's debounced recheck.
type manifestEdits struct {
	mu     sync.Mutex
	timers map[string]*time.Timer
}

// setManifest makes text the open nomi.toml at uri's content, publishes
// its own diagnostics, and rechecks what reads it once the edits pause.
func (s *Server) setManifest(uri, text string) {
	analysis.SetEditedManifest(uriToPath(uri), text)
	s.unused.opened(uri)
	s.publishManifestText(uri, text)
	e := &s.manifestEdits
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.timers == nil {
		e.timers = map[string]*time.Timer{}
	}
	if t := e.timers[uri]; t != nil {
		t.Stop()
	}
	e.timers[uri] = time.AfterFunc(manifestDelay, func() { s.manifestChanged(uri) })
}

// closeManifest returns a closed nomi.toml to the disk's text, publishes
// what the disk's text says, and rechecks what reads it.
func (s *Server) closeManifest(uri string) {
	analysis.ClearEditedManifest(uriToPath(uri))
	s.closeUnused(uri)
	e := &s.manifestEdits
	e.mu.Lock()
	if t := e.timers[uri]; t != nil {
		t.Stop()
		delete(e.timers, uri)
	}
	e.mu.Unlock()
	text, _ := s.contentForURI(protocol.DocumentUri(uri))
	s.publishManifestText(uri, text)
	s.manifestChanged(uri)
}

// publishManifestText publishes the error the front end finds in text, the
// nomi.toml at uri, at its line: what parsing it as the module's manifest
// answers. Empty text publishes nothing of its own.
func (s *Server) publishManifestText(uri, text string) {
	var diags []protocol.Diagnostic
	if text != "" {
		path := uriToPath(uri)
		if _, err := analysis.ParseManifestIn(filepath.Dir(path), []byte(text)); err != nil {
			line, message := analysis.ManifestErrorAt(err)
			diags = append(diags, manifestDiagnostic(text, line, message))
		}
	}
	s.publishInputDiagnostics(manifestSource(uri), map[string][]protocol.Diagnostic{uri: diags})
}

// manifestSource is the source key a nomi.toml's own diagnostics are
// kept under (inputDiagnostics.bySource).
func manifestSource(uri string) string { return "manifest " + uri }

// loweringSource is the source key the diagnostics a document's lowering
// finds in a nomi.toml are kept under.
func loweringSource(uri string) string { return "lowering " + uri }

// publishLoweringManifests publishes what uri's last lowering found in each
// nomi.toml, by its URI; nil clears what it found.
func (s *Server) publishLoweringManifests(uri string, manifests map[string][]protocol.Diagnostic) {
	s.publishInputDiagnostics(loweringSource(uri), manifests)
}

// publishInputDiagnostics records what source says about inputs and
// publishes each input whose diagnostics changed.
func (s *Server) publishInputDiagnostics(source string, inputs map[string][]protocol.Diagnostic) {
	changed := s.inputDiags.update(source, inputs)
	if s.notify == nil {
		return
	}
	for uri, diags := range changed {
		s.notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
			URI:         protocol.DocumentUri(uri),
			Diagnostics: diags,
		})
	}
}

// manifestDiagnostic is an error at line (1-based, 0 for none) of text, a
// nomi.toml, spanning the line's text.
func manifestDiagnostic(text string, line int, message string) protocol.Diagnostic {
	severity := protocol.DiagnosticSeverityError
	source := "nomi"
	lines := newLineIndex(text)
	rng := protocol.Range{}
	if line > 0 {
		l, _ := lines.line(uint32(line - 1))
		start := len(l) - len(strings.TrimLeft(l, " \t"))
		rng = lines.utf16Range(posRange(line, start+1, line, len(strings.TrimRight(l, " \t\r"))+1))
	}
	return protocol.Diagnostic{Range: rng, Severity: &severity, Source: &source, Message: message}
}

// manifestDiagnostics are the problems of a lowering that are in a
// nomi.toml, by its URI: the errors in the [capabilities] lists
// (vmhost's capabilityListProblems), each at its line of the text the
// lowering read, and those that carry a fix as parse errors at the
// diagnostic's start, which a code action in the file offers
// (manifestFixes): the entry of a dependency entries_required finds with
// none.
func (s *Server) manifestDiagnostics(problems, err error) (map[string][]protocol.Diagnostic, map[string][]parser.ParseError) {
	var ds frontend.Diagnostics
	if err != nil || !errors.As(problems, &ds) {
		return nil, nil
	}
	var out map[string][]protocol.Diagnostic
	var fixes map[string][]parser.ParseError
	for _, d := range ds {
		if !filepath.IsAbs(d.Path) || filepath.Base(d.Path) != "nomi.toml" {
			continue
		}
		uri := fileuri.FromPath(d.Path)
		text, _ := s.contentForURI(protocol.DocumentUri(uri))
		msg := d.Message
		for _, h := range d.Hints {
			msg += "\nhelp: " + h
		}
		diag := manifestDiagnostic(text, d.Line, msg)
		if d.Code != "" {
			diag.Code = &protocol.IntegerOrString{Value: d.Code}
		}
		if out == nil {
			out = map[string][]protocol.Diagnostic{}
		}
		out[uri] = append(out[uri], diag)
		if d.Fix != nil {
			if fixes == nil {
				fixes = map[string][]parser.ParseError{}
			}
			start := diag.Range.Start
			fixes[uri] = append(fixes[uri], parser.ParseError{
				Line: int(start.Line) + 1, Col: newLineIndex(text).byteCol(start.Line, start.Character),
				Message: d.Message, Fix: d.Fix,
			})
		}
	}
	return out, fixes
}

// manifestCompletion is what completes at pos in text, the nomi.toml at
// uri: a std module's name inside a list of std names; inside
// [capabilities.deps]' `go` or `go_in_tests`, each dependency whose code
// binds Go; and as a key under [capabilities.deps.modules], each
// dependency with its whole entry, what its own files reach, as the fix of
// a missing entry writes it (Reach.Tightest, analysis.ModuleEntryText). A
// dependency is a module the project's programs load Nomi files from
// (vmhost.Reach's Modules), from what they reach (projectReaches), so none
// is offered while a program does not check, and none the list or table
// already names. A name is quoted unless the cursor is inside a string; a
// key's entry replaces its quotes.
func (s *Server) manifestCompletion(uri, text string, pos protocol.Position) []protocol.CompletionItem {
	at := manifestPlaceAt(text, pos)
	line, lineText, col, lines := at.line, at.lineText, at.col, at.lines
	data := []byte(text)
	reach := func() *vmhost.Reach { return s.reaches.of(filepath.Dir(uriToPath(uri))) }
	var names []string
	var entries map[string][]string
	kind := protocol.CompletionItemKindModule
	switch list := at.list(); {
	case isStdList(list):
		names = capability.StdNames()
	case list == analysis.ListDepsGo || list == analysis.ListDepsGoInTests:
		kind = protocol.CompletionItemKindFolder
		if r := reach(); r != nil {
			for _, m := range r.ModuleReaches() {
				if m.BindsGo && !analysis.ManifestListHolds(data, m.Path, at.table[0], at.table[1], at.key) {
					names = append(names, m.Path)
				}
			}
		}
	case at.onModuleKey():
		kind = protocol.CompletionItemKindFolder
		if r := reach(); r != nil {
			entries = r.Tightest().Deps.Modules
			for _, m := range r.Modules {
				if analysis.ManifestKeyLine(data, "capabilities", "deps", "modules", m) == 0 {
					names = append(names, m)
				}
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	start, end := col, col
	for start > 0 && !strings.ContainsRune("\"[, \t", rune(lineText[start-1])) {
		start--
	}
	quoted := start > 0 && lineText[start-1] == '"'
	// A key's entry is written whole, its quotes too, when nothing follows
	// the key yet.
	entry := entries != nil && !strings.Contains(lineText[col:], "=")
	if entry && quoted {
		start--
		if end < len(lineText) && lineText[end] == '"' {
			end++
		}
	}
	replace := lines.utf16Range(posRange(line, start+1, line, end+1))
	items := make([]protocol.CompletionItem, len(names))
	for i, name := range names {
		k := kind
		item := protocol.CompletionItem{Label: name, Kind: &k}
		insert := name
		switch {
		case entry:
			insert = analysis.ModuleEntryText(name, entries[name])
			filter := name
			if quoted {
				filter = `"` + name
			}
			item.FilterText = &filter
		case !quoted:
			insert = fmt.Sprintf("%q", name)
		}
		item.TextEdit = protocol.TextEdit{Range: replace, NewText: insert}
		items[i] = item
	}
	return items
}

// manifestPlace is where a position stands in a nomi.toml's text: its
// 1-based line, that line's text and the position's byte offset in it,
// the table the line is in, and the key whose value holds the position,
// "" for none (the position is on a key, or between keys).
type manifestPlace struct {
	line     int
	lineText string
	col      int
	table    []string
	key      string
	lines    *lineIndex
}

// manifestPlaceAt is where pos stands in text, a nomi.toml.
func manifestPlaceAt(text string, pos protocol.Position) manifestPlace {
	data := []byte(text)
	at := manifestPlace{line: int(pos.Line) + 1, lines: newLineIndex(text)}
	at.lineText, _ = at.lines.line(pos.Line)
	at.col = min(max(at.lines.byteCol(pos.Line, pos.Character)-1, 0), len(at.lineText))
	at.table = analysis.ManifestTableAt(data, at.line)
	for _, run := range analysis.ManifestTableLayout(data, at.table...).Body {
		// A key with no `=` yet, being typed, has no value.
		if len(run.Key) == 1 && run.Line <= at.line && at.line <= run.Last && (at.line > run.Line || run.ValueStart > 0 && at.col > run.ValueStart) {
			at.key = run.Key[0]
		}
	}
	return at
}

// list is the capability list whose value holds the place, as a change
// names it (analysis.ListSelfStd): a list of std names or of modules, or
// ListDepsModules for a module's entry, whose module is the key. It is ""
// anywhere else.
func (at manifestPlace) list() string {
	switch strings.Join(at.table, ".") {
	case "capabilities.self":
		switch at.key {
		case "std":
			return analysis.ListSelfStd
		case "std_in_tests":
			return analysis.ListSelfStdInTests
		}
	case "capabilities.deps":
		switch at.key {
		case "std":
			return analysis.ListDepsStd
		case "std_in_tests":
			return analysis.ListDepsStdInTests
		case "go":
			return analysis.ListDepsGo
		case "go_in_tests":
			return analysis.ListDepsGoInTests
		}
	case "capabilities.deps.modules":
		if at.key != "" {
			return analysis.ListDepsModules
		}
	}
	return ""
}

// onModuleKey reports whether the place is a key under
// [capabilities.deps.modules], left of its `=`, where a dependency's path
// is written.
func (at manifestPlace) onModuleKey() bool {
	return strings.Join(at.table, ".") == "capabilities.deps.modules" && at.key == "" && !strings.Contains(at.lineText[:at.col], "=")
}

// quoted is the string on the place's line that holds it, without its
// quotes, and the byte offsets of its opening and closing quote; ok is
// false when no string does.
func (at manifestPlace) quoted() (value string, start, end int, ok bool) {
	text := at.lineText
	for i := 0; i < len(text); i++ {
		if text[i] == '#' {
			return "", 0, 0, false
		}
		if text[i] != '"' && text[i] != '\'' {
			continue
		}
		j := strings.IndexByte(text[i+1:], text[i])
		if j < 0 {
			return "", 0, 0, false
		}
		j += i + 1
		if i <= at.col && at.col <= j {
			return text[i+1 : j], i, j, true
		}
		i = j
	}
	return "", 0, 0, false
}

// quotedRange is the protocol range of a string quoted answers, from its
// opening quote at start to its closing quote at end.
func (at manifestPlace) quotedRange(start, end int) protocol.Range {
	return at.lines.utf16Range(posRange(at.line, start+1, at.line, end+2))
}
