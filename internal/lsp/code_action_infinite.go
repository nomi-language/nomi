package lsp

import (
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// "Limit with `Iter.take`" answers the checker's error for a terminal that
// drains an infinite iterator. The diagnostic spans the terminal's callee;
// when that callee is a pipe stage taking the piped value as its source
// (`|> Iter.to_list()`), the fix puts `Iter.take(todo) |> ` before it, so
// the stage reads `|> Iter.take(todo) |> Iter.to_list()`. The count is
// fillPlaceholder, which the language server lists as a `todo` for the
// author to fill. A nested call (`Iter.to_list(xs)`) and a terminal whose
// source is not the piped value (`Supervisor.spawn_all`) get no fix.
func buildLimitInfiniteActions(content, uri string, diags []protocol.Diagnostic) []protocol.CodeAction {
	var lines *lineIndex
	var actions []protocol.CodeAction
	for _, d := range diags {
		head := diagnosticHeadline(d.Message)
		if !strings.Contains(head, "` of an infinite iterator (`") || strings.HasPrefix(head, "`Supervisor.spawn_all`") {
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
		if start < 0 || start > len(text) || !strings.HasSuffix(strings.TrimRight(text[:start], " \t"), "|>") {
			continue
		}
		kind := protocol.CodeActionKindQuickFix
		preferred := true
		actions = append(actions, protocol.CodeAction{
			Title:       "Limit with `Iter.take`",
			Kind:        &kind,
			Diagnostics: []protocol.Diagnostic{d},
			IsPreferred: &preferred,
			Edit: &protocol.WorkspaceEdit{
				Changes: map[protocol.DocumentUri][]protocol.TextEdit{
					protocol.DocumentUri(uri): {{
						Range:   protocol.Range{Start: d.Range.Start, End: d.Range.Start},
						NewText: "Iter.take(" + fillPlaceholder + ") |> ",
					}},
				},
			},
		})
	}
	return actions
}
