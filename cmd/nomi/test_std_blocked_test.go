package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/termcolor"
)

// The `nomi test` and `nomi check` reports of a stdlib module's case that
// reaches a std body the builder declines, in another std file. Each plants
// the decline on a call std itself makes (withPlantedDeclineOf), so the
// child lowers std with it in place.

// stdPosition is the line and column of the first occurrence of text in the
// stdlib file path (relative to the repository root, the tests' working
// directory), at the occurrence's offset within it.
func stdPosition(t *testing.T, path, text string, offset int) (int, int) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(src), text)
	if i < 0 {
		t.Fatalf("%s has no %q", path, text)
	}
	i += offset
	line := strings.Count(string(src[:i]), "\n") + 1
	return line, i - strings.LastIndex(string(src[:i]), "\n")
}

// A std/timer.nomi prompt calls `Duration.nanoseconds`, whose body in
// std/duration.nomi declines at its call to `nanos_in_range`. The case was
// reported at the prompt's call in std/timer.nomi ("this call to
// `Duration.nanoseconds` is not supported yet"), since the cached stdlib
// lowering keeps no std body's decline; it is reported at the construct the
// builder declined, in its own file and line, by `nomi test` and
// `nomi check` alike.
func TestTestCommand_StdCaseBlockedInAnotherStdFileNamesThatFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	withPlantedDeclineOf(t, "nanos_in_range")
	promptLine, _ := stdPosition(t, "std/timer.nomi", "//! assert timer.sleep(Duration.nanoseconds(0))", 0)
	line, col := stdPosition(t, "std/duration.nomi", "pub fn nanoseconds(n: Int) -> Duration\n        Duration(nanos_in_range(n))", len("pub fn nanoseconds(n: Int) -> Duration\n        Duration("))
	at := fmt.Sprintf("std/duration.nomi:%d:%d", line, col)
	message := "this call to `nanos_in_range` is not supported yet, so `fn Duration.nanoseconds` cannot run"

	out, err := runNomi(t, t.TempDir(), "test", "std/timer.nomi")
	plain := termcolor.StripANSI(out)
	if err == nil {
		t.Fatalf("nomi test with a blocked case exited 0:\n%s", plain)
	}
	want := fmt.Sprintf("BLOCKED std/timer.nomi :: sleep //! test line %d %s: %s\n"+
		"  %s: help: this is a gap in Nomi, not a mistake in your code; please report it at https://github.com/nomi-language/nomi/issues\n"+
		"test result: BLOCKED. 0 passed, 0 failed, 1 blocked\n", promptLine, at, message, at)
	if plain != want {
		t.Fatalf("nomi test std/timer.nomi printed:\n%s\nwant:\n%s", plain, want)
	}

	check, err := runNomi(t, t.TempDir(), "check", "std/timer.nomi")
	plain = termcolor.StripANSI(check)
	if err == nil {
		t.Fatalf("nomi check on a blocked case exited 0:\n%s", plain)
	}
	gutter := strings.Repeat(" ", len(fmt.Sprint(line)))
	if !strings.HasPrefix(plain, "error: "+message+"\n"+gutter+"--> "+at+"\n") {
		t.Fatalf("nomi check std/timer.nomi printed:\n%s\nwant the error %q at %s", plain, message, at)
	}
}

// A program-std module's body (std/log.nomi's `warn`, lowered with the
// program that reaches it) that declines at a call is reported as that
// call, at its callee: the report found no node in a stdlib file, and said
// "its body is not supported yet" at the call's parenthesis.
func TestTestCommand_ProgramStdBodyBlockedNamesTheCall(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	withPlantedDeclineOf(t, "log")
	line, col := stdPosition(t, "std/log.nomi", "    log(logger, Level.Warn, message, fields)", len("    "))
	at := fmt.Sprintf("std/log.nomi:%d:%d", line, col)
	want := " " + at + ": this call to `log` is not supported yet, so `fn warn` cannot run\n"

	out, err := runNomi(t, t.TempDir(), "test", "std/log_testing.nomi")
	plain := termcolor.StripANSI(out)
	if err == nil || !strings.Contains(plain, want) || strings.Contains(plain, "its body is not supported yet") {
		t.Fatalf("nomi test std/log_testing.nomi (%v) printed:\n%s\nwant a blocked case with %q", err, plain, want)
	}
}
