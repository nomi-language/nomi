package lsp

import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// Diagnostics in files that are not Nomi: a `message`'s catalogs, and any
// input a keyword declaration's expansion reads (analysis/expansion.go).
//
// The analysis reports each with the declaring file's diagnostics, as a
// TypeError whose File is the input's path. Publishing a document's
// diagnostics publishes those on the input's URI too, open or not, one
// path for every kind of input: what each source document says about an
// input is kept (inputDiagnostics.bySource), and the input's diagnostics are
// all of them together, each once. An input whose sources stop reporting
// anything about it is published empty, which clears it.

// isInputURI reports whether uri may be a catalog: a TOML file. An editor
// sends the server every TOML buffer when it attaches by language rather
// than by path (Zed), so what the server does with one waits for
// readsInput.
func isInputURI(uri string) bool {
	return filepath.Ext(uriToPath(uri)) == ".toml"
}

// followsInput reports whether the server follows uri as an input rather
// than as a document: a TOML file (isInputURI).
func (s *Server) followsInput(uri string) bool {
	return isInputURI(uri)
}

// readsInput reports whether an analysis reads the file at uri as an input:
// a message catalog, under a module's `[i18n].catalogs` directory in a
// listed locale's, or one an analysis read. Editing any
// other TOML file, a Cargo.toml in the same workspace, rechecks nothing.
func (s *Server) readsInput(uri string) bool {
	if _, ok := analysis.CatalogAt(uriToPath(uri)); ok {
		return true
	}
	return s.docs.IsInput(uriToPath(uri))
}

// inputDiagnostics holds what each document's analysis said about each
// input.
type inputDiagnostics struct {
	mu sync.Mutex
	// bySource maps an input's URI to the diagnostics each source
	// document's analysis reports in it.
	bySource map[string]map[string][]protocol.Diagnostic
}

// update records what source's analysis reports in inputs (by input URI),
// and answers the inputs whose diagnostics changed, each with its
// diagnostics now.
func (d *inputDiagnostics) update(source string, inputs map[string][]protocol.Diagnostic) map[string][]protocol.Diagnostic {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.bySource == nil {
		d.bySource = map[string]map[string][]protocol.Diagnostic{}
	}
	touched := map[string]bool{}
	for input, sources := range d.bySource {
		if _, had := sources[source]; had {
			if _, still := inputs[input]; !still {
				delete(sources, source)
				touched[input] = true
			}
		}
	}
	for input, diags := range inputs {
		if d.bySource[input] == nil {
			d.bySource[input] = map[string][]protocol.Diagnostic{}
		}
		d.bySource[input][source] = diags
		touched[input] = true
	}
	out := map[string][]protocol.Diagnostic{}
	for input := range touched {
		out[input] = d.merged(input)
	}
	return out
}

// merged is every source's diagnostics in input, each once, in order.
func (d *inputDiagnostics) merged(input string) []protocol.Diagnostic {
	sources := make([]string, 0, len(d.bySource[input]))
	for s := range d.bySource[input] {
		sources = append(sources, s)
	}
	sort.Strings(sources)
	seen := map[string]bool{}
	out := []protocol.Diagnostic{}
	for _, s := range sources {
		for _, diag := range d.bySource[input][s] {
			key := diag.Message + "\x00" + rangeKey(diag.Range)
			if !seen[key] {
				seen[key] = true
				out = append(out, diag)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Range.Start, out[j].Range.Start
		return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
	})
	return out
}

func rangeKey(r protocol.Range) string {
	return fmt.Sprintf("%d,%d,%d,%d", r.Start.Line, r.Start.Character, r.End.Line, r.End.Character)
}

// inputDiagnosticsOf groups the errors among errs that are in an input
// file, by the input's URI, as diagnostics placed in its text.
func (s *Server) inputDiagnosticsOf(errs []analysis.TypeError) map[string][]protocol.Diagnostic {
	byFile := map[string][]analysis.TypeError{}
	for _, e := range errs {
		if e.File == "" || filepath.Ext(e.File) == ".nomi" {
			continue
		}
		byFile[e.File] = append(byFile[e.File], e)
	}
	out := map[string][]protocol.Diagnostic{}
	for path, errs := range byFile {
		uri := fileuri.FromPath(path)
		content, _ := s.contentForURI(protocol.DocumentUri(uri))
		diags := buildDiagnostics(uri, nil, errs)
		if content != "" {
			lines := newLineIndex(content)
			for i := range diags {
				diags[i].Range = lines.utf16Range(diags[i].Range)
			}
		}
		if !s.relatedInformation {
			foldRelated(diags)
		}
		out[uri] = diags
	}
	return out
}

// publishInputs publishes what source's analysis errs say about inputs.
func (s *Server) publishInputs(notify glsp.NotifyFunc, source string, errs []analysis.TypeError) {
	for uri, diags := range s.inputDiags.update(source, s.inputDiagnosticsOf(errs)) {
		notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
			URI:         protocol.DocumentUri(uri),
			Diagnostics: diags,
		})
	}
}

// inputChanged rechecks what reads the input at uri after it changed: every
// open document, through the programs that read it again, and the closed
// files of its project.
func (s *Server) inputChanged(uri string) {
	if s.notify == nil || !s.readsInput(uri) {
		return
	}
	go func() {
		defer recoverPanic("rechecking after "+uri+" changed", nil)
		open, closed := s.docs.InputsChanged(uriToPath(uri))
		s.queueClosedDiagnostics(closed...)
		for _, doc := range open {
			s.publish(s.notify, doc.URI)
		}
	}()
}

// openInputs are the inputs an editor has open, by URI.
type openInputs struct {
	mu   sync.Mutex
	uris map[string]bool
}

// setInput makes text an open input's content and rechecks what reads it.
func (s *Server) setInput(uri, text string) {
	if !s.readsInput(uri) {
		return
	}
	s.openInputs.mu.Lock()
	if s.openInputs.uris == nil {
		s.openInputs.uris = map[string]bool{}
	}
	s.openInputs.uris[uri] = true
	s.openInputs.mu.Unlock()
	s.docs.SetInputOverlay(uriToPath(uri), text)
	s.inputChanged(uri)
}

// closeInput returns a closed input to the disk's text.
func (s *Server) closeInput(uri string) {
	s.openInputs.mu.Lock()
	delete(s.openInputs.uris, uri)
	s.openInputs.mu.Unlock()
	s.docs.ClearInputOverlay(uriToPath(uri))
	s.inputChanged(uri)
}

// inputOpen reports whether an editor has the input at uri open.
func (s *Server) inputOpen(uri string) bool {
	s.openInputs.mu.Lock()
	defer s.openInputs.mu.Unlock()
	return s.openInputs.uris[uri]
}

// expansionLocations are the input spans an expansion was made of, as
// locations: a message's entry in each catalog.
func expansionLocations(info *analysis.ExpansionInfo) []protocol.Location {
	var out []protocol.Location
	for _, in := range info.Inputs {
		out = append(out, protocol.Location{
			URI:   protocol.DocumentUri(fileuri.FromPath(in.Path)),
			Range: errorRange(in.Line, in.Col, in.EndLine, in.EndCol),
		})
	}
	return out
}
