package analysis_test

import (
	"strings"
	"testing"
)

// A bare variant in a generic constructor's argument resolves against the
// enum the expected type names for it, even when the expected type also names
// the enclosing function's own type parameter (spec, Variant Resolution).
// `Err(.Full)` in `fn write<T>(...) -> Result<Buffer<T>, Error>` takes E from
// the result and leaves T to the arguments; it used to report ".Full cannot be
// resolved here" because only a wholly concrete expected type seeded the call.

const genericResultDecls = `
pub enum E {
  A
  B
}

pub struct Box<T> {
  items: List<T>
}
`

func TestDotVariant_GenericConstructorInAGenericResult(t *testing.T) {
	for name, body := range map[string]string{
		"tail":       "pub fn f<T>(_x: T) -> Result<T, E> {\n  Err(.A)\n}\n",
		"return":     "pub fn f<T>(x: T, ok: Bool) -> Result<T, E> {\n  if !ok {\n    return Err(.B)\n  }\n  Ok(x)\n}\n",
		"arm":        "pub fn f<T>(x: T, n: Int) -> Result<T, E> {\n  case n {\n    0 -> Err(.A)\n    _ -> Ok(x)\n  }\n}\n",
		"nested T":   "pub fn f<T>(_b: Box<T>) -> Result<Box<T>, E> {\n  Err(.A)\n}\n",
		"two params": "pub fn f<T, U>(_x: T, _y: U) -> Result<#(T, U), E> {\n  Err(.B)\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(genericResultDecls + body + "\nfn main() {\n}\n")
			for _, e := range errs {
				t.Errorf("the checker rejects a bare variant in a generic result: %s", diagText(e))
			}
		})
	}
}

// The enum still decides: a variant it lacks is the usual error, naming it.
func TestDotVariant_GenericConstructorInAGenericResultChecksTheVariant(t *testing.T) {
	_, errs := checkSourceWithStdlib(genericResultDecls + "pub fn f<T>(_x: T) -> Result<T, E> {\n  Err(.Z)\n}\n\nfn main() {\n}\n")
	if len(errs) == 0 {
		t.Fatal("the checker admits Err(.Z), which E does not declare")
	}
	if got := diagText(errs[0]); !strings.Contains(got, "no variant 'Z' on enum E") {
		t.Errorf("error %q does not name the missing variant", got)
	}
}
