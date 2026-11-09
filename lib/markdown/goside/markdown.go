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
