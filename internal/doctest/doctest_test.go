package doctest

import (
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
	for i, want := range []bool{false, false, true, true, true, false, false} {
		if got := blocks[i].Runs(); got != want {
			t.Errorf("block %d (%v): Runs() = %v, want %v", i, blocks[i].Info, got, want)
		}
	}
	for i, want := range [][]string{nil, nil, nil, {"ignore"}, nil, nil, nil} {
		if got := blocks[i].UnknownInfo(); !reflect.DeepEqual(got, want) {
			t.Errorf("block %d: UnknownInfo() = %#v, want %#v", i, got, want)
		}
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
fn main() {
  io.print("hi")
}
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
