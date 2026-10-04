package lsp

import (
	"slices"
	"testing"
)

const deriveDecls = `struct Point {
    x: Int
}

enum Color {
    Red
    Green
}

type Meters Int

derive Equatable for Point

impl Display for Color {
    fn to_string(c: Color): String {
        "color"
    }
}
`

func TestCompletion_Derive(t *testing.T) {
	all := []string{"Equatable", "Hashable", "Comparable", "Display", "ToJson", "FromJson", "Debug"}
	tests := []struct {
		name string
		src  string
		want []string // exactly, in order
	}{
		{"interface, nothing typed", deriveDecls + "\nderive ‸\n", all},
		{"interface, typed", deriveDecls + "\nderive Ha‸\n", []string{"Hashable"}},
		{"interface before an existing for", deriveDecls + "\nderive ‸ for Point\n", []string{"Hashable", "Comparable", "Display", "ToJson", "FromJson", "Debug"}},
		{"interface for an enum", deriveDecls + "\nderive ‸ for Color\n", []string{"Equatable", "Hashable", "Comparable", "ToJson", "Debug"}},
		{"type", deriveDecls + "\nderive Hashable for ‸\n", []string{"Point", "Color", "Meters"}},
		{"type, typed", deriveDecls + "\nderive Hashable for Po‸\n", []string{"Point"}},
		{"type without one already deriving it", deriveDecls + "\nderive Equatable for ‸\n", []string{"Color", "Meters"}},
		{"type without one implementing it", deriveDecls + "\nderive Display for ‸\n", []string{"Point", "Meters"}},
		{"type FromJson takes", deriveDecls + "\nderive FromJson for ‸\n", []string{"Point", "Meters"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := itemLabels(complete(t, tt.src))
			if !slices.Equal(got, tt.want) {
				t.Fatalf("labels = %v, want %v", got, tt.want)
			}
		})
	}
}

// An interface the file does not have in scope adds its import; one the
// prelude binds adds none.
func TestCompletion_DeriveImportsTheInterface(t *testing.T) {
	items := completeWith(t, true, deriveDecls+"\nderive ‸\n")
	wantEdit(t, onlyEdit(t, mustItem(t, items, "ToJson")), 0, 0, 0, 0, "import std/json.ToJson\n\n")
	if it := mustItem(t, items, "Hashable"); len(it.AdditionalTextEdits) != 0 {
		t.Fatalf("a prelude interface carries an import: %+v", it.AdditionalTextEdits)
	}

	items = completeWith(t, true, "import std/json.ToJson\n\n"+deriveDecls+"\nderive ToJson for Point\n\nderive ‸\n")
	if it := mustItem(t, items, "ToJson"); len(it.AdditionalTextEdits) != 0 {
		t.Fatalf("an imported interface is imported again: %+v", it.AdditionalTextEdits)
	}
}
