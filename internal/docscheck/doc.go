// Package docscheck holds repository-wide checks over tracked text files.
//
// conflict_markers_test.go: no tracked Markdown or Go file may carry a git
// merge-conflict marker. Markdown is never compiled, so a marker inside it
// breaks nothing else a test runs.
//
// go_versions_test.go: every tracked go.mod declares the same `go` line as the
// root go.mod.
package docscheck
