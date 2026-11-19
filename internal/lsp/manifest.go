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
// lowering read.
func (s *Server) manifestDiagnostics(problems, err error) map[string][]protocol.Diagnostic {
	var ds frontend.Diagnostics
	if err != nil || !errors.As(problems, &ds) {
		return nil
	}
	var out map[string][]protocol.Diagnostic
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
	}
	return out
}

// manifestCompletion is what completes at pos in text, the nomi.toml at
// uri: a std module's name inside a list of std names, and a dependency's
// module path inside [capabilities.deps]' `go` or `go_in_tests` or as a
// key under [capabilities.deps.modules].
// A name is quoted unless the cursor is inside a string.
func manifestCompletion(uri, text string, pos protocol.Position) []protocol.CompletionItem {
	data := []byte(text)
	line := int(pos.Line) + 1
	lines := newLineIndex(text)
	lineText, _ := lines.line(pos.Line)
	col := min(max(lines.byteCol(pos.Line, pos.Character)-1, 0), len(lineText))
	table := analysis.ManifestTableAt(data, line)
	// key is the key whose value holds the cursor, "" for none.
	key := ""
	for _, run := range analysis.ManifestTableLayout(data, table...).Body {
		if len(run.Key) == 1 && run.Line <= line && line <= run.Last && (line > run.Line || col > run.ValueStart) {
			key = run.Key[0]
		}
	}
	root := filepath.Dir(uriToPath(uri))
	var names []string
	kind := protocol.CompletionItemKindModule
	switch strings.Join(table, ".") {
	case "capabilities.self":
		if key == "std" || key == "std_in_tests" {
			names = capability.StdNames()
		}
	case "capabilities.deps":
		switch key {
		case "std", "std_in_tests":
			names = capability.StdNames()
		case "go", "go_in_tests":
			names, kind = vmhost.DependencyModules(root), protocol.CompletionItemKindFolder
		}
	case "capabilities.deps.modules":
		switch {
		case key != "":
			names = capability.StdNames()
		case !strings.Contains(lineText[:col], "="):
			names, kind = vmhost.DependencyModules(root), protocol.CompletionItemKindFolder
		}
	}
	if names == nil {
		return nil
	}
	start := col
	for start > 0 && !strings.ContainsRune("\"[, \t", rune(lineText[start-1])) {
		start--
	}
	quoted := start > 0 && lineText[start-1] == '"'
	replace := lines.utf16Range(posRange(line, start+1, line, col+1))
	items := make([]protocol.CompletionItem, len(names))
	for i, name := range names {
		k := kind
		insert := name
		if !quoted {
			insert = fmt.Sprintf("%q", name)
		}
		items[i] = protocol.CompletionItem{Label: name, Kind: &k, TextEdit: protocol.TextEdit{Range: replace, NewText: insert}}
	}
	return items
}
