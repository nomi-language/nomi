package rt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
)

// `nomi test --format json`: the same outcomes as the text report, as JSON
// Lines, for tools (the Neovim neotest adapter) that match results to source
// positions.
//
// The records are keyed by file and first line, not by name. A test's name is
// display text — an attached test is named `impl / buffered //! test lines
// 58-60` — so a consumer that matched on names would be parsing wording the
// text report is free to change. The line is the same one `--line N` selects.
//
// The stream is one object per line, one per test, then one summary object:
//
//	{"type":"test","file":"/abs/a_test.nomi","line":9,"end_line":12,
//	 "name":"a_test.nomi :: adds","status":"passed","cases":1,"message":""}
//	{"type":"test",...,"status":"failed","cases":9,"failed_cases":1,"message":"line 16: ...","error_line":16}
//	{"type":"file","file":"/abs/b_test.nomi","name":"b_test.nomi","status":"failed","message":"..."}
//	{"type":"summary","passed":3,"failed":2,"blocked":0}
//
// `cases` is how many cases the test counts as: 1, or the rows of the
// `subtests` tables it ran (CaseCount); `failed_cases`, on a failed test, is
// how many of them failed. The summary's `passed` and `failed` count cases,
// so a test of 9 rows of which 1 failed adds 8 and 1; `blocked` counts
// tests, one for a blocked test whatever its tables hold.
//
// A "file" record is a failure that stopped a file's tests from running at all
// (a load or type error): the text report's `FAIL <file>: <error>` line.
//
// `message` is the block the text report prints under a FAIL line, without
// colour and without the trailing newline. `error_line` is present only when
// the failure names a line.
//
// `output` is what the test wrote while it ran (`io.print`, `dbg`, ...; see
// RunTestCases), byte for byte without colour, and is present only when the
// test wrote something. Nothing a test writes reaches the stream outside a
// record: `report` shows it under the record's ok, FAIL or BLOCKED line.
//
// `report` is what the text report prints for the record (`ok <name>`, the
// FAIL line and its block, the BLOCKED lines, a file's FAIL line), written by
// the text report's own functions (textReport). It is coloured exactly when a
// text run in the same environment would colour stdout, so `NOMI_COLOR=always`
// colours it under a pipe; an editor's output panel shows it as is.
//
// "blocked" is a test the VM could not run (see BlockedAt); its message is the
// blockers, as the text report's BLOCKED lines give them, and its
// `error_line` is the first blocker's line when that is in the test's file.

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
	// ErrorLine is, for a blocked test, the line in File of the first code
	// the compiler could not lower: the blocked record's `error_line`. 0 when
	// that code is in another file or has no position.
	ErrorLine int
}

// ResultAt is Result for a test whose position is known. The text report is
// identical to Result's; the JSON record carries the position.
func (r *TestReporter) ResultAt(loc TestLocation, name string, err error, output string, count CaseCount) {
	if r.format != TestFormatJSON {
		r.Result(name, err, output, count)
		return
	}
	count = count.resolve(err)
	r.tally(count)
	rec := testRecord{
		Type:    "test",
		File:    loc.File,
		Line:    loc.Line,
		EndLine: loc.EndLine,
		Name:    name,
		Status:  TestStatusPassed,
		Cases:   count.Cases,
		Output:  stripANSI(output),
	}
	if err != nil {
		rec.Status = TestStatusFailed
		rec.FailedCases = count.Failed
		rec.Message = failureMessage(err)
		rec.ErrorLine = TestErrorLine(err)
		rec.Report = textReport(func(w io.Writer) { writeFailure(w, name, err, output) })
	} else {
		rec.Report = textReport(func(w io.Writer) { writeOK(w, name, output, count) })
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
	Type    string `json:"type"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	EndLine int    `json:"end_line,omitempty"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	// Cases is how many cases the test counts as (CaseCount): 1, or the
	// rows of the `subtests` tables it ran. A "file" record has none.
	Cases int `json:"cases,omitempty"`
	// FailedCases is how many of a failed test's cases failed.
	FailedCases int    `json:"failed_cases,omitempty"`
	Message     string `json:"message"`
	ErrorLine   int    `json:"error_line,omitempty"`
	Output      string `json:"output,omitempty"`
	Report      string `json:"report"`
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
		Report:    textReport(func(w io.Writer) { WriteFailLine(w, name, err) }),
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
func failureMessage(err error) string {
	var b bytes.Buffer
	writeFailureBody(&b, err)
	return string(bytes.TrimRight(stripANSIBytes(b.Bytes()), "\n"))
}

// textReport is a record's `report`: what text mode prints for it, rendered
// by text mode's own writer. Text mode writes to stdout, so the buffer asks
// stdout whether to colour (colourAsStdout), and the bytes match a text run
// in the same environment, `NOMI_COLOR=always` included.
func textReport(write func(w io.Writer)) string {
	var b bytes.Buffer
	write(colourAsStdout{&b})
	return b.String()
}

// colourAsStdout is a buffer that ColorEnabledFor treats as os.Stdout, for
// renderers that decide colour by the writer they are handed (compiler
// diagnostics) rather than by stdout.
type colourAsStdout struct{ io.Writer }

func (colourAsStdout) colourTarget() io.Writer { return os.Stdout }

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
