package main

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/hoverdoc"
)

// Every link of the language server's keyword hovers names a page the site
// has, a tour chapter or a generated spec page, and an anchor on it, as
// the tour's own links are held (TestTourLinksResolve).
func TestKeywordHoverLinksResolve(t *testing.T) {
	anchors, _ := siteAnchors(t)
	links := hoverdoc.KeywordDocLinks()
	if len(links) < 20 {
		t.Fatalf("the keyword hovers link %d pages; the table links more", len(links))
	}
	for _, l := range links {
		page, anchor, _ := strings.Cut(l, "#")
		ids, ok := anchors[page]
		switch {
		case !ok:
			t.Errorf("a keyword hover links %s: no such page", l)
		case anchor != "" && !ids[anchor]:
			t.Errorf("a keyword hover links %s: the page has no such anchor", l)
		}
	}
}
