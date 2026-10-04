package doctest

import (
	"reflect"
	"testing"
)

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
