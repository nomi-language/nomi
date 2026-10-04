package lsp

import (
	"os"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// utf16ToByteCol converts an LSP position — a 0-based line and a 0-based
// UTF-16 code-unit character offset, the encoding LSP clients address columns
// in — into the 1-based byte column the lexer and analyzer use. `lexer.advance`
// increments the column once per source *byte*, so any non-ASCII character
// earlier on the line (a 3-byte em-dash, an accented letter in a string or
// doc comment, …) shifts every later column. Without this conversion, hover /
// go-to-def / find-references / rename silently miss every symbol that sits
// after such a character on its line.
//
// It walks the target line advancing a UTF-16 counter and a byte counter in
// lockstep until the UTF-16 counter reaches `character`. The ASCII path is
// exact (1 byte == 1 UTF-16 unit, so the result is character+1, identical to
// the old arithmetic). Out-of-range lines fall back to character+1.
func utf16ToByteCol(content string, line, character uint32) int {
	lineText, ok := nthLine(content, line)
	if !ok {
		return int(character) + 1
	}
	return byteColOnLine(lineText, character)
}

// byteColOnLine is utf16ToByteCol within one line's text.
func byteColOnLine(lineText string, character uint32) int {
	var u16, byteOff int
	for _, r := range lineText {
		if uint32(u16) >= character {
			break
		}
		if r > 0xFFFF {
			u16 += 2 // astral-plane rune: encoded as a UTF-16 surrogate pair
		} else {
			u16++
		}
		byteOff += utf8.RuneLen(r)
	}
	return byteOff + 1
}

// byteToUTF16Col is the inverse of utf16ToByteCol: it converts a 0-based byte
// column (as the lexer/analyzer produce in positions) into the 0-based UTF-16
// code-unit column the LSP protocol expects in *outgoing* ranges. Without it,
// a response range (a hover highlight, a go-to-def target, a rename edit, a
// diagnostic squiggle) that sits after a non-ASCII character on its line is
// reported a few columns too far right. ASCII is exact (byte == UTF-16);
// out-of-range lines fall back to the byte column.
func byteToUTF16Col(content string, line, byteCol uint32) uint32 {
	lineText, ok := nthLine(content, line)
	if !ok {
		return byteCol
	}
	return utf16ColOnLine(lineText, byteCol)
}

// utf16ColOnLine is byteToUTF16Col within one line's text.
func utf16ColOnLine(lineText string, byteCol uint32) uint32 {
	var byteOff, u16 uint32
	for _, r := range lineText {
		if byteOff >= byteCol {
			break
		}
		byteOff += uint32(utf8.RuneLen(r))
		if r > 0xFFFF {
			u16 += 2 // astral-plane rune: UTF-16 surrogate pair
		} else {
			u16++
		}
	}
	return u16
}

// contentForURI returns the text of the document at uri — from the open-document
// snapshot when available, else read from disk (closed files, materialized
// stdlib). Used to convert outgoing byte-column ranges to UTF-16.
func (s *Server) contentForURI(uri protocol.DocumentUri) (string, bool) {
	if doc := s.docs.Snapshot(string(uri)); doc != nil {
		return doc.Content, true
	}
	if b, err := os.ReadFile(uriToPath(string(uri))); err == nil {
		return string(b), true
	}
	return "", false
}

// utf16RangeFromContent converts a byte-column range into UTF-16 columns using
// the given document content. Lines are untouched — only columns shift. The
// free-function core shared by s.utf16Range and callers (e.g. rename) that
// already hold the content.
func utf16RangeFromContent(content string, r protocol.Range) protocol.Range {
	return protocol.Range{
		Start: protocol.Position{Line: r.Start.Line, Character: byteToUTF16Col(content, r.Start.Line, r.Start.Character)},
		End:   protocol.Position{Line: r.End.Line, Character: byteToUTF16Col(content, r.End.Line, r.End.Character)},
	}
}

// utf16Range converts a byte-column range (as analyzer positions produce) into
// the UTF-16 columns the LSP protocol uses, fetching the target document's
// content by URI. Falls back to the byte range unchanged when content is
// unavailable (no worse than not converting).
func (s *Server) utf16Range(uri protocol.DocumentUri, r protocol.Range) protocol.Range {
	content, ok := s.contentForURI(uri)
	if !ok {
		return r
	}
	return utf16RangeFromContent(content, r)
}

// toUTF16Locations converts every location's byte-column range to UTF-16
// (each against its own URI's content, read and indexed once per URI). In
// place; returns the slice for call-site chaining at a handler's return.
func (s *Server) toUTF16Locations(locs []protocol.Location) []protocol.Location {
	indexes := map[protocol.DocumentUri]*lineIndex{}
	for i := range locs {
		li, seen := indexes[locs[i].URI]
		if !seen {
			if content, ok := s.contentForURI(locs[i].URI); ok {
				li = newLineIndex(content)
			}
			indexes[locs[i].URI] = li
		}
		if li != nil {
			locs[i].Range = li.utf16Range(locs[i].Range)
		}
	}
	return locs
}

// toUTF16Location converts a single location's range to UTF-16. nil-safe.
func (s *Server) toUTF16Location(loc *protocol.Location) *protocol.Location {
	if loc != nil {
		loc.Range = s.utf16Range(loc.URI, loc.Range)
	}
	return loc
}

// toUTF16DocumentSymbols converts the Range and SelectionRange of each symbol
// (recursively, through Children) from byte to UTF-16 columns, in place.
func toUTF16DocumentSymbols(lines *lineIndex, syms []protocol.DocumentSymbol) {
	for i := range syms {
		syms[i].Range = lines.utf16Range(syms[i].Range)
		syms[i].SelectionRange = lines.utf16Range(syms[i].SelectionRange)
		toUTF16DocumentSymbols(lines, syms[i].Children)
	}
}

// toUTF16InlayHints converts each hint's byte-column Position to UTF-16, in place.
func toUTF16InlayHints(lines *lineIndex, hints []InlayHint) {
	for i := range hints {
		hints[i].Position.Character = lines.utf16Col(hints[i].Position.Line, hints[i].Position.Character)
	}
}

// toUTF16Result converts the ranges of a definition/implementation result that
// is typed `any` (a *Location, a []Location, or nil). Pass-through for anything
// else.
func (s *Server) toUTF16Result(result any) any {
	switch v := result.(type) {
	case *protocol.Location:
		return s.toUTF16Location(v)
	case []protocol.Location:
		return s.toUTF16Locations(v)
	}
	return result
}

// nthLine returns the n-th (0-based) line of content, excluding the trailing
// newline, and whether that line exists. It scans content from the top, so
// a request converting many positions uses a lineIndex instead.
func nthLine(content string, n uint32) (string, bool) {
	nthLineScans.Add(1)
	var idx uint32
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] != '\n' {
			continue
		}
		if idx == n {
			return content[start:i], true
		}
		idx++
		start = i + 1
	}
	if idx == n {
		return content[start:], true
	}
	return "", false
}

