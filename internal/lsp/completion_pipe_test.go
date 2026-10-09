package lsp

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCompletion_PipeOffersFunctionsTakingTheValue(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		want, not []string
	}{
		{
			"a List is an Iter",
			"fn f(xs: List<Int>): Int {\n    xs |> " + cursorMark + "\n}\n",
			[]string{"Iter.map", "Iter.filter", "Iter.count", "List.head", "List.concat", "dbg"},
			[]string{"Iter.repeat", "Iter.iterate", "String.trim", "if_missing", "try"},
		},
		{
			"a String",
			"fn f(s: String): String {\n    s |> " + cursorMark + "\n}\n",
			[]string{"String.trim", "String.to_upper", "Display.to_string"},
			[]string{"List.head", "Duration.seconds"},
		},
		{
			"typed prefix filters on the function name",
			"fn f(s: String): String {\n    s |> tri" + cursorMark + "\n}\n",
			[]string{"String.trim"},
			[]string{"String.to_upper"},
		},
		{
			"the file's own functions whose first parameter fits",
			"fn double(n: Int): Int {\n    n * 2\n}\n\nfn shout(s: String): String {\n    s\n}\n\nfn f(): Int {\n    3 |> " + cursorMark + "\n}\n",
			[]string{"double", "Int.to_string"},
			[]string{"shout", "f"},
		},
		{
			"a recorded value type",
			"fn f(xs: List<Int>): Int {\n    ys = xs |> Iter.map(|x| x + 1) |> Iter.to_list()\n    ys |> " + cursorMark + "\n}\n",
			[]string{"Iter.map", "List.head"},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, tt.src)
			labelsInclude(t, items, tt.want...)
			labelsExclude(t, items, tt.not...)
		})
	}
}

func TestCompletion_PipeInsertsTheCallWithoutItsFirstArgument(t *testing.T) {
	tests := []struct {
		name, src, label, text string
	}{
		{"bare stage", "fn f(xs: List<Int>): List<Int> {\n    xs |> ma" + cursorMark + "\n}\n", "Iter.map", "Iter.map(${1:f})$0"},
		{"no argument left", "fn f(xs: List<Int>): Int {\n    xs |> cou" + cursorMark + "\n}\n", "Iter.count", "Iter.count()$0"},
		{"owner already written", "fn f(xs: List<Int>): List<Int> {\n    xs |> Iter.ma" + cursorMark + "\n}\n", "map", "map(${1:f})$0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			it := mustItem(t, completeWith(t, true, tt.src), tt.label)
			if te := textEditOf(t, it); te.NewText != tt.text {
				t.Fatalf("insert = %q, want %q", te.NewText, tt.text)
			}
		})
	}
}

func TestCompletion_PipeOffersImportedFileFunctions(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"text.nomi": "pub fn shout(s: String): String {\n    s\n}\n\npub fn twice(n: Int): Int {\n    n * 2\n}\n",
		"main.nomi": "fn main() {\n}\n",
	})
	s := NewServer()
	uri := "file://" + filepath.Join(dir, "main.nomi")
	items := completeIn(t, s, uri, "import text\n\nfn main() {\n    \"a\" |> "+cursorMark+"\n}\n").Items
	labelsInclude(t, items, "text.shout")
	labelsExclude(t, items, "text.twice")
}

// A closed project file is an import source with index declarations and no
// analyzed scope; looking up an owner the document does not name reads it.
func TestCompletion_PipeOwnerLookupReadsClosedFiles(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"lib.nomi":  "import std/duration.Duration\n\npub fn pause(): Duration {\n    Duration.seconds(1)\n}\n",
		"main.nomi": "fn main() {\n}\n",
	})
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	s.docs.IndexWorkspace(context.Background())
	uri := "file://" + filepath.Join(dir, "main.nomi")
	items := completeIn(t, s, uri, "import lib\n\nfn main() {\n    lib.pause() |> "+cursorMark+"\n}\n").Items
	labelsInclude(t, items, "Duration.as_seconds")
}

// `dbg` is a stage anywhere in a pipe, offered after the functions that
// take the value; `assert` and `refute`, which a stage cannot be, are not.
func TestCompletion_PipeKeywordStages(t *testing.T) {
	items := complete(t, "fn f(xs: List<Int>): Int {\n    xs |> "+cursorMark+"\n}\n")
	labelsInclude(t, items, "dbg", "if", "case", "then", "tap")
	labelsExclude(t, items, "assert", "refute")
	labelsBefore(t, items, "Iter.count", "dbg")
	labelsBefore(t, items, "List.head", "dbg")

	// Mid-chain, and on a new line.
	items = complete(t, "fn f(xs: List<Int>): Int {\n    xs\n    |> List.tail()\n    |> "+cursorMark+"\n    |> Iter.count()\n}\n")
	labelsInclude(t, items, "dbg")
	labelsBefore(t, items, "Maybe.map", "dbg")

	// Typed, it matches as any word does.
	items = complete(t, "fn f(xs: List<Int>): Int {\n    xs |> db"+cursorMark+"\n}\n")
	if len(items) == 0 || items[0].Label != "dbg" {
		t.Errorf("want dbg first after `db`, got %v", itemLabels(items))
	}

	// A function whose type fits the expected type ranks first; dbg after.
	items = complete(t, "fn double(n: Int): Int {\n    n * 2\n}\n\nfn f(n: Int): Int {\n    n |> "+cursorMark+"\n}\n")
	labelsBefore(t, items, "double", "dbg")

	// An imported file's function, whose label sorts after `dbg`.
	dir := writeProject(t, map[string]string{
		"lib.nomi":  "pub fn shout(s: String): String {\n    s\n}\n",
		"main.nomi": "fn main() {\n}\n",
	})
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	s.docs.IndexWorkspace(context.Background())
	uri := "file://" + filepath.Join(dir, "main.nomi")
	items = completeIn(t, s, uri, "import lib\n\nfn main() {\n    _ = \"a\" |> "+cursorMark+"\n}\n").Items
	labelsBefore(t, items, "lib.shout", "dbg")
}

// `try` is a stage only over a Result or a Maybe, so it is offered when the
// piped value is one, or when its type is not known, and not otherwise.
func TestCompletion_PipeOffersTryOnlyWhereItApplies(t *testing.T) {
	tests := []struct {
		name, src string
		offered   bool
	}{
		{"a Result", "fn f(r: Result<Int, String>): Result<Int, String> {\n    n = r |> " + cursorMark + "\n    Ok(n)\n}\n", true},
		{"a Maybe", "fn f(m: Maybe<Int>): Maybe<Int> {\n    n = m |> " + cursorMark + "\n    Some(n)\n}\n", true},
		{"a call returning a Maybe", "fn f(s: String): Maybe<Int> {\n    n = s |> String.to_int() |> " + cursorMark + "\n    Some(n)\n}\n", true},
		{"an unknown type", "fn f(): Maybe<Int> {\n    n = nope |> " + cursorMark + "\n    Some(n)\n}\n", true},
		{"an Int", "fn f(n: Int): Int {\n    n |> " + cursorMark + "\n}\n", false},
		{"a String", "fn f(s: String): String {\n    s |> " + cursorMark + "\n}\n", false},
		{"a List", "fn f(xs: List<Int>): Int {\n    xs |> " + cursorMark + "\n}\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, tt.src)
			labelsInclude(t, items, "dbg")
			if tt.offered {
				labelsInclude(t, items, "try")
			} else {
				labelsExclude(t, items, "try")
			}
		})
	}
}
