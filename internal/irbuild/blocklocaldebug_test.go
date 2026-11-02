package irbuild

import (
	"strings"
	"testing"
)

// TestBlockLocalDebug_RendersAsAModuleLevelDeclaration pins Debug over types
// declared inside a body. Every such call was BLOCKED ("qualified call,
// Type.method: Debug.inspect") because no impl reached the builder for a
// block-local type; registerBlockLocalDebug supplies the one the front end
// writes at module level, so the text is that declaration's derived Debug.
func TestBlockLocalDebug_RendersAsAModuleLevelDeclaration(t *testing.T) {
	const want = "Token(\"x\")\n" +
		"Coord(1, 2)\n" +
		"Items([1, 2])\n" +
		"Pt{y: 2, x: 1, tok: Token(\"t\")}\n" +
		"Dot\n" +
		"Circle(1.5)\n" +
		"Rect{w: 1, h: 2}\n" +
		"[Dot, Circle(2.0)]\n" +
		"Some(Token(\"m\"))\n" +
		"Token(\"i\")\n" +
		"dbg line 38: Token(\"d\") = Token(\"d\")\n" +
		"Pt{name: \"second\"}\n"
	if got := vmReference(fixture("block_local_debug.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s", got, want)
	}
}

// TestBlockLocalDebug_InTestBodies runs the same rule for declarations inside
// `test` bodies.
func TestBlockLocalDebug_InTestBodies(t *testing.T) {
	got := vmReference(fixture("block_local_debug_tests.nomi"))
	if got.exit != 0 || !strings.Contains(got.stdout, "2 passed, 0 failed") {
		t.Fatalf("both tests must pass on the VM: %s", got)
	}
}
