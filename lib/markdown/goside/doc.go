// Package goside is the Go half of the Nomi module markdown: Markdown
// rendered to HTML with goldmark, as CommonMark with GitHub's extensions
// (tables, strikethrough, task lists and bare URLs as links). The Nomi
// facade (markdown.nomi) hands the result to std/html as trusted markup.
//
// goldmark's safe defaults are kept: raw HTML in the source is left out
// (`<!-- raw HTML omitted -->`), and a link or image whose URL has a
// dangerous scheme (`javascript:`) gets an empty one. That is what makes
// the output safe to trust when the Markdown comes from a user, and what
// lets `markdown.render` return its output through `Html.trusted`.
//
// The Go side is one function, ToHTML, bound with `go`. Rendering to
// memory cannot fail, so it answers no error.
package goside
