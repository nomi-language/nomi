// Package goside is the Go half of the Nomi module markdown: Markdown
// rendered to HTML with goldmark, as CommonMark with GitHub's extensions
// (tables, strikethrough, task lists and bare URLs as links). The Nomi
// facade (markdown.nomi) hands the result to std/html as trusted markup.
//
// goldmark's safe defaults are kept: raw HTML in the source is left out
// (`<!-- raw HTML omitted -->`), and a link or image whose URL has a
// dangerous scheme (`javascript:`) gets an empty one. That is what makes
// the output safe to trust when the Markdown comes from a user.
package goside

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var renderer = goldmark.New(goldmark.WithExtensions(extension.GFM))

// ToHTML is source rendered as HTML. Rendering to memory cannot fail, so it
// answers no error.
func ToHTML(source string) string {
	var out bytes.Buffer
	if err := renderer.Convert([]byte(source), &out); err != nil {
		// goldmark fails only when its writer does, and a bytes.Buffer
		// does not.
		panic("goldmark: " + err.Error())
	}
	return out.String()
}
