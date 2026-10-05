package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const bareBraceDecls = `struct Address {
    street: String
    city: String
}

struct Person {
    name: String = "x"
    address: Address
}

struct Team {
    lead: Person
}

fn greet(a: Address): String {
    a.city
}

fn show(d: Display): String {
    Display.to_string(d)
}

`

// completeTriggered requests completion at the mark as a client does when
// the user types trigger; "" is an invoked request.
func completeTriggered(t *testing.T, src, trigger string) []protocol.CompletionItem {
	t.Helper()
	content, pos := splitCursor(t, src)
	s := NewServer()
	uri := "file:///bare_brace.nomi"
	s.docs.Open(uri, content)
	params := &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     pos,
		},
	}
	if trigger != "" {
		params.Context = &protocol.CompletionContext{TriggerKind: protocol.CompletionTriggerKindTriggerCharacter, TriggerCharacter: &trigger}
	}
	res, err := s.textDocumentCompletion(nil, params)
	if err != nil {
		t.Fatal(err)
	}
	return completionItemsOf(t, res)
}

// bareBraceModes are the ways a request reaches `{‸}`: invoked, the `{`
// trigger with the `}` an auto-pair plugin added, that `}` reported as the
// trigger (blink.cmp), and the `{` trigger with no closing brace typed.
var bareBraceModes = []struct{ name, trigger, unpaired string }{
	{"invoked", "", ""},
	{"{ trigger", "{", ""},
	{"} trigger", "}", ""},
	{"{ trigger, unpaired", "{", "unpaired"},
}

// unpaired is src as typed with no auto-pair plugin: nothing follows the
// cursor on its line.
func unpaired(src string) string {
	at := strings.Index(src, cursorMark) + len(cursorMark)
	end := strings.IndexByte(src[at:], '\n')
	return src[:at] + src[at+end:]
}

// A bare brace where the position expects a nominal struct offers that
// struct's unwritten fields, however the request comes.
func TestCompletion_BareBraceOffersTheExpectedStructsFields(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string // in order
	}{
		{"struct literal field", "fn f(): Person {\n    Person{address: {‸}}\n}\n", []string{"street", "city"}},
		{"annotated binding", "fn f(): Address {\n    a: Address = {‸}\n    a\n}\n", []string{"street", "city"}},
		{"spread patch", "fn f(p: Person): Person {\n    {..p, address: {‸}}\n}\n", []string{"street", "city"}},
		{"patch fields keep declaration order", "fn f(t: Team): Team {\n    {..t, lead: {‸}}\n}\n", []string{"name", "address"}},
		{"function argument", "fn f(): String {\n    greet({‸})\n}\n", []string{"street", "city"}},
		{"block tail", "fn f(): Address {\n    {‸}\n}\n", []string{"street", "city"}},
		{"return", "fn f(): Address {\n    return {‸}\n}\n", []string{"street", "city"}},
		{"typed list item", "fn f(): List<Person> {\n    people: List<Person> = [{‸}]\n    people\n}\n", []string{"address", "name"}},
		{"case arm", "fn f(n: Int): Address {\n    case n {\n        0 -> {‸}\n        _ -> Address{street: \"a\", city: \"b\"}\n    }\n}\n", []string{"street", "city"}},
	}
	for _, tt := range tests {
		for _, m := range bareBraceModes {
			t.Run(tt.name+"/"+m.name, func(t *testing.T) {
				src := bareBraceDecls + tt.src
				if m.unpaired != "" {
					src = unpaired(src)
				}
				items := completeTriggered(t, src, m.trigger)
				if got := strings.Join(itemLabels(items), ","); got != strings.Join(tt.want, ",") {
					t.Fatalf("fields = %s, want %s", got, strings.Join(tt.want, ","))
				}
				if te := textEditOf(t, items[0]); te.NewText != tt.want[0]+": " {
					t.Fatalf("insert = %q, want %q", te.NewText, tt.want[0]+": ")
				}
			})
		}
	}
}

