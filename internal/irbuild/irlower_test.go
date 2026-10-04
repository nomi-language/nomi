package irbuild

import "testing"

// lowerIROnly runs the front end and the IR builder over an in-memory module
// for a test that reads what the builder did through its observation hooks
// (irFuncObserved, IRDeclineObserved) rather than through the Result. A body
// the builder declines is part of what such a test reads, so the refusal is
// not an error here.
func lowerIROnly(t *testing.T, src string) *Result {
	t.Helper()
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
