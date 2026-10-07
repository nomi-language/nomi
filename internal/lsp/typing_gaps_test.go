package lsp

import (
	"encoding/json"
	"strings"
	"testing"
)

// typingGap is a known panic of the language server on text someone is
// typing: a break in the rule TestLSPSurvivesTyping and
// FuzzLSPSurvivesEdits hold the server to. Each is pinned by a minimal
// reproducer that TestKnownTypingGaps checks still panics. When a fix makes
// it stop, the test fails; delete the entry in the same change.
type typingGap struct {
	name string
	// fix says where the fix belongs.
	fix string
	// match is a substring of the panic's value or stack. The typing tests
	// skip a panic that holds it.
	match string
	// src is the document's text; cursorMark marks where method is
	// requested. With no method, opening the document (its analysis)
	// panics.
	src    string
	method string
}

var knownTypingGaps = []typingGap{}

// knownTypingGapFor is the known gap f matches, or nil.
func knownTypingGapFor(f typingFinding) *typingGap {
	for i := range knownTypingGaps {
		g := &knownTypingGaps[i]
		if strings.Contains(f.value, g.match) || strings.Contains(f.stack, g.match) {
			return g
		}
	}
	return nil
}

// TestKnownTypingGaps holds each known gap's reproducer to its panic.
func TestKnownTypingGaps(t *testing.T) {
	recordPanics(t)
	for _, gap := range knownTypingGaps {
		t.Run(gap.name, func(t *testing.T) {
			text, pos := splitCursor(t, gap.src)
			var counts typingCounts
			ss := newTypingSession(t, gap.name, map[string]string{"main.nomi": text}, "main.nomi", &counts)
			ss.setText(text)
			var raw json.RawMessage
			if gap.method != "" {
				ss.call(gap.method, ss.at(gap.method, pos), &raw)
			} else {
				// Hover waits for the analysis of the text.
				ss.call("textDocument/hover", ss.at("textDocument/hover", pos), &raw)
			}
			ss.close()
			for _, f := range ss.findings {
				if strings.Contains(f.value, gap.match) || strings.Contains(f.stack, gap.match) {
					return
				}
			}
			t.Fatalf("this reproducer no longer panics with %q; if it is fixed (%s), remove %s from knownTypingGaps:\n%s\nfindings: %v",
				gap.match, gap.fix, gap.name, gap.src, ss.findings)
		})
	}
}
