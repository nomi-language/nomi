package analysis

import "testing"

// TestCall_ABlockArgumentFillsItsOwnSlot: a block is a value like any
// expression, so as the last positional argument it fills the slot it is
// written in, as `Response.json({ article })` fills `value` and leaves
// `status` to its default. Only a lambda, a field accessor, a capture or a
// bare function name moves to a trailing callback parameter
// (spec §7 *Lambdas at Call Sites*).
func TestCall_ABlockArgumentFillsItsOwnSlot(t *testing.T) {
	for name, src := range map[string]string{
		"a defaulted parameter after it": "fn f(a: Int, b: String = \"s\") -> Int\n  _ = b\n  a\nend\n\nfn main()\n  x = 1\n  _ = f(begin\n      x + 1\n  end)\nend\n",
		"two parameters of one type":     "fn f(a: Int, b: Int = 2) -> Int\n  a * 10 + b\nend\n\nfn main()\n  x = 1\n  _ = f(begin\n    y = x + 1\n    y\n  end)\nend\n",
	} {
		_, errs := checkSource(src)
		if len(errs) != 0 {
			t.Errorf("%s: %+v, want no error", name, errs)
		}
	}
	// The block still fills the first slot when that slot cannot take it.
	_, errs := checkSource("fn f(a: Int, b: String = \"s\") -> Int\n  _ = b\n  a\nend\n\nfn main()\n  _ = f(begin\n      \"x\" + \"y\"\n  end)\nend\n")
	if len(errs) != 1 || errs[0].Message != "argument 1: expected Int, got String" {
		t.Fatalf("errors %+v, want the one `argument 1: expected Int, got String`", errs)
	}
}
