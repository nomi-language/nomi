package analysis_test

import (
	"strings"
	"testing"
)

// A body whose return type is Unit, written or omitted, may end in a `dbg`
// observation; the value dbg passes through is discarded.
func TestUnitDbgTail_Accepted(t *testing.T) {
	for name, src := range map[string]string{
		"dbg expression":     "fn main() {\n    x = 3\n    dbg x\n}\n",
		"dbg of a generic":   "fn main() {\n    dbg Some(1)\n}\n",
		"pipe ending in dbg": "fn main() {\n    [1, 2]\n    |> Iter.map(|n| n * 2)\n    |> Iter.to_list()\n    |> dbg\n}\n",
		"written Unit":       "fn show(n: Int): Unit {\n    dbg n + 1\n}\n\nfn main() {\n    show(1)\n}\n",
		"impl function":      "struct Box {\n    n: Int\n}\n\nimpl Box {\n    fn show(b: Box) {\n        dbg b.n\n    }\n}\n\nfn main() {\n    Box.show(Box{n: 1})\n}\n",
		"nested function":    "fn main() {\n    fn show(n: Int) {\n        n |> dbg\n    }\n    show(2)\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(src)
			expectNoStdlibErrors(t, errs)
		})
	}
}

// Only a final dbg is exempt: any other non-Unit final value in a Unit body
// is still the return type mismatch, and so is a pipe whose last stage is
// not the dbg.
func TestUnitDbgTail_OtherValuesStillRejected(t *testing.T) {
	for name, src := range map[string]string{
		"plain value":        "fn main() {\n    x = 3\n    x\n}\n",
		"call":               "fn twice(n: Int): Int {\n    n * 2\n}\n\nfn main() {\n    twice(2)\n}\n",
		"dbg mid-pipe":       "fn main() {\n    [1, 2]\n    |> dbg\n    |> Iter.count()\n}\n",
		"written Unit value": "fn show(n: Int): Unit {\n    n + 1\n}\n\nfn main() {\n    show(1)\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(src)
			expectStdlibError(t, errs, "return type mismatch: expected Unit, got Int")
		})
	}
}

// dbg stays a pass-through: a body of any other return type that ends in
// dbg answers the observed value, which must be the declared type.
func TestUnitDbgTail_NonUnitBodyReturnsTheValue(t *testing.T) {
	_, errs := checkSourceWithStdlib("fn twice(n: Int): Int {\n    dbg n * 2\n}\n\nfn main() {\n    _ = twice(2)\n}\n")
	expectNoStdlibErrors(t, errs)

	_, errs = checkSourceWithStdlib("fn label(n: Int): String {\n    dbg n * 2\n}\n\nfn main() {\n    _ = label(2)\n}\n")
	found := false
	for _, e := range errs {
		found = found || strings.Contains(e.Message, "return type mismatch: expected String, got Int")
	}
	if !found {
		t.Fatalf("a String body ending in `dbg <Int>` was accepted: %v", errs)
	}
}
