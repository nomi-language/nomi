package vmhost

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/virtualproject"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmrunner"

	"github.com/nomi-language/nomi/rt"
)

// The test reporter and the failure renderer are rt's, so every runner —
// `nomi test`, the FFI wrapper, an embedder — prints the same lines.

// TestOptions selects cases: Line with LineSet is `nomi test --line N`.
type TestOptions = frontend.TestOptions

// TestReport is what Test reports into.
type TestReport = rt.TestReporter

// TestFormat is a report format: text or JSON lines.
type TestFormat = rt.TestFormat

// The report formats.
const (
	TestFormatText = rt.TestFormatText
	TestFormatJSON = rt.TestFormatJSON
)

// TestLocation locates a case in a JSON report.
type TestLocation = rt.TestLocation

// CaseCount is how many cases a test counts as in a report (CaseResult.Count).
type CaseCount = rt.CaseCount

// OKLine is the text after `ok ` on a passed test's report line.
func OKLine(name string, count CaseCount) string { return rt.OKLine(name, count) }

// NewTestReport is a text reporter writing to w.
func NewTestReport(w io.Writer) *TestReport { return rt.NewTestReporter(w) }

// NewTestReportFormat is a reporter writing to w in format.
func NewTestReportFormat(w io.Writer, format TestFormat) *TestReport {
	return rt.NewTestReporterFormat(w, format)
}

// ParseTestFormat reads a `--format` value.
func ParseTestFormat(s string) (TestFormat, error) { return rt.ParseTestFormat(s) }

// TestName is the label a case is reported under: its file, then its name.
func TestName(path, test string) string { return rt.TestName(path, test) }

// DisplayPath is how a report spells a path: relative to the working
// directory when under it.
func DisplayPath(path string) string { return rt.DisplayPath(path) }

// DefaultTestEnv sets NOMI_ENV to "test" unless the caller set it, and
// answers the restore.
func DefaultTestEnv() (func(), error) { return rt.DefaultTestEnv() }

// WriteFailLine writes `FAIL <name>: <err>`, with diagnostics below the name,
// one per line.
func WriteFailLine(w io.Writer, name string, err error) { rt.WriteFailLine(w, name, err) }

// Diagnostics is the error Load, LoadSource and Check answer for a program
// the front end rejects: one Diagnostic per error, each naming its file,
// line and column.
type Diagnostics = frontend.Diagnostics

// Diagnostic is one front-end error.
type Diagnostic = frontend.Diagnostic

// WriteFailure renders a program's failure as `nomi run` prints it: an
// assertion failure with its operands and stages, anything else as its text.
// It is vmrunner's, so a built binary prints the same failure.
func WriteFailure(w io.Writer, err error) {
	vmrunner.WriteFailure(w, err)
}

// FormatFailure is a program's failure as plain, uncoloured text with no
// trailing newline, for a host that shows it in its own UI (the tour).
func FormatFailure(err error) string {
	var exit *Exit
	if errors.As(err, &exit) {
		// The message the program prints, then the status it exits with,
		// which a page has no other way to show.
		status := fmt.Sprintf("exit status %d", exit.Code)
		if exit.Message == "" {
			return status
		}
		return exit.Message + "\n" + status
	}
	var assertionErr *rt.AssertionFailure
	if errors.As(err, &assertionErr) {
		return rt.FormatAssertionFailure(assertionErr)
	}
	var earlyErr *rt.EarlyReturnFailure
	if errors.As(err, &earlyErr) {
		return rt.FormatEarlyReturnFailure(earlyErr)
	}
	return err.Error()
}

func splitMultiFile(src string) (string, string, map[string]string, *analysis.Manifest, error) {
	project, err := virtualproject.FromMarkedSource(src)
	if err != nil {
		return "", "main", nil, nil, err
	}
	return project.EntrySource, project.EntryName, project.VirtualFiles, project.Manifest, nil
}

// TimedOut is the failure of a test case that did not finish within the
// limit SetTestTimeout gave: the limit, where the case was running when it
// was stopped (innermost first), and the line it is reported at.
type TimedOut = vm.TimedOut

// DefaultTestTimeout is how long `nomi test` lets each case run unless
// `--timeout` says otherwise.
const DefaultTestTimeout = 10 * time.Second

// TestTimeoutHint is the `help:` line `nomi test` ends a timed-out case's
// failure with.
const TestTimeoutHint = "`nomi test --timeout <duration>` sets how long a case may run " +
	"(10s unless given), and `--timeout 0` removes the limit"

// TestTimeoutEnv carries `nomi test --timeout` to an FFI project's test
// wrapper, which runs the cases in its own process (TestTimeoutFromEnv).
const TestTimeoutEnv = ffirun.TestTimeoutEnv

// TestTimeoutFromEnv is the limit TestTimeoutEnv names, or zero (no limit)
// when it is unset or not a duration.
func TestTimeoutFromEnv() time.Duration {
	d, err := time.ParseDuration(os.Getenv(TestTimeoutEnv))
	if err != nil || d < 0 {
		return 0
	}
	return d
}

// SetTestTimeout makes every test case the program runs from now on (Test,
// Cases) stop once it has run for d of wall-clock time, its group's clock,
// boot and setup included, and fail with a *TimedOut whose text names where
// it was running and ends with hint as a `help:` line when hint is not
// empty. Zero means no limit, which is the default. A program with a
// debugger (SetDebugger) runs with no limit, since a paused case must not
// time out.
func (p *Program) SetTestTimeout(d time.Duration, hint string) {
	p.testTimeout, p.testTimeoutHint = d, hint
}
