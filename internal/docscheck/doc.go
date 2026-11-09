// Package docscheck holds repository-wide checks over tracked text files.
//
// conflict_markers_test.go: no tracked Markdown or Go file may carry a git
// merge-conflict marker. Markdown is never compiled, so a marker inside it
// breaks nothing else a test runs.
//
// go_versions_test.go: every tracked go.mod declares the same `go` line as the
// root go.mod.
//
// spec_citations_test.go: every spec citation in docs/*.md and in Go and Nomi
// comments (`spec §7`, `§7 *Calls*`) names a chapter of docs/spec.md, and the
// heading or bold lead-in it gives is in that chapter.
//
// package_docs_test.go: every citation of a package doc's section
// (`(internal/irbuild package doc, "Linkage names")`) names a directory with
// a doc.go that has that `# Heading`, and every relative link in the
// repository's guides (AGENTS.md, docs/, the editor, tour and grammar
// READMEs) names a file that exists. The implementation's design notes live
// in package docs, so these hold the pointers to them.
package docscheck
