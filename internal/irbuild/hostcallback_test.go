package irbuild

import "testing"

// TestHostCallback_GoCallsANomiHandlerFromGoroutines runs
// testdata/go_calls_nomi_handler through its generated FFI wrapper. The Go
// half calls the Nomi handler from one goroutine per request, all released
// together, as a Go HTTP server calls its handler. Each call crosses a struct
// with a String, two Maps and Bytes into Nomi and a struct back out, or an
// Err; Go renders what came back, one line per request in request order.
//
// So a field read from the wrong offset, a Bytes that arrived as its Go
// `string` representation, a map lost in conversion, an Err that did not
// cross, or a handler call that is not safe from several goroutines at once
// is a visible difference in the output rather than a passing build.
func TestHostCallback_GoCallsANomiHandlerFromGoroutines(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; compiles a Go FFI wrapper; -short")
	}
	want := "0 /: 200 text/plain \"hello POST n=0\"\n" +
		"1 /echo: 200 application/octet-stream \"body 1\"\n" +
		"2 /missing: 404 text/plain \"not found\"\n" +
		"3 /fail: error refused request 3\n" +
		"4 /echo: 200 application/octet-stream \"body 4\"\n" +
		"5 /: 200 text/plain \"hello POST n=5\"\n" +
		"6 /fail: error refused request 6\n" +
		"7 /missing: 404 text/plain \"not found\"\n"
	got := vmReference(fixture("go_calls_nomi_handler/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
