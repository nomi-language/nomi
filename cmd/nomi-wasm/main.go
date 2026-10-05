//go:build js && wasm

// Command nomi-wasm exposes Nomi to the browser. Built with GOOS=js
// GOARCH=wasm, it registers these global functions:
//
//	nomiRun(source)             -> { output: string, error: string } // run a program on the VM
//	nomiRunStdlibTest(module, body, context) -> { output: string, error: string }
//	nomiFormat(source)          -> { output: string, error: string } // format source
//	nomiFormatTestBody(source)  -> { output: string, error: string }
//	nomiSemTokens(source)       -> string (JSON)                     // semantic tokens
//	nomiHover(source, line, col) -> string (markdown)               // LSP hover
//
// both running entirely client-side — the engine behind an interactive language
// tour (no server). The stdlib is //go:embed'd into the binary, so
// `import std/io` and friends work in the browser with no filesystem.
//
//	GOOS=js GOARCH=wasm go build -o nomi.wasm ./cmd/nomi-wasm
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"syscall/js"

	"github.com/nomi-language/nomi/internal/analysis"
	nomiformat "github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// stdLib backs nomiHighlight's semantic layer; loaded once at startup.
var stdLib *std.StdLib

// run executes args[0] as a Nomi program and returns a JS object
// `{ output: string, error: string }`. error is "" on success.
func run(this js.Value, args []js.Value) (res any) {
	var buf bytes.Buffer
	defer func() {
		if r := recover(); r != nil {
			res = result(buf.String(), fmt.Sprintf("%v", r))
		}
	}()

	if len(args) < 1 {
		return result("", "no source provided")
	}
	src := args[0].String()

	// Multi-file convention: `// FILE: <name>` markers split the source
	// into virtual sibling files (and an optional `// FILE: nomi.toml`
	// manifest). With no markers, this is a no-op and the input behaves
	// as a single-file program named "main".
	entrySrc, entryName, virtualFiles, manifest, splitErr := vmhost.SplitMultiFile(src)
	if splitErr != nil {
		return result("", splitErr.Error())
	}

	hasTests, err := vmhost.SourceContainsTests(entrySrc)
	if err != nil {
		return result(buf.String(), err.Error())
	}

	// The VM runs the block: one front-end pass and one IR lowering serve
	// both the program and its tests, as `nomi run` and `nomi test` do.
	p, err := vmhost.LoadSource(entryName, entrySrc,
		vmhost.WithVirtualFiles(virtualFiles),
		vmhost.WithVirtualManifest(manifest))
	if err != nil {
		return result(buf.String(), err.Error())
	}
	errText := ""
	if err := p.Run(context.Background(), &buf, nil, false); err != nil {
		errText = formatRunError(err)
	}
	if hasTests {
		report := formatCaseResults(p.Cases(&buf, vmhost.TestOptions{}))
		if strings.Contains(report, "test result: FAILED") || strings.Contains(report, "test result: BLOCKED") {
			errText = appendError(errText, report)
		} else if report != "" {
			fmt.Fprintln(&buf, report)
		}
	}

	return result(buf.String(), errText)
}

// formatCaseResults renders the VM's case results as `nomi test` does: a case the
// VM could not run prints one `BLOCKED <case> <blocker>` line per blocker (the
// diagnostic `nomi check` gives, its hints indented under it) and
// counts toward the summary's blocked number.
func formatCaseResults(cases []vmhost.CaseResult) string {
	if len(cases) == 0 {
		return ""
	}
	passed, failed, blocked := 0, 0, 0
	var report strings.Builder
	for _, c := range cases {
		name := c.Name
		if name == "" {
			name = "main"
		}
		switch {
		case c.Blocked != nil:
			blocked++
			for _, blocker := range c.Blocked {
				first, more, _ := strings.Cut(blocker, "\n")
				fmt.Fprintf(&report, "BLOCKED %s %s\n", name, first)
				if more != "" {
					report.WriteString(indent(more, "  "))
					report.WriteByte('\n')
				}
			}
		case c.Err != nil:
			failed++
			fmt.Fprintf(&report, "FAIL %s\n", name)
			report.WriteString(indent(formatRunError(c.Err), "  "))
			report.WriteByte('\n')
		default:
			passed++
			fmt.Fprintf(&report, "ok %s\n", name)
		}
	}
	verdict := "ok"
	switch {
	case failed > 0:
		verdict = "FAILED"
	case blocked > 0:
		verdict = "BLOCKED"
	}
	fmt.Fprintf(&report, "test result: %s. %d passed, %d failed", verdict, passed, failed)
	if blocked > 0 {
		fmt.Fprintf(&report, ", %d blocked", blocked)
	}
	return report.String()
}

// formatSrc formats args[0] and returns `{ output, error }`.
func formatSrc(this js.Value, args []js.Value) (res any) {
	defer func() {
		if r := recover(); r != nil {
			res = result("", fmt.Sprintf("%v", r))
		}
	}()
	if len(args) < 1 {
		return result("", "no source provided")
	}
	out, err := nomiformat.Format(args[0].String())
	if err != nil {
		return result("", err.Error())
	}
	return result(out, "")
}

