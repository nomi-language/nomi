package lsp

import (
	"sort"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// textDocumentDocumentHighlight answers the occurrences in the current
// file of the symbol under the cursor, matched as references are: a
// definition (a binding, parameter, pattern name or declaration) is a
// Write, a use a Read. A punned field label (`{name}`) that reads a
// variable is a Read of it.
//
// Editors ask on every cursor rest, so it reads the document's occurrence
// index, built once per analysis, and touches only the occurrences of the
// line under the cursor and of the symbol found there.
func (s *Server) textDocumentDocumentHighlight(_ *glsp.Context, params *protocol.DocumentHighlightParams) ([]protocol.DocumentHighlight, error) {
	uri := string(params.TextDocument.URI)
	doc := s.docs.Snapshot(uri)
	if doc == nil || doc.Analysis == nil {
		return nil, nil
	}
	lines := s.lines.get(uri, doc.Content)
	pos := analysis.Pos{
		Line: int(params.Position.Line) + 1,
		Col:  lines.byteCol(params.Position.Line, params.Position.Character),
	}
	idx := s.occurrences.get(uri, doc.Analysis)
	sym := idx.symbolAt(pos)
	if sym == nil {
		return nil, nil
	}
	hs := documentHighlights(idx, sym, uriToPath(uri))
	for i := range hs {
		hs[i].Range = lines.utf16Range(hs[i].Range)
	}
	return hs, nil
}

// documentHighlights returns the byte-column highlights of sym in the
// indexed file, sorted by position.
func documentHighlights(idx *occurrenceIndex, sym *analysis.Symbol, path string) []protocol.DocumentHighlight {
	targets := symbolTargets(sym, path)
	read, write := protocol.DocumentHighlightKindRead, protocol.DocumentHighlightKindWrite
	cands := idx.candidates(targets)
	seen := make(map[protocol.Range]bool, len(cands))
	var out []protocol.DocumentHighlight
	// candidates lists definitions first, so a position that is both (an
	// import's last segment) counts as the definition.
	for _, o := range cands {
		if !matchesAnyTarget(o.sym, targets) || analysis.IsSynthesizedLine(o.at.Line) {
			continue
		}
		r := makeLocation("", o.at, o.sym.Name).Range
		if seen[r] {
			continue
		}
		seen[r] = true
		kind := &read
		if o.kind == occurrenceDefinition {
			kind = &write
		}
		out = append(out, protocol.DocumentHighlight{Range: r, Kind: kind})
	}
	sort.Slice(out, func(i, j int) bool { return posBefore(out[i].Range.Start, out[j].Range.Start) })
	return out
}

// occurrenceKind is the FileAnalysis map an occurrence comes from, in the
// order highlights prefer them.
type occurrenceKind uint8

const (
	occurrenceDefinition occurrenceKind = iota
	occurrenceReference
	occurrencePunnedLabel
)

// occurrence is one entry of a FileAnalysis's Definitions, References or
// PunnedFieldLabels.
type occurrence struct {
	at   analysis.Pos
	sym  *analysis.Symbol
	kind occurrenceKind
}

// identityKey is the name and position of the declaration a symbol
// resolves to: the part of symbolIdentity that matchesIdentity requires
// to be equal.
type identityKey struct {
	name string
	pos  analysis.Pos
}

func identityKeyOf(sym *analysis.Symbol) identityKey {
	id := symbolIdentity(sym)
	return identityKey{id.Name, id.Pos}
}

// occurrenceIndex groups one FileAnalysis's occurrences by line and by the
// declaration they name. Every symbol matchesSymbol accepts for a target
// is the target itself, resolves to it, or has its identity, so a target's
// candidates are the occurrences filed under the target's pointer and its
// identity key; matchesAnyTarget then decides each exactly as a walk of
// the whole maps would.
type occurrenceIndex struct {
	fa *analysis.FileAnalysis
	// byLine holds each line's references, then its definitions, the order
	// FileAnalysis.TokenAt checks them in.
	byLine     map[int][]occurrence
	byPointer  map[*analysis.Symbol][]occurrence
	byIdentity map[identityKey][]occurrence
}

func newOccurrenceIndex(fa *analysis.FileAnalysis) *occurrenceIndex {
	idx := &occurrenceIndex{
		fa:         fa,
		byLine:     map[int][]occurrence{},
		byPointer:  map[*analysis.Symbol][]occurrence{},
		byIdentity: map[identityKey][]occurrence{},
	}
	file := func(m map[analysis.Pos]*analysis.Symbol, kind occurrenceKind) {
		for at, sym := range m {
			o := occurrence{at: at, sym: sym, kind: kind}
			idx.byPointer[sym] = append(idx.byPointer[sym], o)
			if sym.Resolved != nil && sym.Resolved != sym {
				idx.byPointer[sym.Resolved] = append(idx.byPointer[sym.Resolved], o)
			}
			k := identityKeyOf(sym)
			idx.byIdentity[k] = append(idx.byIdentity[k], o)
		}
	}
	file(fa.Definitions, occurrenceDefinition)
	file(fa.References, occurrenceReference)
	file(fa.PunnedFieldLabels, occurrencePunnedLabel)
	for at, sym := range fa.References {
		idx.byLine[at.Line] = append(idx.byLine[at.Line], occurrence{at: at, sym: sym, kind: occurrenceReference})
	}
	for at, sym := range fa.Definitions {
		idx.byLine[at.Line] = append(idx.byLine[at.Line], occurrence{at: at, sym: sym, kind: occurrenceDefinition})
	}
	return idx
}

// symbolAt is FileAnalysis.SymbolAt over the cursor's line only.
func (idx *occurrenceIndex) symbolAt(pos analysis.Pos) *analysis.Symbol {
	for _, o := range idx.byLine[pos.Line] {
		width := o.sym.Span
		if width <= 0 {
			width = len(o.sym.Name)
		}
		if pos.Col >= o.at.Col && pos.Col < o.at.Col+width {
			return o.sym
		}
	}
	return nil
}

// candidates returns the occurrences that can match targets, definitions
// before references before punned labels. A target's pointer bucket adds
// only what its identity bucket lacks; an occurrence two targets share
// comes twice, with the same range and kind, which documentHighlights
// drops.
func (idx *occurrenceIndex) candidates(targets []symbolTarget) []occurrence {
	var out []occurrence
	for _, t := range targets {
		k := identityKey{t.id.Name, t.id.Pos}
		out = append(out, idx.byIdentity[k]...)
		for _, o := range idx.byPointer[t.sym] {
			if identityKeyOf(o.sym) != k {
				out = append(out, o)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].kind < out[j].kind })
	return out
}

// occurrenceCache keeps the occurrence index of each open document's
// latest analysis.
type occurrenceCache struct {
	mu   sync.Mutex
	docs map[string]*occurrenceIndex
	// builds counts the indexes built, for tests.
	builds int
}

func (c *occurrenceCache) get(uri string, fa *analysis.FileAnalysis) *occurrenceIndex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if idx, ok := c.docs[uri]; ok && idx.fa == fa {
		return idx
	}
	if c.docs == nil {
		c.docs = map[string]*occurrenceIndex{}
	}
	idx := newOccurrenceIndex(fa)
	c.docs[uri] = idx
	c.builds++
	return idx
}

// forget drops a closed document's index.
func (c *occurrenceCache) forget(uri string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.docs, uri)
}