// nthLineScans counts nthLine calls, each a scan of the text from its
// first line, for tests.
var nthLineScans atomic.Int64

// lineIndex holds the start offset of each line of one text, so a column
// conversion reads only its own line. A request converting many ranges of
// a document converts them through one, which a lineCache keeps per
// document text.
type lineIndex struct {
	content string
	starts  []int
}

func newLineIndex(content string) *lineIndex {
	return &lineIndex{content: content, starts: lineOffsets(content)}
}

// line is nthLine through the index.
func (li *lineIndex) line(n uint32) (string, bool) {
	if int(n) >= len(li.starts) {
		return "", false
	}
	end := len(li.content)
	if int(n)+1 < len(li.starts) {
		end = li.starts[n+1] - 1
	}
	return li.content[li.starts[n]:end], true
}

// byteCol is utf16ToByteCol through the index.
func (li *lineIndex) byteCol(line, character uint32) int {
	lineText, ok := li.line(line)
	if !ok {
		return int(character) + 1
	}
	return byteColOnLine(lineText, character)
}

// utf16Col is byteToUTF16Col through the index.
func (li *lineIndex) utf16Col(line, byteCol uint32) uint32 {
	lineText, ok := li.line(line)
	if !ok {
		return byteCol
	}
	return utf16ColOnLine(lineText, byteCol)
}

// utf16Range is utf16RangeFromContent through the index.
func (li *lineIndex) utf16Range(r protocol.Range) protocol.Range {
	return protocol.Range{
		Start: protocol.Position{Line: r.Start.Line, Character: li.utf16Col(r.Start.Line, r.Start.Character)},
		End:   protocol.Position{Line: r.End.Line, Character: li.utf16Col(r.End.Line, r.End.Character)},
	}
}

// lineCache keeps the lineIndex of each open document's latest text.
type lineCache struct {
	mu   sync.Mutex
	docs map[string]*lineIndex
	// builds counts the indexes built, for tests.
	builds int
}

func (c *lineCache) get(uri, content string) *lineIndex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if li, ok := c.docs[uri]; ok && li.content == content {
		return li
	}
	if c.docs == nil {
		c.docs = map[string]*lineIndex{}
	}
	li := newLineIndex(content)
	c.docs[uri] = li
	c.builds++
	return li
}

// forget drops a closed document's index.
func (c *lineCache) forget(uri string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.docs, uri)
}
