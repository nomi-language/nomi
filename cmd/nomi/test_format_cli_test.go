package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `nomi test --format json`: the record stream an editor integration reads.
// The records are matched to source by file and first line, so these tests
// pin the positions as well as the statuses.

const formatFixture = `import std/io

/// Doubles.
//! assert double(2) == 4
fn double(x: Int): Int {
  x * 2
}

test "passes" {
  io.print("hello from a test")
  assert 1 + 1 == 2
}

test "fails" {
  xs = [1, 2, 3]
  assert Iter.count(xs) == 4
}

test "traps" {
  n = 0
  assert 10 / n == 1
}

tests "group" {
  test "inner ok" {
    assert True
  }

  test "inner bad" {
    assert False
  }
}
`

// The text report for formatFixture, recorded from the command before json
// mode existed. Text mode must still print exactly this.
const formatFixtureText = `hello from a test
ok fixture_test.nomi :: double //! test line 4
ok fixture_test.nomi :: passes
FAIL fixture_test.nomi :: fails
  line 16: assertion failed
    assert Iter.count(xs) == 4
    values:
      xs
        = [1, 2, 3]
      Iter.count(xs)
        = 3
FAIL fixture_test.nomi :: traps
  line 21: division by zero
ok fixture_test.nomi :: group / inner ok
FAIL fixture_test.nomi :: group / inner bad
  line 30: assertion failed
    assert False
test result: FAILED. 3 passed, 3 failed
`

type jsonTestRecord struct {
	Type      string  `json:"type"`
	File      string  `json:"file"`
	Line      int     `json:"line"`
	EndLine   int     `json:"end_line"`
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	Message   *string `json:"message"`
	ErrorLine int     `json:"error_line"`
	Passed    int     `json:"passed"`
	Failed    int     `json:"failed"`
	Blocked   int     `json:"blocked"`
}

// runNomiSplit runs nomi in dir with colour off and returns stdout and stderr
// separately, since json mode's contract is about which stream gets what.
func runNomiSplit(t *testing.T, cacheRoot, dir string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(nomiBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+cacheRoot, "NO_COLOR=1", "NOMI_COLOR=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func parseJSONRecords(t *testing.T, stdout string) []jsonTestRecord {
	t.Helper()
	var records []jsonTestRecord
	for i, line := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		var rec jsonTestRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("stdout line %d is not a JSON record: %v\n%q\nwhole stdout:\n%s", i+1, err, line, stdout)
		}
		records = append(records, rec)
	}
	return records
}

func writeFormatFixture(t *testing.T) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	// macOS hands out /var/... temp dirs that are symlinks to /private/var;
	// the command reports the path it resolved, so compare against that.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	path = filepath.Join(dir, "fixture_test.nomi")
	mustWrite(t, path, formatFixture)
	return dir, path
}

func TestTestCommand_TextFormatUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir, _ := writeFormatFixture(t)
	for _, args := range [][]string{
		{"test", "fixture_test.nomi"},
		{"test", "fixture_test.nomi", "--format", "text"},
		{"test", "--format=text", "fixture_test.nomi"},
	} {
		stdout, stderr, err := runNomiSplit(t, t.TempDir(), dir, args...)
		if err == nil {
			t.Fatalf("%v: expected a failing exit status", args)
		}
		if stdout != formatFixtureText {
			t.Fatalf("%v: text report changed.\ngot:\n%s\nwant:\n%s", args, stdout, formatFixtureText)
		}
		if stderr != "" {
			t.Fatalf("%v: unexpected stderr:\n%s", args, stderr)
		}
	}
}

func TestTestCommand_JSONFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir, path := writeFormatFixture(t)
	stdout, stderr, err := runNomiSplit(t, t.TempDir(), dir, "test", "fixture_test.nomi", "--format", "json")
	if err == nil {
		t.Fatalf("expected a failing exit status, as text mode has")
	}
	if stderr != "hello from a test\n" {
		t.Fatalf("a test's own output belongs on stderr in json mode; stderr:\n%q", stderr)
	}
	records := parseJSONRecords(t, stdout)

	type want struct {
		line, endLine int
		name, status  string
		errorLine     int
	}
	wants := []want{
		{4, 4, "fixture_test.nomi :: double //! test line 4", "passed", 0},
		{9, 12, "fixture_test.nomi :: passes", "passed", 0},
		{14, 17, "fixture_test.nomi :: fails", "failed", 16},
		{19, 22, "fixture_test.nomi :: traps", "failed", 21},
		{25, 27, "fixture_test.nomi :: group / inner ok", "passed", 0},
		{29, 31, "fixture_test.nomi :: group / inner bad", "failed", 30},
	}
	if len(records) != len(wants)+1 {
		t.Fatalf("got %d records, want %d tests and a summary:\n%s", len(records), len(wants), stdout)
	}
	for i, w := range wants {
		rec := records[i]
		if rec.Type != "test" || rec.File != path || rec.Line != w.line || rec.EndLine != w.endLine ||
			rec.Name != w.name || rec.Status != w.status || rec.ErrorLine != w.errorLine {
			t.Errorf("record %d = %+v, want %+v in %s", i, rec, w, path)
		}
		if rec.Message == nil {
			t.Errorf("record %d has no message field", i)
		} else if (w.status == "passed") != (*rec.Message == "") {
			t.Errorf("record %d: status %s with message %q", i, rec.Status, *rec.Message)
		}
	}

	// The message is the text report's block under the FAIL line, verbatim.
	fails := *records[2].Message
	wantFails := "  line 16: assertion failed\n    assert Iter.count(xs) == 4\n    values:\n      xs\n        = [1, 2, 3]\n      Iter.count(xs)\n        = 3"
	if fails != wantFails {
		t.Errorf("failure message:\n%q\nwant:\n%q", fails, wantFails)
	}
	if !strings.Contains(formatFixtureText, "FAIL fixture_test.nomi :: fails\n"+wantFails+"\n") {
		t.Errorf("the json message is not the block the text report prints")
	}
	if got := *records[3].Message; got != "  line 21: division by zero" {
		t.Errorf("trap message = %q", got)
	}

	summary := records[len(records)-1]
	if summary.Type != "summary" || summary.Passed != 3 || summary.Failed != 3 || summary.Blocked != 0 {
		t.Errorf("summary = %+v", summary)
	}
}

