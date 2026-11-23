package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// A struct that holds itself through an anonymous struct field's Option
// (testdata/anon_recursive.nomi declares one) is built two levels deep and
// read back. Its literal declined: irComposedFieldKind held a record field
// only when it reached no type the walk was deciding, so `wrap: {inner:
// Option<Node>, depth: Int}` left Node outside the retained structs, where a
// tuple field of the same parts was decided part by part.
func TestAnonRecursiveStruct_BuildsAndReads(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	src := `struct Node
    value: Int
    wrap: {inner: Option<Node>, depth: Int}
end

fn inner_value(n: Node) -> Int
    case n.wrap.inner
        Some(m) -> m.value
        None    -> 0
    end
end

fn main()
    leaf = Node{value: 1, wrap: {inner: None, depth: 0}}
    top = Node{value: 2, wrap: {inner: Some(leaf), depth: 1}}
    dbg top.value
    dbg top.wrap.depth
    dbg inner_value(top)
    dbg inner_value(leaf)
    dbg Debug.inspect(top)
end
`
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(path); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	const want = `dbg line 16: top.value = 2
dbg line 17: top.wrap.depth = 1
dbg line 18: inner_value(top) = 1
dbg line 19: inner_value(leaf) = 0
dbg line 20: Debug.inspect(top) = "Node{value: 2, wrap: {depth: 1, inner: Some(Node{value: 1, wrap: {depth: 0, inner: None}})}}"
`
	got := vmReference(path)
	if got.stdout+got.stderr != want || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
}
