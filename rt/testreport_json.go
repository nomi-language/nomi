package rt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
)

// `nomi test --format json`: the same outcomes as the text report, as JSON
// Lines, for tools (the Neovim neotest adapter) that match results to source
// positions.
//
// The records are keyed by FILE AND FIRST LINE, not by name. A test's name is
// display text — an attached test is named `impl / buffered //! test lines
// 58-60` — so a consumer that matched on names would be parsing wording the
// text report is free to change. The line is the same one `--line N` selects.
//
// The stream is one object per line, one per test, then one summary object:
//
//	{"type":"test","file":"/abs/a_test.nomi","line":9,"end_line":12,
//	 "name":"a_test.nomi :: adds","status":"passed","message":""}
//	{"type":"test",...,"status":"failed","message":"line 16: assertion failed\n...","error_line":16}
//	{"type":"file","file":"/abs/b_test.nomi","name":"b_test.nomi","status":"failed","message":"..."}
//	{"type":"summary","passed":3,"failed":2,"blocked":0}
//
// A "file" record is a failure that stopped a file's tests from running at all
// (a load or type error): the text report's `FAIL <file>: <error>` line.
//
// `message` is the block the text report prints under a FAIL line, without
// colour and without the trailing newline. `error_line` is present only when
// the failure names a line.
//
// "blocked" is a test the VM could not run (see BlockedAt); its message is the
// reasons, one per line, as the text report's BLOCKED lines give them.

// TestFormat selects how a TestReporter writes.
type TestFormat int

const (
	TestFormatText TestFormat = iota
	TestFormatJSON
)

// ParseTestFormat reads a `--format` value.
func ParseTestFormat(s string) (TestFormat, error) {
	switch s {
	case "text":
		return TestFormatText, nil
	case "json":
		return TestFormatJSON, nil
	}
	return TestFormatText, fmt.Errorf("unknown test format %q (want text or json)", s)
}

func (f TestFormat) String() string {
	if f == TestFormatJSON {
		return "json"
	}
	return "text"
}

// The status values a test record carries.
const (
	TestStatusPassed  = "passed"
	TestStatusFailed  = "failed"
	TestStatusBlocked = "blocked"
)

// NewTestReporterFormat is NewTestReporter writing in the given format.
func NewTestReporterFormat(w io.Writer, format TestFormat) *TestReporter {
	return &TestReporter{w: w, format: format}
}

// Format reports which format this reporter writes.
func (r *TestReporter) Format() TestFormat { return r.format }

// TestLocation is where a test is declared: the absolute file and the first
// and last lines of the declaration. A zero Line means the position is
// unknown, and the record omits it.
type TestLocation struct {
	File    string
	Line    int
	EndLine int
}

// ResultAt is Result for a test whose position is known. The text report is
// identical to Result's; the JSON record carries the position.
func (r *TestReporter) ResultAt(loc TestLocation, name string, err error) {
	if r.format != TestFormatJSON {
		r.Result(name, err)
		return
	}
	rec := testRecord{
		Type:    "test",
		File:    loc.File,
		Line:    loc.Line,
		EndLine: loc.EndLine,
		Name:    name,
		Status:  TestStatusPassed,
	}
	if err != nil {
		r.failed++
		rec.Status = TestStatusFailed
		rec.Message = r.failureMessage(err)
		rec.ErrorLine = TestErrorLine(err)
	} else {
		r.passed++
	}
	r.writeRecord(rec)
}

// FailFile is Fail for a file named by its absolute path: the text report
// shows the display path, as Fail(DisplayPath(file), err) does, and the JSON
// record carries the absolute one.
func (r *TestReporter) FailFile(file string, err error) {
	if r.format != TestFormatJSON {
		r.Fail(DisplayPath(file), err)
		return
	}
	r.failed++
	r.writeFileFailure(file, DisplayPath(file), err)
}

type testRecord struct {
	Type      string `json:"type"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	ErrorLine int    `json:"error_line,omitempty"`
}

type testSummaryRecord struct {
	Type    string `json:"type"`
	Passed  int    `json:"passed"`
	Failed  int    `json:"failed"`
	Blocked int    `json:"blocked"`
}

func (r *TestReporter) writeFileFailure(file, name string, err error) {
	r.writeRecord(testRecord{
		Type:      "file",
		File:      file,
		Name:      name,
		Status:    TestStatusFailed,
		Message:   stripANSI(fmt.Sprint(err)),
		ErrorLine: TestErrorLine(err),
	})
}

func (r *TestReporter) writeSummary() {
	r.writeRecord(testSummaryRecord{Type: "summary", Passed: r.passed, Failed: r.failed, Blocked: r.blocked})
}

func (r *TestReporter) writeRecord(v any) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err) // the records are plain strings and ints
	}
	_, _ = r.w.Write(b.Bytes())
}

// failureMessage is the block the text report prints under a FAIL line —
// rendered by the same function, so the two cannot disagree — with its colour
// removed and its trailing newline trimmed.
func (r *TestReporter) failureMessage(err error) string {
	var b bytes.Buffer
	(&TestReporter{w: &b}).failureBody(err)
	return string(bytes.TrimRight(stripANSIBytes(b.Bytes()), "\n"))
}

// TestErrorLine is the source line a test failure points at, or 0.
func TestErrorLine(err error) int {
	var assertionErr *AssertionFailure
	if errors.As(err, &assertionErr) && assertionErr.Line > 0 {
		return assertionErr.Line
	}
	var earlyErr *EarlyReturnFailure
	if errors.As(err, &earlyErr) && earlyErr.Line > 0 {
		return earlyErr.Line
	}
	var lined interface{ SourceLine() int }
	if errors.As(err, &lined) && lined.SourceLine() > 0 {
		return lined.SourceLine()
	}
	// A runtime fault's text leads with its line: "line 21: division by zero".
	if m := leadingLine.FindStringSubmatch(err.Error()); m != nil {
		if n, convErr := strconv.Atoi(m[1]); convErr == nil {
			return n
		}
	}
	return 0
}

var (
	leadingLine = regexp.MustCompile(`^line (\d+):`)
	ansiSGR     = regexp.MustCompile("\x1b\\[[0-9;]*m")
)

func stripANSI(s string) string { return ansiSGR.ReplaceAllString(s, "") }

func stripANSIBytes(b []byte) []byte { return ansiSGR.ReplaceAll(b, nil) }
