package lsp

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ffirun"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func inlineGoHelperByName(name string) (ffirun.InlineGoHelper, bool) {
	for _, helper := range ffirun.InlineGoHelpers {
		if helper.Name == name {
			return helper, true
		}
	}
	return ffirun.InlineGoHelper{}, false
}

func inlineGoHelperCompletions(prefix string) []protocol.CompletionItem {
	kind := protocol.CompletionItemKindFunction
	var items []protocol.CompletionItem
	for _, helper := range ffirun.InlineGoHelpers {
		if prefix != "" &&
			!strings.HasPrefix(helper.Name, prefix) &&
			!strings.HasPrefix(strings.ToLower(helper.Name), strings.ToLower(prefix)) {
			continue
		}
		detail := helper.Signature
		items = append(items, protocol.CompletionItem{
			Label:         helper.Name,
			Kind:          &kind,
			Detail:        &detail,
			Documentation: helper.Description,
		})
	}
	return items
}

func inlineGoHelperHover(content string, nodes []ast.Node, pos analysis.Pos) (*protocol.Hover, bool) {
	if !posInInlineGo(nodes, pos) {
		return nil, false
	}
	name, startCol, endCol, ok := goIdentAt(content, pos.Line, pos.Col)
	if !ok {
		return nil, false
	}
	helper, ok := inlineGoHelperByName(name)
	if !ok {
		return nil, false
	}
	line := toZeroBased(pos.Line)
	startChar := byteToUTF16Col(content, line, toZeroBased(startCol))
	endChar := byteToUTF16Col(content, line, toZeroBased(endCol))
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind: protocol.MarkupKindMarkdown,
			Value: fmt.Sprintf("```go\n%s\n```\n\n%s",
				helper.Signature, helper.Description),
		},
		Range: &protocol.Range{
			Start: protocol.Position{Line: line, Character: startChar},
			End:   protocol.Position{Line: line, Character: endChar},
		},
	}, true
}

func posInInlineGo(nodes []ast.Node, pos analysis.Pos) bool {
	for _, n := range nodes {
		if nodeContainsInlineGo(n, pos) {
			return true
		}
	}
	return false
}

func nodeContainsInlineGo(n ast.Node, pos analysis.Pos) bool {
	switch v := n.(type) {
	case *ast.ExternFunc:
		return rawBodyContainsPos(v.GoBody, v.GoBodyLine, v.GoBodyCol, pos)
	case *ast.ExternType:
		return rawBodyContainsPos(v.GoBody, v.GoBodyLine, v.GoBodyCol, pos)
	case *ast.ImplBlock:
		return posInInlineGo(v.Items, pos)
	}
	return false
}

func rawBodyContainsPos(body string, line, col int, pos analysis.Pos) bool {
	if body == "" || line == 0 || col == 0 {
		return false
	}
	endLine, endCol := rawBodyEnd(line, col, body)
	if pos.Line < line || pos.Line > endLine {
		return false
	}
	if pos.Line == line && pos.Col < col {
		return false
	}
	if pos.Line == endLine && pos.Col > endCol {
		return false
	}
	return true
}

func rawBodyEnd(line, col int, body string) (int, int) {
	endLine := line
	endCol := col
	for i := 0; i < len(body); i++ {
		if body[i] == '\n' {
			endLine++
			endCol = 1
			continue
		}
		endCol++
	}
	return endLine, endCol
}

func goIdentAt(content string, line, col int) (string, int, int, bool) {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return "", 0, 0, false
	}
	lineText := lines[line-1]
	idx := col - 1
	if idx < 0 {
		idx = 0
	}
	if idx > len(lineText) {
		idx = len(lineText)
	}
	if idx == len(lineText) || !isGoIdentChar(lineText[idx]) {
		if idx > 0 && isGoIdentChar(lineText[idx-1]) {
			idx--
		} else {
			return "", 0, 0, false
		}
	}
	start := idx
	for start > 0 && isGoIdentChar(lineText[start-1]) {
		start--
	}
	end := idx + 1
	for end < len(lineText) && isGoIdentChar(lineText[end]) {
		end++
	}
	if start == end {
		return "", 0, 0, false
	}
	return lineText[start:end], start + 1, end + 1, true
}

func isGoIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}
