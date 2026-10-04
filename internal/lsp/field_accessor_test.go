package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const accessorDecls = `struct Address {
  city: String
}

struct User {
  name: String
  age: Int
  address: Address
}

struct Box<T> {
  value: T
}

`

// Hover on a field accessor's segment shows the field at the type it is read
// at, and go-to-definition lands on the field's declaration.
func TestFieldAccessor_HoverAndDefinition(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantHover string
		wantDecl  string
	}{
		{
			name:      "one field",
			body:      "fn f(users: List<User>): List<String> {\n  users |> Iter.map(.▮name) |> Iter.to_list()\n}\n",
			wantHover: "```nomi\nname: String\n```",
			wantDecl:  "name: String",
		},
		{
			name:      "the second segment of a chain",
			body:      "fn f(users: List<User>): List<String> {\n  users |> Iter.map(.address.▮city) |> Iter.to_list()\n}\n",
			wantHover: "```nomi\ncity: String\n```",
			wantDecl:  "city: String",
		},
		{
			name:      "the first segment of a chain",
			body:      "fn f(users: List<User>): List<String> {\n  users |> Iter.map(.▮address.city) |> Iter.to_list()\n}\n",
			wantHover: "```nomi\naddress: Address\n```",
			wantDecl:  "address: Address",
		},
		{
			name:      "a generic struct's field, instantiated",
			body:      "fn f(boxes: List<Box<Int>>): List<Int> {\n  boxes |> Iter.map(.▮value) |> Iter.to_list()\n}\n",
			wantHover: "```nomi\nvalue: Int\n```",
			wantDecl:  "value: T",
		},
		{
			name:      "an annotated binding",
			body:      "fn f(u: User): Int {\n  get: (User) -> Int = .▮age\n  get(u)\n}\n",
			wantHover: "```nomi\nage: Int\n```",
			wantDecl:  "age: Int",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, pos := definitionMarkerPosition(t, accessorDecls+tc.body)
			uri := "file:///field_accessor.nomi"
			s := NewServer()
			s.docs.Open(uri, src)
			if doc := s.docs.Get(uri); doc == nil || len(doc.Analysis.TypeErrors) != 0 {
				t.Fatalf("the source does not check: %v", doc.Analysis.TypeErrors)
			}

			if got := appFieldHover(t, s, uri, pos); got != tc.wantHover {
				t.Errorf("hover:\ngot:  %q\nwant: %q", got, tc.wantHover)
			}
			loc := mustDefinitionLocation(t, s, uri, pos)
			if loc.URI != protocol.DocumentUri(uri) {
				t.Fatalf("definition landed in %q, want %q", loc.URI, uri)
			}
			lines := strings.Split(src, "\n")
			at := int(loc.Range.Start.Line)
			if at < 0 || at >= len(lines) {
				t.Fatalf("definition points at line %d, outside the document", at)
			}
			if got := strings.TrimSpace(lines[at]); got != tc.wantDecl {
				t.Fatalf("definition points at %q, want %q", got, tc.wantDecl)
			}
		})
	}
}

// After a leading dot where a function over a struct is expected, the
// struct's fields are offered; where an enum is expected, its variants are.
func TestCompletion_FieldAccessorOffersTheStructFields(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"generic argument pinned by an earlier one", "fn f(users: List<User>): Int {\n    Iter.map(users, ." + cursorMark + ")\n    1\n}\n", []string{"name", "age", "address"}},
		{"generic argument, typed and recorded", "fn f(users: List<User>): List<String> {\n    Iter.map(users, .na" + cursorMark + ") |> Iter.to_list()\n}\n", []string{"name"}},
		{"pipe stage argument", "fn f(users: List<User>): Int {\n    users |> Iter.map(." + cursorMark + ")\n    1\n}\n", []string{"name", "age", "address"}},
		{"trailing argument past a default", "fn f(users: List<User>): Int {\n    Iter.sort_by(users, ." + cursorMark + ")\n    1\n}\n", []string{"name", "age", "address"}},
		{"annotated binding", "fn f(): Int {\n    get: (User) -> Int = ." + cursorMark + "\n    1\n}\n", []string{"name", "age", "address"}},
		{"chain", "fn f(users: List<User>): Int {\n    users |> Iter.map(.address." + cursorMark + ")\n    1\n}\n", []string{"city"}},
		{"generic struct", "fn f(boxes: List<Box<Address>>): Int {\n    boxes |> Iter.map(.value." + cursorMark + ")\n    1\n}\n", []string{"city"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, accessorDecls+tt.src)
			labelsInclude(t, items, tt.want...)
			labelsExclude(t, items, "Some", "None", "Ok", "True")
		})
	}
}

func TestCompletion_DotAtAnEnumPositionStillOffersVariants(t *testing.T) {
	items := complete(t, colorDecls+"fn f(): Int {\n    paint(.r"+cursorMark+")\n}\n")
	labelsInclude(t, items, "Red")
}
