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
fn double(x: Int) -> Int {
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

group "group" {
  test "inner ok" {
    assert True
  }

  test "inner bad" {
    assert False
  }
}
`

// The text report for formatFixture. A test's own output is indented under
// its line.
const formatFixtureText = `ok fixture_test.nomi :: double //! test line 4
ok fixture_test.nomi :: passes
  hello from a test
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
	Report    *string `json:"report"`
	Output    string  `json:"output"`
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
	if stderr != "" {
		t.Fatalf("a test's own output belongs in its record, not on stderr; stderr:\n%q", stderr)
	}
	records := parseJSONRecords(t, stdout)
	if got := records[1].Output; got != "hello from a test\n" {
		t.Errorf("the passing test's output = %q", got)
	}

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

// Colour belongs to `report` alone: `message` never carries escapes, even
// when colour is forced, and no raw escape byte reaches stdout.
func TestTestCommand_JSONFormatMessageHasNoColour(t *testing.T) {
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
		t.Fatalf("json output carries raw escape bytes:\n%q", stdout.String())
	}
	records := parseJSONRecords(t, stdout.String())
	for i, rec := range records[:len(records)-1] {
		if rec.Message == nil || strings.Contains(*rec.Message, "\x1b") {
			t.Errorf("record %d: message %v carries escapes or is missing", i, rec.Message)
		}
	}
	if got := *records[2].Message; !strings.HasPrefix(got, "  line 16: assertion failed\n    assert Iter.count(xs) == 4") {
		t.Fatalf("message = %q", got)
	}
	if rep := records[2].Report; rep == nil || !strings.Contains(*rep, "\x1b[") {
		t.Fatalf("NOMI_COLOR=always left the report plain, so this test checks nothing: %v", rep)
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

const reportFixture = `test "ok" {
    assert 1 == 1
}

test "bad" {
    x = 2
    assert x == 3
}
`

// runReportFixture runs `nomi test a_test.nomi` over reportFixture with env
// added to a colour-neutral environment, and returns stdout.
func runReportFixture(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(nomiBin, append([]string{"test", "a_test.nomi"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+t.TempDir(), "NO_COLOR=", "CLICOLOR_FORCE=", "NOMI_COLOR=")
	cmd.Env = append(cmd.Env, env...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run()
	return stdout.String()
}

// Each record's `report` is the text report's bytes for that record: coloured
// and highlighted when colour is forced, plain otherwise. The reports plus the
// summary line are the text run's output, so the two cannot drift.
func TestTestCommand_JSONFormatReport(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a_test.nomi"), reportFixture)

	cases := []struct {
		name    string
		env     []string
		ok, bad string
	}{
		{
			name: "forced",
			env:  []string{"NOMI_COLOR=always"},
			ok:   "\x1b[32mok\x1b[0m a_test.nomi :: ok\n",
			bad: "\x1b[1m\x1b[31mFAIL\x1b[0m a_test.nomi :: bad\n" +
				"  \x1b[2mline 7: assertion failed\x1b[0m\n" +
				"    \x1b[35massert\x1b[0m \x1b[34mx\x1b[0m == \x1b[33m3\x1b[0m\n" +
				"    \x1b[2mvalues:\x1b[0m\n" +
				"      \x1b[34mx\x1b[0m\n" +
				"        \x1b[2m=\x1b[0m \x1b[33m2\x1b[0m\n",
		},
		{
			name: "off",
			env:  []string{"NOMI_COLOR=never"},
			ok:   "ok a_test.nomi :: ok\n",
			bad: "FAIL a_test.nomi :: bad\n" +
				"  line 7: assertion failed\n" +
				"    assert x == 3\n" +
				"    values:\n" +
				"      x\n" +
				"        = 2\n",
		},
	}
	const plainMessage = "  line 7: assertion failed\n    assert x == 3\n    values:\n      x\n        = 2"
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			records := parseJSONRecords(t, runReportFixture(t, dir, c.env, "--format", "json"))
			if len(records) != 3 {
				t.Fatalf("want two tests and a summary, got %+v", records)
			}
			for i, want := range []string{c.ok, c.bad} {
				if got := records[i].Report; got == nil || *got != want {
					t.Errorf("record %d report = %q, want %q", i, deref(got), want)
				}
			}
			if got := deref(records[1].Message); got != plainMessage {
				t.Errorf("message = %q, want %q", got, plainMessage)
			}
			text := runReportFixture(t, dir, c.env)
			if !strings.HasPrefix(text, c.ok+c.bad) || strings.Count(text, "\n") != strings.Count(c.ok+c.bad, "\n")+1 {
				t.Errorf("the reports are not the text run's lines.\ntext:\n%q\nreports:\n%q", text, c.ok+c.bad)
			}
		})
	}
}

// A file record's report is the text report's `FAIL <file>` block: here the
// rendered compiler diagnostic, coloured when colour is forced.
func TestTestCommand_JSONFormatFileReport(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a_test.nomi"), "test \"bad\" {\n    assert undefined_name == 1\n}\n")
	for _, c := range []struct {
		env    string
		colour bool
	}{{"NOMI_COLOR=always", true}, {"NOMI_COLOR=never", false}} {
		records := parseJSONRecords(t, runReportFixture(t, dir, []string{c.env}, "--format", "json"))
		rec := records[0]
		if rec.Type != "file" || rec.Report == nil {
			t.Fatalf("%s: record = %+v", c.env, rec)
		}
		if strings.Contains(*rec.Report, "\x1b[") != c.colour {
			t.Errorf("%s: report colour = %v, want %v:\n%q", c.env, !c.colour, c.colour, *rec.Report)
		}
		if strings.Contains(deref(rec.Message), "\x1b") {
			t.Errorf("%s: message carries escapes: %q", c.env, deref(rec.Message))
		}
		text := runReportFixture(t, dir, []string{c.env})
		if !strings.HasPrefix(text, *rec.Report) {
			t.Errorf("%s: the report is not the text run's block.\ntext:\n%q\nreport:\n%q", c.env, text, *rec.Report)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
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
		{"24", []int{25, 29}}, // a `group` header runs its group
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

// A `--line` that selects no test is an error, not an empty pass: an editor
// rerunning a line the test has since moved away from must not report ok.
// Line 6 is the body of `fn double`, which no test, attached prompt or group
// header covers.
func TestTestCommand_LineWithNoTestFails(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir, path := writeFormatFixture(t)
	const want = "no test at fixture_test.nomi:6\nhelp: the nearest test starts on line 4"

	stdout, stderr, err := runNomiSplit(t, t.TempDir(), dir, "test", path, "--line", "6")
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("exit = %v, want status 1\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if stdout != "" || stderr != "nomi test: "+want+"\n" {
		t.Fatalf("stdout:\n%s\nstderr:\n%q\nwant stderr %q", stdout, stderr, "nomi test: "+want+"\n")
	}

	stdout, stderr, err = runNomiSplit(t, t.TempDir(), dir, "test", path, "--line", "6", "--format", "json")
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("json: exit = %v, want status 1\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	records := parseJSONRecords(t, stdout)
	if len(records) != 2 {
		t.Fatalf("json: want a file record and the summary, got:\n%s", stdout)
	}
	file := records[0]
	if file.Type != "file" || file.Status != "failed" || file.File != path || file.Message == nil || *file.Message != want {
		t.Fatalf("json: file record = %+v, want a failed file record with message %q", file, want)
	}
	if sum := records[1]; sum.Type != "summary" || sum.Passed != 0 || sum.Failed != 1 {
		t.Fatalf("json: summary = %+v", sum)
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

fn echo_upper(s: String) -> String go ffi.EchoUpper

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
	if strings.Contains(stderr, "printed by an ffi test") {
		t.Fatalf("test output reached stderr instead of its record:\n%s", stderr)
	}
	records := parseJSONRecords(t, stdout)
	if len(records) != 3 {
		t.Fatalf("want two tests and a summary:\n%s", stdout)
	}
	if got := records[0].Output; got != "printed by an ffi test\n" {
		t.Errorf("the ffi test's output = %q", got)
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

const outputFixture = `import std/io

test "passes" {
    dbg 5
    io.print("printed")
    assert True
}

test "fails" {
    dbg 6
    assert False
}
`

// What a test writes, `dbg` and `io.print` alike, is reported under its own
// line: indented in text mode, and in json mode in the record's `output`
// (plain) and `report` (coloured as text mode is). Nothing reaches stdout
// outside a record, or stderr.
func TestTestCommand_OutputUnderItsTest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a_test.nomi"), outputFixture)
	cases := []struct {
		name          string
		env           []string
		ok, bad, last string
	}{
		{
			name: "forced",
			env:  []string{"NOMI_COLOR=always"},
			ok: "\x1b[32mok\x1b[0m a_test.nomi :: passes\n" +
				"  \x1b[36mdbg\x1b[0m \x1b[2mline 4:\x1b[0m \x1b[33m5\x1b[0m = \x1b[33m5\x1b[0m\n" +
				"  printed\n",
			bad: "\x1b[1m\x1b[31mFAIL\x1b[0m a_test.nomi :: fails\n" +
				"  \x1b[36mdbg\x1b[0m \x1b[2mline 10:\x1b[0m \x1b[33m6\x1b[0m = \x1b[33m6\x1b[0m\n" +
				"  \x1b[2mline 11: assertion failed\x1b[0m\n" +
				"    \x1b[35massert\x1b[0m \x1b[36mFalse\x1b[0m\n",
			last: "test result: \x1b[1m\x1b[31mFAILED\x1b[0m. \x1b[32m1\x1b[0m passed, \x1b[1m\x1b[31m1\x1b[0m failed\n",
		},
		{
			name: "off",
			env:  []string{"NOMI_COLOR=never"},
			ok: "ok a_test.nomi :: passes\n" +
				"  dbg line 4: 5 = 5\n" +
				"  printed\n",
			bad: "FAIL a_test.nomi :: fails\n" +
				"  dbg line 10: 6 = 6\n" +
				"  line 11: assertion failed\n" +
				"    assert False\n",
			last: "test result: FAILED. 1 passed, 1 failed\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, stderr := runOutputFixture(t, dir, c.env)
			if want := c.ok + c.bad + c.last; text != want || stderr != "" {
				t.Errorf("text report:\n%q\nwant:\n%q\nstderr: %q", text, want, stderr)
			}
			stdout, stderr := runOutputFixture(t, dir, c.env, "--format", "json")
			if stderr != "" {
				t.Errorf("json mode wrote to stderr: %q", stderr)
			}
			records := parseJSONRecords(t, stdout)
			if len(records) != 3 {
				t.Fatalf("want two tests and a summary, got %+v", records)
			}
			for i, want := range []struct{ report, output string }{
				{c.ok, "dbg line 4: 5 = 5\nprinted\n"},
				{c.bad, "dbg line 10: 6 = 6\n"},
			} {
				if got := deref(records[i].Report); got != want.report {
					t.Errorf("record %d report = %q, want %q", i, got, want.report)
				}
				if got := records[i].Output; got != want.output {
					t.Errorf("record %d output = %q, want %q", i, got, want.output)
				}
			}
		})
	}
}

// runOutputFixture runs `nomi test a_test.nomi` with env added to a
// colour-neutral environment, and returns stdout and stderr.
func runOutputFixture(t *testing.T, dir string, env []string, args ...string) (string, string) {
	t.Helper()
	cmd := exec.Command(nomiBin, append([]string{"test", "a_test.nomi"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+t.TempDir(), "NO_COLOR=", "CLICOLOR_FORCE=", "NOMI_COLOR=")
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stdout.String(), stderr.String()
}
