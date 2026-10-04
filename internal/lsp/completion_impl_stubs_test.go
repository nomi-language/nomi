package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// insertOf is the text an accepted item writes.
func insertOf(t *testing.T, items []protocol.CompletionItem, label string) string {
	t.Helper()
	return textEditOf(t, mustItem(t, items, label)).NewText
}

func TestCompletion_ImplStubDisplayOnAStruct(t *testing.T) {
	src := "struct Item {\n    name: String\n}\n\nimpl Display for Item {\n    " + cursorMark + "\n}\n"
	items := completeWith(t, true, src)
	if got := strings.Join(itemLabels(items), ","); got != "to_string" {
		t.Fatalf("items = %s, want to_string alone", got)
	}
	want := "fn to_string(item: Item): String {\n    $0\n}"
	if got := insertOf(t, items, "to_string"); got != want {
		t.Fatalf("snippet =\n%s\nwant\n%s", got, want)
	}
	it := mustItem(t, items, "to_string")
	if it.InsertTextFormat == nil || *it.InsertTextFormat != protocol.InsertTextFormatSnippet {
		t.Fatal("the stub is not a snippet")
	}
	// Its later lines are relative to the cursor's line, which the client
	// indents them by (Neovim's snippet expansion does so whatever the
	// mode says).
	if it.InsertTextMode == nil || *it.InsertTextMode != protocol.InsertTextModeAdjustIndentation {
		t.Fatal("a multi-line stub snippet must ask the client to adjust its indentation")
	}
	if it.Detail == nil || *it.Detail != "fn to_string(item: Item): String" {
		t.Fatalf("detail = %v", it.Detail)
	}
}

func TestCompletion_ImplStubAtColumnOne(t *testing.T) {
	// The cursor at column 1 inside the block, with `f` typed on the way
	// to `fn`: the item indents itself and matches `fn to_string`.
	src := "struct Item {\n    name: String\n}\n\nimpl Display for Item {\nf" + cursorMark + "\n}\n"
	items := completeWith(t, true, src)
	it := mustItem(t, items, "to_string")
	if got := textEditOf(t, it).NewText; got != "    fn to_string(item: Item): String {\n        $0\n    }" {
		t.Fatalf("insert = %q", got)
	}
	if it.FilterText == nil || *it.FilterText != "fn to_string" {
		t.Fatalf("filter = %v, want `fn to_string`", it.FilterText)
	}
}

func TestCompletion_ImplStubDisplayOnAnEnum(t *testing.T) {
	src := "enum TrafficLight {\n    Red\n    Green\n}\n\nimpl Display for TrafficLight {\n    to" + cursorMark + "\n}\n"
	items := completeWith(t, true, src)
	want := "fn to_string(traffic_light: TrafficLight): String {\n    $0\n}"
	if got := insertOf(t, items, "to_string"); got != want {
		t.Fatalf("snippet =\n%s\nwant\n%s", got, want)
	}
	// The item replaces the typed word.
	te := textEditOf(t, mustItem(t, items, "to_string"))
	if te.Range.Start.Character != 4 || te.Range.End.Character != 6 {
		t.Fatalf("range = %+v, want the word `to`", te.Range)
	}
}

const shapeIface = `interface Shape {
    fn area(shape: self): Float
    fn scale(shape: self, factor: Float): self

    open fn describe(_shape: self): String {
        "a shape"
    }

    fn name(_shape: self): String {
        "shape"
    }
}

struct Square {
    side: Float
}
`

