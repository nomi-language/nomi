package lsp

import (
	"regexp"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// "Call the pipe stage" answers the checker's error for a bare name piped
// into (`3 |> double`, `x |> io.print`): a pipe stage is a call, so the fix
// appends `()` to the name, which the error says to write. The diagnostic
// spans the stage, and the fix is offered only when the text it spans is the
// name the message quotes, so it never inserts into anything else.

var barePipeStageMessage = regexp.MustCompile("^" + regexp.QuoteMeta(analysis.BarePipeStagePrefix) + ": write `([^`]+)\\(\\)`$")

func buildBarePipeStageActions(content, uri string, diags []protocol.Diagnostic) []protocol.CodeAction {
	var lines *lineIndex
	var actions []protocol.CodeAction
	for _, d := range diags {
		m := barePipeStageMessage.FindStringSubmatch(diagnosticHeadline(d.Message))
		if m == nil || d.Range.Start.Line != d.Range.End.Line {
			continue
		}
		if lines == nil {
			lines = newLineIndex(content)
		}
		text, ok := lines.line(d.Range.Start.Line)
		if !ok {
			continue
		}
		start := lines.byteCol(d.Range.Start.Line, d.Range.Start.Character) - 1
		end := lines.byteCol(d.Range.End.Line, d.Range.End.Character) - 1
		if start < 0 || end > len(text) || start >= end || text[start:end] != m[1] {
			continue
		}
		kind := protocol.CodeActionKindQuickFix
		preferred := true
		actions = append(actions, protocol.CodeAction{
			Title:       "Call `" + m[1] + "()`",
			Kind:        &kind,
			Diagnostics: []protocol.Diagnostic{d},
			IsPreferred: &preferred,
			Edit: &protocol.WorkspaceEdit{
				Changes: map[protocol.DocumentUri][]protocol.TextEdit{
					protocol.DocumentUri(uri): {{
						Range:   protocol.Range{Start: d.Range.End, End: d.Range.End},
						NewText: "()",
					}},
				},
			},
		})
	}
	return actions
}

// "Write `then`" answers the checker's error for a lambda piped into
// (`x |> |v| v + 1`): a lambda is not a stage, and `then |v| ...` is. The
// fix inserts `then ` before the lambda, and is offered only when the
// diagnostic starts at a `|` that directly follows a `|>`, so a lambda in
// parentheses, `x |> (|v| v + 1)`, gets none.
func buildLambdaPipeStageActions(content, uri string, diags []protocol.Diagnostic) []protocol.CodeAction {
	var lines *lineIndex
	var actions []protocol.CodeAction
	for _, d := range diags {
		if !strings.HasPrefix(diagnosticHeadline(d.Message), analysis.LambdaPipeStagePrefix+":") {
			continue
		}
		if lines == nil {
			lines = newLineIndex(content)
		}
		text, ok := lines.line(d.Range.Start.Line)
		if !ok {
			continue
		}
		start := lines.byteCol(d.Range.Start.Line, d.Range.Start.Character) - 1
		if start < 0 || start >= len(text) || text[start] != '|' {
			continue
		}
		if !strings.HasSuffix(strings.TrimRight(text[:start], " \t"), "|>") {
			continue
		}
		kind := protocol.CodeActionKindQuickFix
		preferred := true
		actions = append(actions, protocol.CodeAction{
			Title:       "Write `then` before the lambda",
			Kind:        &kind,
			Diagnostics: []protocol.Diagnostic{d},
			IsPreferred: &preferred,
			Edit: &protocol.WorkspaceEdit{
				Changes: map[protocol.DocumentUri][]protocol.TextEdit{
					protocol.DocumentUri(uri): {{
						Range:   protocol.Range{Start: d.Range.Start, End: d.Range.Start},
						NewText: "then ",
					}},
				},
			},
		})
	}
	return actions
}
