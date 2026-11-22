package doctest

import (
	"fmt"
	"reflect"
	"testing"
)

func TestInfoWords(t *testing.T) {
	md := "```nomi-run error\nx\n```\n\n```nomi-run no-run\ny\n```\n\n```nomi-run\nz\n```\n\n```nomi-run ignore\nw\n```\n" +
		"\n```nomi-run manual\nv\n```\n\n```nomi-run manual error\nu\n```\n\n```nomi-run manual expand\nt\n```\n"
	blocks := ExtractBlocks(md, "nomi-run")
	if len(blocks) != 7 {
		t.Fatalf("expected seven blocks, got %d", len(blocks))
	}
	for i, want := range []bool{false, false, true, true, true, false, true} {
		if got := blocks[i].Runs(); got != want {
			t.Errorf("block %d (%v): Runs() = %v, want %v", i, blocks[i].Info, got, want)
		}
	}
	// `expand` is no word a block carries.
	for i, want := range [][]string{nil, nil, nil, {"ignore"}, nil, nil, {"expand"}} {
		if got := blocks[i].UnknownInfo(); !reflect.DeepEqual(got, want) {
			t.Errorf("block %d: UnknownInfo() = %#v, want %#v", i, got, want)
		}
	}
}

func TestFences(t *testing.T) {
	md := "a\n```\nplain\n```\n\n```tree\nx/\n```\n\n```nomi-run error\ny\n```\n"
	var got []string
	for _, b := range Fences(md) {
		got = append(got, fmt.Sprintf("%s@%d:%s", b.Lang, b.Line, b.Code))
	}
	if want := []string{"@2:plain", "tree@6:x/", "nomi-run@10:y"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Fences = %q, want %q", got, want)
	}
	if blocks := ExtractBlocks(md, "tree"); len(blocks) != 1 || blocks[0].Line != 6 {
		t.Errorf("ExtractBlocks(tree) = %+v, want the one tree block at line 6", blocks)
	}
}

func TestDocCommentMarkdown(t *testing.T) {
	src := "//# Module.\n//# ```text\n//# a\n//# ```\nfn f() -> Int\n    /// ```nomi\n    ///x = 1\n    /// ```\n    1\nend\n"
	want := "Module.\n```text\na\n```\n\n```nomi\nx = 1\n```\n\n\n"
	if got := DocCommentMarkdown(src); got != want {
		t.Errorf("DocCommentMarkdown = %q, want %q", got, want)
	}
	if blocks := Fences(DocCommentMarkdown(src)); len(blocks) != 2 || blocks[1].Lang != "nomi" || blocks[1].Line != 6 {
		t.Errorf("Fences of the doc comments = %+v, want ```text at 2 and ```nomi at 6", blocks)
	}
}

func TestReadOnlyInfo(t *testing.T) {
	blocks := ExtractBlocks("```nomi-run read-only:photos/* manual read-only:notes/a.md read-only:\nx\n```\n", "nomi-run")
	if len(blocks) != 1 {
		t.Fatalf("expected one block, got %d", len(blocks))
	}
	b := blocks[0]
	if got, want := b.UnknownInfo(), []string{"read-only:"}; !reflect.DeepEqual(got, want) {
		t.Errorf("UnknownInfo() = %#v, want %#v: a read-only: word needs a glob", got, want)
	}
	if got, want := b.ReadOnlyGlobs(), []string{"photos/*", "notes/a.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ReadOnlyGlobs() = %#v, want %#v", got, want)
	}
	for name, want := range map[string]bool{"photos/beach.jpg": true, "photos/2026/x.jpg": false, "notes/a.md": true, "notes/b.md": false} {
		if got := b.MarkedReadOnly(name); got != want {
			t.Errorf("MarkedReadOnly(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestShownPutsTheFailureOnTheLineAfterTheOutput(t *testing.T) {
	for _, tc := range []struct{ output, failure, want string }{
		{"a\nb\n", "", "a\nb\n"},
		{"a\n", "error: div by zero", "a\nerror: div by zero"},
		{"a", "error: div by zero", "a\nerror: div by zero"},
		{"", "todo reached at main.nomi:6: parse", "todo reached at main.nomi:6: parse"},
	} {
		if got := Shown(tc.output, tc.failure); got != tc.want {
			t.Errorf("Shown(%q, %q) = %q, want %q", tc.output, tc.failure, got, tc.want)
		}
	}
}

func TestExtractBlocksReadsHiddenExpect(t *testing.T) {
	md := `Before.

` + "```nomi" + `
fn main()
  io.print("hi")
end
` + "```" + `
<!-- expect
hi
there
-->

After.`

	blocks := ExtractBlocks(md, "nomi")
	if len(blocks) != 1 {
		t.Fatalf("expected one block, got %d", len(blocks))
	}
	want := []string{"hi", "there"}
	if !reflect.DeepEqual(blocks[0].Expected, want) {
		t.Fatalf("expected hidden output %#v, got %#v", want, blocks[0].Expected)
	}
}

func TestToolInfo(t *testing.T) {
	blocks := ExtractBlocks("```nomi-run manual tool:sqlkit/generate tool:sqlkit tool:/x\nx\n```\n", "nomi-run")
	if len(blocks) != 1 {
		t.Fatalf("expected one block, got %d", len(blocks))
	}
	b := blocks[0]
	if got, want := b.UnknownInfo(), []string{"tool:sqlkit", "tool:/x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("UnknownInfo() = %#v, want %#v: a tool: word names a module and a tool", got, want)
	}
	if got, want := b.Tools(), []Tool{{Module: "sqlkit", Name: "generate"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tools() = %#v, want %#v", got, want)
	}
}