func TestCompletion_ImplStubUserInterface(t *testing.T) {
	src := shapeIface + "\nimpl Shape for Square {\n    " + cursorMark + "\n}\n"
	items := completeWith(t, true, src)
	// The all-at-once item first, the required functions in declaration
	// order, then the open default. The final default is never offered:
	// an impl may not override it.
	if got := strings.Join(itemLabels(items), ","); got != "all missing functions,area,scale,describe" {
		t.Fatalf("items = %s", got)
	}
	if got := insertOf(t, items, "area"); got != "fn area(square: Square): Float {\n    $0\n}" {
		t.Fatalf("area = %q", got)
	}
	if got := insertOf(t, items, "scale"); got != "fn scale(square: Square, factor: Float): Square {\n    $0\n}" {
		t.Fatalf("scale = %q", got)
	}
	want := "fn area(square: Square): Float {\n    ${1}\n}\n\nfn scale(square: Square, factor: Float): Square {\n    ${2}\n}"
	if got := insertOf(t, items, "all missing functions"); got != want {
		t.Fatalf("all =\n%s\nwant\n%s", got, want)
	}
	describe := mustItem(t, items, "describe")
	if describe.Detail == nil || !strings.HasPrefix(*describe.Detail, "default: ") {
		t.Fatalf("the default's detail = %v, want it labelled default", describe.Detail)
	}

	plain := completeWith(t, false, src)
	if got := insertOf(t, plain, "all missing functions"); got != "fn area(square: Square): Float {\n        \n    }\n\n    fn scale(square: Square, factor: Float): Square {\n        \n    }" {
		t.Fatalf("plain all = %q", got)
	}
	if got := insertOf(t, plain, "area"); got != "fn area(square: Square): Float {\n        \n    }" {
		t.Fatalf("plain area = %q", got)
	}
	if it := mustItem(t, plain, "area"); it.InsertTextFormat != nil && *it.InsertTextFormat == protocol.InsertTextFormatSnippet {
		t.Fatal("a client without snippet support got a snippet")
	}
}

func TestCompletion_ImplStubSkipsDefinedFunctions(t *testing.T) {
	src := shapeIface + "\nimpl Shape for Square {\n    fn area(square: Square): Float {\n        square.side * square.side\n    }\n\n    " + cursorMark + "\n}\n"
	items := completeWith(t, true, src)
	if got := strings.Join(itemLabels(items), ","); got != "scale,describe" {
		t.Fatalf("items = %s, want scale then describe", got)
	}
}

func TestCompletion_ImplStubAfterFn(t *testing.T) {
	src := shapeIface + "\nimpl Shape for Square {\n    fn sc" + cursorMark + "\n}\n"
	items := completeWith(t, true, src)
	it := mustItem(t, items, "scale")
	te := textEditOf(t, it)
	if te.Range.Start.Character != 4 {
		t.Fatalf("range starts at %d, want the `fn` at 4", te.Range.Start.Character)
	}
	if !strings.HasPrefix(te.NewText, "fn scale(") {
		t.Fatalf("insert = %q", te.NewText)
	}
	if it.FilterText == nil || *it.FilterText != "fn scale" {
		t.Fatalf("filter = %v, want `fn scale`", it.FilterText)
	}
	labelsExclude(t, items, "area")
}

func TestCompletion_ImplStubUnclosedBlock(t *testing.T) {
	src := shapeIface + "\nimpl Shape for Square {\n    fn area(square: Square): Float {\n        square.side\n    }\n\n    " + cursorMark + "\n\nfn main() {\n}\n"
	items := completeWith(t, true, src)
	if got := strings.Join(itemLabels(items), ","); got != "scale,describe" {
		t.Fatalf("items = %s, want scale then describe", got)
	}
}

func TestCompletion_ImplStubNotInBodiesOrInherentBlocks(t *testing.T) {
	cases := map[string]string{
		"function body":  shapeIface + "\nimpl Shape for Square {\n    fn area(square: Square): Float {\n        " + cursorMark + "\n    }\n}\n",
		"inherent block": shapeIface + "\nimpl Square {\n    " + cursorMark + "\n}\n",
		"parameter list": shapeIface + "\nimpl Shape for Square {\n    fn area(\n        " + cursorMark + "\n    ): Float {\n        1.0\n    }\n}\n",
		"top level":      shapeIface + "\n" + cursorMark + "\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			labelsExclude(t, completeWith(t, true, src), "area", "scale", "all missing functions")
		})
	}
	// A function body keeps its own completion.
	labelsInclude(t, completeWith(t, true, cases["function body"]), "square")
}

func TestCompletion_ImplStubGenericImpl(t *testing.T) {
	// Iter<T>'s element type is not in the header, so it is a linked
	// placeholder; the open default follows.
	src := "struct Box<T> {\n    value: T\n}\n\nimpl Iter for Box<T> {\n    " + cursorMark + "\n}\n"
	items := completeWith(t, true, src)
	if got := strings.Join(itemLabels(items), ","); got != "each_while,known_count" {
		t.Fatalf("items = %s", got)
	}
	if got := insertOf(t, items, "each_while"); got != "fn each_while(box: Box<T>, yield: (${1:T}) -> Bool): Bool {\n    $0\n}" {
		t.Fatalf("each_while = %q", got)
	}
	if got := insertOf(t, items, "known_count"); got != "fn known_count(box: Box<T>): Maybe<Int> {\n    $0\n}" {
		t.Fatalf("known_count = %q", got)
	}
}

