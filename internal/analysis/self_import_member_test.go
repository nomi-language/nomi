package analysis_test

import (
	"strings"
	"testing"
)

// `import std/io.{self, IOError}` binds `io` as `import std/io` does, so a
// member the file does not have is the same error. It was accepted, and the
// IR builder declined the call; a member the file has still checks clean.
func TestSelfImportMissingFileMemberIsRejected(t *testing.T) {
	errs := checkWithStdlib(`import std/io.{self, IOError}

fn f(e: IOError): IOError {
  e
}

fn main() {
  io.print("a")
  io.no_such_function(1)
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, e.Message)
	}
	want := "file 'io' has no member 'no_such_function'"
	if strings.Join(got, "\n") != want {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}
