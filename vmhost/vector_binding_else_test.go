package vmhost_test

import "testing"

// A binding `else` on a vector pattern with elements after the `..` is
// refutable: the checker admits the else, and the VM runs the else for a
// vector the pattern does not match.
func TestVectorPatternWithTailsBindingElse_Runs(t *testing.T) {
	got := runOutput(t, `import std/io

fn ends_one_three(v: Vector<Int>) -> Bool {
    #[1, .., 3] = v else {
        return False
    }
    True
}

fn middle(v: Vector<Int>) -> Int {
    #[first, ..mid, last] = v else {
        return -1
    }
    first + Vector.length(mid) + last
}

fn main() {
    io.print("${ends_one_three(#[1, 2, 3])}")
    io.print("${ends_one_three(#[1, 2])}")
    io.print("${ends_one_three(#[1, 3])}")
    io.print("${middle(#[10, 2, 3, 20])}")
    io.print("${middle(#[10])}")
}
`)
	want := "True\nFalse\nTrue\n32\n-1\n"
	if got != want {
		t.Errorf("output:\n got %q\nwant %q", got, want)
	}
}
