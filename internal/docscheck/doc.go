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
// keywords_test.go: every list of Nomi's keywords outside Go (the spec's,
// the tree-sitter grammar's and its highlight query's, the bat and VS Code
// grammars') holds the lexer's reserved and contextual words, apart from
// the differences listed there with their reasons.
//
// precedence_test.go: the operator levels in tree-sitter-nomi/grammar.js and
// in the spec's precedence table, and the spec's notes on which operators
// do not chain, are those of internal/token's precedence table.
//
// fence_languages_test.go: every fenced block in an authored Markdown file
// and in a std doc comment has a language, one the site's block renderer
// (tour/src/lib/rehype-shiki.mjs) gives a form, read from the renderer
// itself. A file only GitHub renders may use the few more languages listed
// there with their reasons.
//
// zed_highlights_test.go: Zed's highlights.scm is what
// scripts/sync-zed-highlights.sh generates from the canonical query.
//
// diagnostic_text_test.go: no non-test Go code recognizes a diagnostic by
// its message text (a strings test, a regexp match, `==` against a
// literal); it reads the diagnostic's Code, Args or Fix. The exceptions are
// listed there, each with its reason.
//
// test_main_test.go: no tracked TestMain both defers a call and calls
// os.Exit, which runs no deferred call, so a temporary directory a TestMain
// removes is removed on every exit that is not a crash.
//
// package_docs_test.go: every citation of a package doc's section
// (`(internal/irbuild package doc, "Linkage names")`) names a directory with
// a doc.go that has that `# Heading`, and every relative link in the
// repository's guides (AGENTS.md, docs/, the editor, tour and grammar
// READMEs) names a file that exists. The implementation's design notes live
// in package docs, so these hold the pointers to them.
//
// agents_names_test.go: every test or fuzz target AGENTS.md names is
// declared, or begins a declared one's name; every repository path it
// names exists, is ignored by git (a generated page, a build's output) or
// lies under a package; and every NOMI_ variable it names is read or set
// by tracked code.
package docscheck