// What follows a bare brace is still a field: a typed prefix filters the
// fields, and a field already written is not offered again.
func TestCompletion_BareBraceAfterAField(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"typed prefix", "fn f(): Address {\n    a: Address = {ci‸}\n    a\n}\n", []string{"city"}},
		{"nested, one field written", "fn f(): Person {\n    Person{address: {street: \"x\", ‸}}\n}\n", []string{"city"}},
		{"patch, one field written", "fn f(p: Person): Person {\n    {..p, address: {city: \"x\", ‸}}\n}\n", []string{"street"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, bareBraceDecls+tt.src)
			if got := strings.Join(itemLabels(items), ","); got != strings.Join(tt.want, ",") {
				t.Fatalf("fields = %s, want %s", got, strings.Join(tt.want, ","))
			}
		})
	}
}

// A `{` where no nominal struct is expected is a block, a map or an
// anonymous struct: it offers no fields, a trigger opens nothing, and an
// invoked request still offers the names in scope.
func TestCompletion_BareBraceWithoutAStructOffersNoFields(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"function body", "fn f(): Address {‸}\n"},
		{"if body", "fn f(c: Bool, a: Address): Address {\n    if c {‸} else {\n        a\n    }\n}\n"},
		{"Int binding", "fn f(): Int {\n    n: Int = {‸}\n    n\n}\n"},
		{"map binding", "fn f(): Int {\n    m: Map<String, Int> = {‸}\n    0\n}\n"},
		{"unannotated binding", "fn f(): Int {\n    a = {‸}\n    0\n}\n"},
		{"interface argument", "fn f(): String {\n    show({‸})\n}\n"},
		{"a later line of a block", "fn f(): Address {\n    a: Address = {\n        Address{street: \"a\", city: \"b\"}\n        st‸\n    }\n    a\n}\n"},
	}
	for _, tt := range tests {
		for _, m := range bareBraceModes {
			t.Run(tt.name+"/"+m.name, func(t *testing.T) {
				src := bareBraceDecls + tt.src
				if m.unpaired != "" {
					if !strings.Contains(src, "{‸}") {
						t.Skip("no brace at the cursor")
					}
					src = unpaired(src)
				}
				items := completeTriggered(t, src, m.trigger)
				labelsExclude(t, items, "street", "city", "name", "address")
				if m.trigger != "" && len(items) > 0 {
					t.Fatalf("a %s trigger opened %v", m.trigger, itemLabels(items))
				}
				if m.trigger == "" && m.unpaired == "" && findItem(items, "greet") == nil && !strings.HasPrefix(tt.name, "a later line") {
					t.Fatalf("an invoked request lost the names in scope: %v", itemLabels(items))
				}
			})
		}
	}
}

// Right after a field's value, with no `,` yet, the outer literal's fields
// are not offered: `{..p, address: {}‸}` once offered Person's `name`.
func TestCompletion_NoFieldRightAfterAValue(t *testing.T) {
	for _, src := range []string{
		"fn f(p: Person): Person {\n    {..p, address: {}‸}\n}\n",
		"fn f(a: Address): Person {\n    Person{address: a‸}\n}\n",
	} {
		labelsExclude(t, complete(t, bareBraceDecls+src), "name")
	}
	labelsInclude(t, complete(t, bareBraceDecls+"fn f(a: Address): Person {\n    Person{\n        address: a\n        ‸\n    }\n}\n"), "name")
}

func TestBraceMayOpenLiteral(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"Point{", true},
		{"a: Address = {", true},
		{"Person{address: {", true},
		{"greet({", true},
		{"f(a, {", true},
		{"[{", true},
		{"0 -> {", true},
		{"return {", true},
		{"x\n    {", true},
		{"fn f() {", false},
		{"fn f(): Address {", false},
		{"if c {", false},
		{"case n {", false},
		{"xs |> then |x| {", false},
		{"f(|x| {", false},
		{"{", false},
		{"noreturn {", false},
	}
	for _, tt := range tests {
		if got := braceMayOpenLiteral(tt.text, len(tt.text)-1); got != tt.want {
			t.Errorf("braceMayOpenLiteral(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}
