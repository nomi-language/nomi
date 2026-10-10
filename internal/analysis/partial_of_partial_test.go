package analysis_test

import "testing"

// A partial application bound to a name is called, and partially applied
// again, by the callee's parameter names at its open slots, with their
// defaults. The checker knew no names for such a binding, so a named `_` in
// `c(port: _, host: "x")` matched no slot, was dropped, and the call was
// typed as the callee's result: the next call was "`String` is not a
// function".

const partialOfPartialDecls = `
fn connect(host: String, port: Int = 8080, timeout: Int = 30): String {
  "${host}:${Int.to_string(port)}/${Int.to_string(timeout)}"
}
`

// Each case binds the partial at Unit, so the mismatch names its type.
func TestPartialOfPartial_TypesAsAFunctionOfTheOpenSlots(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`  c = connect(_, port: _)
  x: Unit = c(port: _, host: "x")`, "expected Unit, got (Int) -> String"},
		{`  c = connect(_, port: _)
  x: Unit = c(_, 2)`, "expected Unit, got (String) -> String"},
		{`  c = connect(_, port: _)
  x: Unit = c("h", port: _)`, "expected Unit, got (Int) -> String"},
		// A positional written after a named argument skips the slot the
		// name claimed, as in a full call.
		{`  c = connect(_, port: _, timeout: _)
  x: Unit = c(timeout: _, host: "h", _)`, "expected Unit, got (Int, Int) -> String"},
		// Three levels deep.
		{`  c = connect(_, port: _, timeout: _)
  d = c(port: _, host: _)
  e = d(host: "h", port: _)
  x: Unit = e(port: _)`, "expected Unit, got (Int) -> String"},
		// The lone named `_` names the partial's parameter.
		{`  lone = connect("h", port: _)
  x: Unit = lone(port: _)`, "expected Unit, got (Int) -> String"},
		{`  lam = |a: String, b: Int = 5| a
  la = lam(b: _, a: _)
  x: Unit = la(a: "a", b: _)`, "expected Unit, got (Int) -> String"},
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
  c = connect(_, port: _)
  e = c(port: _, host: "x")
  s: String = e(9)
  t: String = e(port: 9)
  u: String = e()
  three = connect(_, port: _, timeout: _)
  m: (Int, Int) -> String = three(timeout: _, host: "h", _)
  _ = (s, t, u, m)
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestPartialOfPartial_RejectsAWrongCall(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`  x = c(nope: _)`, "no parameter named 'nope'"},
		{`  x = c(host: 1, port: _)`, "argument 'host': expected String, got Int"},
		{`  x = c(_, host: _)`, "parameter 'host' already has a value"},
		// The partial of the partial has `port` open and nothing else.
		{`  e = c(host: "x", port: _)
  x = e(host: _)`, "no parameter named 'host'"},
		{`  e = c(host: "x", port: _)
  x = e(1, _)`, "expected 0 to 1 arguments, got 2"},
		{`  e = c(host: "x", port: _)
  x = e("p")`, "argument 1: expected Int, got String"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(partialOfPartialDecls + "\nfn main() {\n  c = connect(_, port: _)\n" + tc.src + "\n  _ = x\n}\n")
			expectStdlibError(t, errs, tc.want)
		})
	}
}
