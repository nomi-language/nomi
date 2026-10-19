package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const sharedVariantDecls = `enum Paint {
    /// A red tin.
    Red Int
    Blue
}

enum Light {
    /// Stop.
    Red
    Amber
}

`

// A leading dot with no expected enum offers every enum's variants. A
// variant two enums share is one item, since accepting it inserts the same
// text whichever enum it is read as. Its detail names every enum that
// declares it, in name order, and its documentation gives each enum's
// declaration and doc under that enum's name. It used to show the
// declaration and doc of whichever enum the scope's map yielded first.
func TestCompletion_DotVariantSharedByTwoEnumsNamesBoth(t *testing.T) {
	src := sharedVariantDecls + "fn f() -> Int {\n    x = ." + cursorMark + "\n    1\n}\n"
	const wantDetail = "variant of Light, Paint"
	const wantDoc = "**Light**: `Light.Red`\n\nStop.\n\n**Paint**: `Paint.Red(Int)`\n\nA red tin."
	for i := 0; i < 30; i++ {
		s := NewServer()
		list := completeIn(t, s, "file:///completion_case.nomi", src)
		var reds []protocol.CompletionItem
		for _, it := range list.Items {
			if it.Label == "Red" {
				reds = append(reds, it)
			}
		}
		if len(reds) != 1 {
			t.Fatalf("run %d: want one Red item, got %d", i, len(reds))
		}
		red := reds[0]
		got := "<none>"
		if red.Detail != nil {
			got = *red.Detail
		}
		if got != wantDetail {
			t.Fatalf("run %d: Red's detail = %q, want %q", i, got, wantDetail)
		}
		resolved, err := s.completionItemResolve(nil, &red)
		if err != nil {
			t.Fatal(err)
		}
		doc, _ := resolved.Documentation.(protocol.MarkupContent)
		if doc.Value != wantDoc {
			t.Fatalf("run %d: Red's documentation = %q, want %q", i, doc.Value, wantDoc)
		}
		if amber := findItem(list.Items, "Amber"); amber == nil || amber.Detail == nil || *amber.Detail != "Light.Amber" {
			t.Fatalf("run %d: Amber, declared by one enum, should keep its declaration as its detail; got %+v", i, amber)
		}
	}
}
