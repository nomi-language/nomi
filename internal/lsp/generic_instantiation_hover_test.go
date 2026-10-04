package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// hoverText is the hover markdown at the ▮ marker in input.
func hoverText(t *testing.T, name, input string) string {
	t.Helper()
	input, pos := hoverMarkerPosition(t, input)
	uri := "file:///generic_hover_" + name + ".nomi"
	s := NewServer()
	s.docs.Open(uri, input)
	res, err := s.textDocumentHover(nil, &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col - 1)},
		},
	})
	if err != nil {
		t.Fatalf("hover error: %v", err)
	}
	if res == nil {
		return ""
	}
	mc, ok := res.Contents.(protocol.MarkupContent)
	if !ok {
		t.Fatalf("expected MarkupContent, got %T", res.Contents)
	}
	return mc.Value
}

// Hover shows a generic call's instantiation, never the callee's own type
// parameters: a pipeline after a head with no type (an unknown member), an
// accumulator of a seedless `Iter.reduce` inside a lambda passed to
// `Iter.map` (two callees that both name a parameter `U`), and a partial
// application whose hole a later call fills.
func TestHover_GenericCallsShowTheirInstantiation(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{
			"stage after an unknown member",
			"fn f(n: Int): List<Int> {\n  ▮evens = List.range(0, n)\n    |> Iter.filter(|x| x % 2 == 0)\n    |> Iter.to_list()\n  evens\n}\n",
			"```nomi\nevens: List<Int>\n```",
		},
		{
			"lambda parameter after an unknown member",
			"fn f(n: Int): List<Int> {\n  List.range(0, n)\n    |> Iter.filter(|▮x| x % 2 == 0)\n    |> Iter.to_list()\n}\n",
			"```nomi\nx: Int\n```",
		},
		{
			"seedless reduce inside map",
			"fn f(): List<Int> {\n  ▮sums = [[1, 2], [3]]\n    |> Iter.map(|xs| { xs |> Iter.reduce(|a, b| a + b) })\n    |> Iter.to_list()\n  sums\n}\n",
			"```nomi\nsums: List<Int>\n```",
		},
		{
			"seedless reduce accumulator",
			"fn f(): List<Int> {\n  [[1, 2], [3]]\n    |> Iter.map(|xs| { xs |> Iter.reduce(|▮a, b| a + b) })\n    |> Iter.to_list()\n}\n",
			"```nomi\na: Int\n```",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hoverText(t, c.name, c.input); got != c.want {
				t.Errorf("hover = %q, want %q", got, c.want)
			}
		})
	}
}