func TestCompletion_ImplStubGenericInterfaceArguments(t *testing.T) {
	iface := "interface Store<K, V> {\n    fn get(store: self, key: K): Maybe<V>\n    fn put(store: self, key: K, value: V): self\n}\n\nstruct Cache<T> {\n    items: List<T>\n}\n\n"
	t.Run("from the header", func(t *testing.T) {
		items := completeWith(t, true, iface+"impl Store<String, T> for Cache<T> {\n    "+cursorMark+"\n}\n")
		if got := insertOf(t, items, "put"); got != "fn put(cache: Cache<T>, key: String, value: T): Cache<T> {\n    $0\n}" {
			t.Fatalf("put = %q", got)
		}
	})
	t.Run("pinned by a written function", func(t *testing.T) {
		src := iface + "impl Store for Cache<T> {\n    fn get(cache: Cache<T>, key: Int): Maybe<T> {\n        if key > 0 { List.head(cache.items) } else { None }\n    }\n\n    " + cursorMark + "\n}\n"
		items := completeWith(t, true, src)
		if got := insertOf(t, items, "put"); got != "fn put(cache: Cache<T>, key: Int, value: T): Cache<T> {\n    $0\n}" {
			t.Fatalf("put = %q", got)
		}
	})
	t.Run("unpinned", func(t *testing.T) {
		items := completeWith(t, true, iface+"impl Store for Cache<T> {\n    "+cursorMark+"\n}\n")
		want := "fn get(cache: Cache<T>, key: ${1:K}): Maybe<${2:V}> {\n    ${3}\n}\n\nfn put(cache: Cache<T>, key: ${1:K}, value: ${2:V}): Cache<T> {\n    ${4}\n}"
		if got := insertOf(t, items, "all missing functions"); got != want {
			t.Fatalf("all =\n%s\nwant\n%s", got, want)
		}
		plain := completeWith(t, false, iface+"impl Store for Cache<T> {\n    "+cursorMark+"\n}\n")
		if got := insertOf(t, plain, "get"); got != "fn get(cache: Cache<T>, key: K): Maybe<V> {\n        \n    }" {
			t.Fatalf("plain get = %q", got)
		}
	})
}

func TestCompletion_ImplStubTwoSelfParametersKeepTheirNames(t *testing.T) {
	src := "struct Money {\n    cents: Int\n}\n\nimpl Equatable for Money {\n    " + cursorMark + "\n}\n"
	if got := insertOf(t, completeWith(t, true, src), "equal?"); got != "fn equal?(a: Money, b: Money): Bool {\n    $0\n}" {
		t.Fatalf("equal? = %q", got)
	}
}

func TestCompletion_ImplStubImportedInterface(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"engine.nomi": "pub interface Powered {\n    fn label(e: self): String\n\n    open fn describe(e: self, _loud: Bool = False): String {\n        Powered.label(e)\n    }\n}\n",
	})
	src := "import engine.{Powered}\n\nstruct Motor {\n    name: String\n}\n\nimpl Powered for Motor {\n    " + cursorMark + "\n}\n"
	s := NewServer()
	s.snippetSupport = true
	items := completeIn(t, s, "file://"+filepath.Join(dir, "main.nomi"), src).Items
	if got := strings.Join(itemLabels(items), ","); got != "label,describe" {
		t.Fatalf("items = %s", got)
	}
	if got := insertOf(t, items, "describe"); got != "fn describe(motor: Motor, _loud: Bool = False): String {\n    $0\n}" {
		t.Fatalf("describe = %q", got)
	}

	qualified := "import engine\n\nstruct Motor {\n    name: String\n}\n\nimpl engine.Powered for Motor {\n    " + cursorMark + "\n}\n"
	items = completeIn(t, s, "file://"+filepath.Join(dir, "main.nomi"), qualified).Items
	labelsInclude(t, items, "label", "describe")
}
