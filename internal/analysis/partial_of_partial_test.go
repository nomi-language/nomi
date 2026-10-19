package analysis_test

import "testing"

// A partial application bound to a name is called, and partially applied
// again, by the callee's parameter names at its open slots, with their
// defaults. The checker knew no names for such a binding, so a named `_` in
// `c(port: _, host: "x")` matched no slot, was dropped, and the call was
// typed as the callee's result: the next call was "`String` is not a
// function".

const partialOfPartialDecls = `
fn connect(host: String, port: Int = 8080, timeout: Int = 30) -> String {
  "${host}:${Int.to_string(port)}/${Int.to_string(timeout)}"
}
`

// Each case binds the partial at Unit, so the mismatch names its type.
func TestPartialOfPartial_TypesAsAFunctionOfTheOpenSlots(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`  c = &connect(&1, port: &2)
  x: Unit = &c(port: &1, host: "x")`, "expected Unit, got (Int) -> String"},
		{`  c = &connect(&1, port: &2)
  x: Unit = &c(&1, 2)`, "expected Unit, got (String) -> String"},
		{`  c = &connect(&1, port: &2)
  x: Unit = &c("h", port: &1)`, "expected Unit, got (Int) -> String"},
		// A positional written after a named argument skips the slot the
		// name claimed, as in a full call.
		{`  c = &connect(&1, port: &2, timeout: &3)
  x: Unit = &c(timeout: &1, host: "h", &2)`, "expected Unit, got (Int, Int) -> String"},
		// Three levels deep.
		{`  c = &connect(&1, port: &2, timeout: &3)
  d = &c(port: &1, host: &2)
  e = &d(host: "h", port: &1)
  x: Unit = &e(port: &1)`, "expected Unit, got (Int) -> String"},
		// The lone named `&1` names the partial's parameter.
		{`  lone = &connect("h", port: &1)
  x: Unit = &lone(port: &1)`, "expected Unit, got (Int) -> String"},
		{`  lam = fn a: String, b: Int = 5 -> a
  la = &lam(b: &1, a: &2)
  x: Unit = &la(a: "a", b: &1)`, "expected Unit, got (Int) -> String"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(partialOfPartialDecls + "\nfn main() {\n" + tc.src + "\n  _ = x\n}\n")
			expectStdlibError(t, errs, tc.want)
		})
	}
}

func TestPartialOfPartial_CallsAndDefaultsCheck(t *testing.T) {
	_, errs := checkSourceWithStdlib(partialOfPartialDecls + `
fn main() {
  c = &connect(&1, port: &2)
  e = &c(port: &1, host: "x")
  s: String = e(9)
  t: String = e(port: 9)
  u: String = e()
  three = &connect(&1, port: &2, timeout: &3)
  m: (Int, Int) -> String = &three(timeout: &1, host: "h", &2)
  _ = #(s, t, u, m)
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestPartialOfPartial_RejectsAWrongCall(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`  x = &c(nope: &1)`, "no parameter named 'nope'"},
		{`  x = &c(host: 1, port: &1)`, "argument 'host': expected String, got Int"},
		{`  x = &c(&1, host: &2)`, "parameter 'host' already has a value"},
		// The partial of the partial has `port` open and nothing else.
		{`  e = &c(host: "x", port: &1)
  x = &e(host: &1)`, "no parameter named 'host'"},
		{`  e = &c(host: "x", port: &1)
  x = &e(1, &1)`, "expected 0 to 1 arguments, got 2"},
		{`  e = &c(host: "x", port: &1)
  x = e("p")`, "argument 1: expected Int, got String"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(partialOfPartialDecls + "\nfn main() {\n  c = &connect(&1, port: &2)\n" + tc.src + "\n  _ = x\n}\n")
			expectStdlibError(t, errs, tc.want)
		})
	}
}