// Colour is the text report's business; a record never carries escapes, even
// when colour is forced.
func TestTestCommand_JSONFormatHasNoColour(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir, _ := writeFormatFixture(t)
	cmd := exec.Command(nomiBin, "test", "fixture_test.nomi", "--format", "json")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+t.TempDir(), "NOMI_COLOR=always")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run()
	if strings.Contains(stdout.String(), "\x1b") {
		t.Fatalf("json output carries escape codes:\n%q", stdout.String())
	}
	records := parseJSONRecords(t, stdout.String())
	if got := *records[2].Message; !strings.HasPrefix(got, "  line 16: assertion failed\n    assert Iter.count(xs) == 4") {
		t.Fatalf("message = %q", got)
	}
	// The same run in text mode is coloured, so the check above is not vacuous.
	text := exec.Command(nomiBin, "test", "fixture_test.nomi")
	text.Dir = dir
	text.Env = cmd.Env
	out, _ := text.Output()
	if !strings.Contains(string(out), "\x1b[") {
		t.Fatalf("NOMI_COLOR=always did not colour the text report, so this test checks nothing")
	}
}

func TestTestCommand_JSONFormatWithLine(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir, path := writeFormatFixture(t)
	cases := []struct {
		line     string
		wantLine []int
	}{
		{"4", []int{4}},       // an attached `//!` test
		{"14", []int{14}},     // a test
		{"24", []int{25, 29}}, // a `tests` header runs its group
	}
	for _, c := range cases {
		stdout, _, _ := runNomiSplit(t, t.TempDir(), dir, "test", path, "--line", c.line, "--format", "json")
		records := parseJSONRecords(t, stdout)
		var got []int
		for _, rec := range records[:len(records)-1] {
			got = append(got, rec.Line)
		}
		if len(got) != len(c.wantLine) {
			t.Fatalf("--line %s: records at lines %v, want %v\n%s", c.line, got, c.wantLine, stdout)
		}
		for i := range got {
			if got[i] != c.wantLine[i] {
				t.Fatalf("--line %s: records at lines %v, want %v", c.line, got, c.wantLine)
			}
		}
		if records[len(records)-1].Type != "summary" {
			t.Fatalf("--line %s: last record is not the summary", c.line)
		}
	}
}

func TestTestCommand_JSONFormatFileFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	path := filepath.Join(dir, "broken_test.nomi")
	mustWrite(t, path, `test "bad" {
  assert undefined_name == 1
}
`)
	stdout, _, err := runNomiSplit(t, t.TempDir(), dir, "test", path, "--format", "json")
	if err == nil {
		t.Fatalf("expected a failing exit status")
	}
	records := parseJSONRecords(t, stdout)
	if len(records) != 2 {
		t.Fatalf("want a file record and a summary:\n%s", stdout)
	}
	rec := records[0]
	if rec.Type != "file" || rec.File != path || rec.Status != "failed" || rec.Message == nil || *rec.Message == "" {
		t.Fatalf("file record = %+v", rec)
	}
	if s := records[1]; s.Failed != 1 {
		t.Fatalf("summary = %+v", s)
	}
}

func TestTestCommand_JSONFormatRejectsUnknownFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir, _ := writeFormatFixture(t)
	stdout, stderr, err := runNomiSplit(t, t.TempDir(), dir, "test", "fixture_test.nomi", "--format", "xml")
	if err == nil || stdout != "" || !strings.Contains(stderr, `unknown test format "xml"`) {
		t.Fatalf("err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
}

// The FFI path runs the file in a generated wrapper process; its records and
// its test output must be split the same way.
func TestTestCommand_JSONFormatFFI(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, _ := stageEchoFixture(t)
	testPath := filepath.Join(projectRoot, "main_test.nomi")
	mustWrite(t, testPath, `import std/io

gopkg "echobinding" as ffi

fn echo_upper(s: String): String go ffi.EchoUpper

test "ffi passes" {
  io.print("printed by an ffi test")
  assert echo_upper("hello") == "HELLO"
}

test "ffi fails" {
  assert echo_upper("a") == "b"
}
`)
	stdout, stderr, err := runNomiSplit(t, cacheRoot, projectRoot, "test", testPath, "--format", "json")
	if err == nil {
		t.Fatalf("expected a failing exit status")
	}
	if !strings.Contains(stderr, "printed by an ffi test") {
		t.Fatalf("test output missing from stderr:\n%s", stderr)
	}
	records := parseJSONRecords(t, stdout)
	if len(records) != 3 {
		t.Fatalf("want two tests and a summary:\n%s", stdout)
	}
	if r := records[0]; r.Status != "passed" || r.Line != 7 {
		t.Errorf("first record = %+v", r)
	}
	if r := records[1]; r.Status != "failed" || r.Line != 12 || r.ErrorLine != 13 {
		t.Errorf("second record = %+v", r)
	}
	if s := records[2]; s.Type != "summary" || s.Passed != 1 || s.Failed != 1 {
		t.Errorf("summary = %+v", s)
	}
	if entries, _ := os.ReadDir(cacheRoot); len(entries) == 0 {
		t.Errorf("no wrapper cache entry, so the file never took the FFI path this test is about")
	}
}