// formatTestBody formats args[0] as the body of an ordinary test block, then
// returns only the formatted body text for the reference-page editor.
func formatTestBody(this js.Value, args []js.Value) (res any) {
	defer func() {
		if r := recover(); r != nil {
			res = result("", fmt.Sprintf("%v", r))
		}
	}()
	if len(args) < 1 {
		return result("", "no source provided")
	}
	wrapped, _ := vmhost.ReferenceTestSource("", args[0].String(), "")
	out, err := nomiformat.Format(wrapped)
	if err != nil {
		return result("", err.Error())
	}
	return result(extractReferenceTestBody(out), "")
}

// runStdlibTestBody executes an editable stdlib reference snippet on the VM,
// as the body of one `test "reference"` appended to the embedded stdlib module
// that produced it (vmhost.StdlibReference): the case is built in the module's
// own scope against the cached stdlib lowering, as `nomi test <std file>`
// builds the module's `//!` prompts.
func runStdlibTestBody(this js.Value, args []js.Value) (res any) {
	var buf bytes.Buffer
	defer func() {
		if r := recover(); r != nil {
			res = result(buf.String(), fmt.Sprintf("%v", r))
		}
	}()
	if len(args) < 2 {
		return result("", "module and test body required")
	}
	context := ""
	if len(args) >= 3 {
		context = args[2].String()
	}
	cases, err := vmhost.StdlibReference(args[0].String(), args[1].String(), context, &buf)
	if err != nil {
		return result(buf.String(), err.Error())
	}
	report := formatCaseResults(cases)
	if strings.Contains(report, "test result: FAILED") || strings.Contains(report, "test result: BLOCKED") {
		return result(buf.String(), report)
	}
	if report != "" {
		fmt.Fprintln(&buf, report)
	}
	return result(buf.String(), "")
}

func formatRunError(err error) string { return vmhost.FormatFailure(err) }

func appendError(existing, next string) string {
	if existing == "" {
		return next
	}
	if next == "" {
		return existing
	}
	return existing + "\n" + next
}

func extractReferenceTestBody(src string) string {
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	if len(lines) < 2 {
		return ""
	}
	body := lines[1 : len(lines)-1]
	for i, line := range body {
		body[i] = strings.TrimPrefix(line, "  ")
	}
	return strings.Join(body, "\n")
}

func indent(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// semToken is the JSON shape the browser highlighter consumes for the semantic
// overlay layer.
type semToken struct {
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Len  int    `json:"len"`
	Type string `json:"type"`
}

// semTokensSrc returns the analyzer's semantic tokens for args[0] as a JSON
// array, for the browser to overlay on tree-sitter's syntactic highlighting.
func semTokensSrc(this js.Value, args []js.Value) (res any) {
	defer func() {
		if recover() != nil {
			res = "[]"
		}
	}()
	if len(args) < 1 {
		return "[]"
	}
	toks := highlight.SemanticTokens(args[0].String(), stdLib)
	out := make([]semToken, 0, len(toks))
	for _, t := range toks {
		out = append(out, semToken{Line: t.Line, Col: t.Col, Len: t.Length, Type: t.Type})
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// hoverSrc returns the hover markdown for the symbol at (line, col) — both
// 1-based — in args[0], or "" if there's nothing to show. It runs the same
// renderer the LSP runs (internal/hoverdoc) over the same analysis the LSP
// builds for a single pathless buffer (highlight.Analyze, which builds the
// project). It does not call lsp.HoverAt — the LSP framework is deliberately
// out of this binary — and the agreement with Zed holds only for a snippet:
// there are no sibling files here and no filesystem to read them from.
func hoverSrc(this js.Value, args []js.Value) (res any) {
	defer func() {
		if recover() != nil {
			res = ""
		}
	}()
	if len(args) < 3 {
		return ""
	}
	fa := highlight.Analyze(args[0].String(), stdLib)
	if fa == nil {
		return ""
	}
	return hoverdoc.At(fa, analysis.Pos{Line: args[1].Int(), Col: args[2].Int()})
}

func result(output, errStr string) any {
	return map[string]any{"output": output, "error": errStr}
}

func main() {
	// The analysis vmhost's front end and lowering read too, so the page
	// analyzes the stdlib once.
	stdLib = std.Shared()
	js.Global().Set("nomiRun", js.FuncOf(run))
	js.Global().Set("nomiRunStdlibTest", js.FuncOf(runStdlibTestBody))
	js.Global().Set("nomiFormat", js.FuncOf(formatSrc))
	js.Global().Set("nomiFormatTestBody", js.FuncOf(formatTestBody))
	js.Global().Set("nomiSemTokens", js.FuncOf(semTokensSrc))
	js.Global().Set("nomiHover", js.FuncOf(hoverSrc))
	// The first VM run would otherwise lower the whole standard library inside
	// its own timed budget. Doing it here makes it part of the worker's cold
	// load, which the tour client budgets separately (LOAD_BUDGET_MS).
	vmhost.Warm()
	// Keep the Go runtime alive so the exported functions stay callable.
	select {}
}
