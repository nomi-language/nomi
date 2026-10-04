package irbuild

import "testing"

// `FromJson.from_json(json)` with no type argument runs on the VM at the type
// its annotated binding names (testdata/fromjson_expected_self.nomi).
//
// The method declares self only in its result, so no argument says which impl
// the call names. The checker solves self from the expected type and records
// it as Symbol.ReturnSelf, and returnSelfDispatch lowers the call as the
// turbofish spelling of that type. Each function returns a Result over a
// different type, which the checker used to take as self instead.
func TestReturnSelf_FromJsonAtTheAnnotatedType(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "Ok(3)\n" +
		"Err([1]: expected int, got bool)\n" +
		"Ok(a)\n" +
		"Err(expected object, got array)\n"
	got := vmReference(fixture("fromjson_expected_self.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
