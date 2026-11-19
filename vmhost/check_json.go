package vmhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/parser"
)

// `nomi check --format json` reports as JSON Lines on stdout, as `nomi test
// --format json` does: one record per diagnostic, one per checked file and
// a summary last. The records are for tools and agents, so every position
// is given as a line and byte column and as a byte offset, and a fix is
// text edits a tool applies as they are.
//
// An FFI program is checked in its wrapper process, which writes its
// diagnostics' records on its stdout (ReportCheck), and `nomi check` passes
// them through (CheckJSON.Write), so such a program's records are the ones
// any other program's are.

// checkJSONRecord is one line of `nomi check --format json`.
type checkJSONRecord struct {
	Type string `json:"type"`
	File string `json:"file,omitempty"`

	// A "diagnostic" record.
	checkJSONSpan
	Severity string             `json:"severity,omitempty"`
	Code     string             `json:"code,omitempty"`
	Args     []string           `json:"args,omitempty"`
	Message  string             `json:"message,omitempty"`
	Hints    []string           `json:"hints,omitempty"`
	Related  []checkJSONRelated `json:"related,omitempty"`
	Fix      *checkJSONFix      `json:"fix,omitempty"`

	// A "file" record: "ok" or "failed", and the importer a file checked
	// through one is ok through.
	Status  string `json:"status,omitempty"`
	Through string `json:"through,omitempty"`

	// The "summary" record.
	Files       *int `json:"files,omitempty"`
	Failed      *int `json:"failed,omitempty"`
	Diagnostics *int `json:"diagnostics,omitempty"`
}

// checkJSONSpan is a range in a file: 1-based lines, 1-based byte columns,
// the end one past the last byte, and the same two points as 0-based byte
// offsets into the file. The offsets are omitted when the file could not
// be read; the end is omitted for a point.
type checkJSONSpan struct {
	Line      int  `json:"line,omitempty"`
	Col       int  `json:"col,omitempty"`
	EndLine   int  `json:"end_line,omitempty"`
	EndCol    int  `json:"end_col,omitempty"`
	StartByte *int `json:"start_byte,omitempty"`
	EndByte   *int `json:"end_byte,omitempty"`
}

type checkJSONRelated struct {
	File string `json:"file"`
	checkJSONSpan
	Message string `json:"message"`
}

// checkJSONFix is a diagnostic's fix: its edits, in File when that is not
// the diagnostic's own file (a capability-not-allowed error's fix edits
// nomi.toml).
type checkJSONFix struct {
	Title string          `json:"title"`
	File  string          `json:"file,omitempty"`
	Edits []checkJSONEdit `json:"edits"`
}

// checkJSONEdit replaces the span's text with Text; an empty span inserts.
type checkJSONEdit struct {
	checkJSONSpan
	Text string `json:"text"`
}

// CheckJSON writes the records of one `nomi check --format json` run,
// each diagnostic once across the run.
type CheckJSON struct {
	w        io.Writer
	sources  map[string][]int // a file's line start offsets, nil when unreadable
	reported map[string]bool
	pending  []byte // the start of a record Write has not had the end of
	files    int
	failed   int
	diags    int
}

// NewCheckJSON is a run writing its records to w.
func NewCheckJSON(w io.Writer) *CheckJSON {
	return &CheckJSON{w: w, sources: map[string][]int{}, reported: map[string]bool{}}
}

// Failed is how many files the run's File records failed.
func (cj *CheckJSON) Failed() int { return cj.failed }

// File reports the check of the entry at path, which err failed: its
// diagnostics (Errors), then its "file" record.
func (cj *CheckJSON) File(path string, err error) {
	cj.files++
	if err == nil {
		cj.write(checkJSONRecord{Type: "file", File: path, Status: "ok"})
		return
	}
	cj.failed++
	cj.Errors(path, err)
	cj.write(checkJSONRecord{Type: "file", File: path, Status: "failed"})
}

// Errors writes err, the failure of the check of the entry at path, as
// diagnostic records: a record per call a capability list refuses, though
// its text gives the first of each name; a failed `go build` of the Go the
// program binds under ffirun.GoBuildFailedCode; and anything else that is
// not Diagnostics as its text. A wrapper's failure whose records it wrote
// itself (ffirun.ErrCheckReported) writes nothing more.
func (cj *CheckJSON) Errors(path string, err error) {
	if errors.Is(err, ffirun.ErrCheckReported) {
		return
	}
	var ds Diagnostics
	var refused *CapabilityError
	if errors.As(err, &refused) {
		ds = refused.Diagnostics
	} else if report, ok := ffirun.GoBuildReport(err); ok {
		ds = Diagnostics{{Path: path, Code: ffirun.GoBuildFailedCode, Message: report}}
	} else {
		errors.As(err, &ds)
	}
	if len(ds) == 0 {
		cj.diags++
		cj.write(checkJSONRecord{Type: "diagnostic", File: path, Severity: "error", Message: err.Error()})
		return
	}
	for _, d := range ds {
		cj.writeOnce(cj.diagnostic(path, d))
	}
}

// Warnings reports a program's warnings as diagnostics of severity
// "warning", or "hint" for one marked Hint (`nomi check --hints`), which
// fail no file.
func (cj *CheckJSON) Warnings(ds Diagnostics) {
	for _, d := range ds {
		r := cj.diagnostic(d.Path, d)
		r.Severity = "warning"
		if d.Hint {
			r.Severity = "hint"
		}
		cj.writeOnce(r)
	}
}

// Write passes through the records another process wrote for this run, an
// FFI wrapper's check (ReportCheck), as this run writes its own: each
// diagnostic once, and an error counted in the summary. A line that is no
// record is reported as an error's text.
func (cj *CheckJSON) Write(p []byte) (int, error) {
	cj.pending = append(cj.pending, p...)
	for {
		i := bytes.IndexByte(cj.pending, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := bytes.TrimSpace(cj.pending[:i])
		cj.pending = cj.pending[i+1:]
		if len(line) == 0 {
			continue
		}
		var r checkJSONRecord
		if err := json.Unmarshal(line, &r); err != nil || r.Type != "diagnostic" {
			r = checkJSONRecord{Type: "diagnostic", Severity: "error", Message: string(line)}
		}
		cj.writeOnce(r)
	}
}

// Through reports a file checked through the importer that loads it.
func (cj *CheckJSON) Through(path, importer string) {
	cj.files++
	cj.write(checkJSONRecord{Type: "file", File: path, Status: "ok", Through: importer})
}

// Summary writes the run's last record.
func (cj *CheckJSON) Summary() {
	files, failed, diags := cj.files, cj.failed, cj.diags
	cj.write(checkJSONRecord{Type: "summary", Files: &files, Failed: &failed, Diagnostics: &diags})
}

// ReportCheck is an FFI wrapper's `nomi check` of entry: Check with opts,
// reported as frontend.DiagnosticsFormatEnv selects. Under `nomi check
// --format json` each warning and error is a record on stdout (CheckJSON),
// which the `nomi` that started the wrapper passes through; otherwise the
// warnings and the failure are text on stderr, as `nomi check` prints them.
// It answers whether the check passed.
func ReportCheck(entry string, stdout, stderr io.Writer, opts ...Option) bool {
	if !frontend.JSONDiagnostics() {
		err := Check(entry, append(opts, WithWarnings(stderr))...)
		if err != nil {
			WriteFailure(stderr, err)
		}
		return err == nil
	}
	cj := NewCheckJSON(stdout)
	err := Check(entry, append(opts, WithWarningsTo(cj.Warnings))...)
	if err != nil {
		cj.Errors(entry, err)
	}
	return err == nil
}

// writeOnce writes r unless the run wrote the same record before, and
// counts an error it writes.
func (cj *CheckJSON) writeOnce(r checkJSONRecord) {
	line := cj.encode(r)
	if cj.reported[string(line)] {
		return
	}
	cj.reported[string(line)] = true
	if r.Severity == "error" {
		cj.diags++
	}
	_, _ = cj.w.Write(line)
}

func (cj *CheckJSON) write(r checkJSONRecord) {
	_, _ = cj.w.Write(cj.encode(r))
}

// encode is r as its line, newline included.
func (cj *CheckJSON) encode(r checkJSONRecord) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(r)
	return b.Bytes()
}

func (cj *CheckJSON) diagnostic(entry string, d Diagnostic) checkJSONRecord {
	file := d.Path
	if file == "" {
		file = entry
	}
	r := checkJSONRecord{
		Type: "diagnostic", File: file, Severity: "error", Code: d.Code, Args: d.Args,
		Message: d.Message, Hints: d.Hints,
		checkJSONSpan: cj.span(file, d.Line, d.Col, d.EndLine, d.EndCol),
	}
	for _, rel := range d.Related {
		rf := rel.Path
		if rf == "" {
			rf = file
		}
		r.Related = append(r.Related, checkJSONRelated{File: rf, Message: rel.Message,
			checkJSONSpan: cj.span(rf, rel.Line, rel.Col, rel.EndLine, rel.EndCol)})
	}
	if d.Fix != nil && len(d.Fix.Edits) > 0 {
		fix := &checkJSONFix{Title: d.Fix.Title, File: d.Fix.Path}
		in := file
		if d.Fix.Path != "" {
			in = d.Fix.Path
		}
		for _, e := range d.Fix.Edits {
			fix.Edits = append(fix.Edits, cj.edit(in, e))
		}
		r.Fix = fix
	}
	return r
}

func (cj *CheckJSON) edit(file string, e parser.Edit) checkJSONEdit {
	sp := cj.span(file, e.Start.Line, e.Start.Col, e.End.Line, e.End.Col)
	if sp.EndLine == 0 {
		// An insertion is an empty span: its end is its start.
		sp.EndLine, sp.EndCol, sp.EndByte = sp.Line, sp.Col, sp.StartByte
	}
	return checkJSONEdit{checkJSONSpan: sp, Text: e.Text}
}

// span is the range from line:col to endLine:endCol in file, with byte
// offsets when the file can be read. A zero endLine is a point.
func (cj *CheckJSON) span(file string, line, col, endLine, endCol int) checkJSONSpan {
	sp := checkJSONSpan{Line: line, Col: col}
	if endLine > 0 && (endLine != line || endCol != col) {
		sp.EndLine, sp.EndCol = endLine, endCol
	}
	starts := cj.lineStarts(file)
	offset := func(l, c int) *int {
		if starts == nil || l < 1 || l > len(starts) || c < 1 {
			return nil
		}
		o := starts[l-1] + c - 1
		return &o
	}
	if line > 0 {
		sp.StartByte = offset(line, max(col, 1))
	}
	if sp.EndLine > 0 {
		sp.EndByte = offset(sp.EndLine, sp.EndCol)
	}
	return sp
}

// lineStarts are the byte offsets at which file's lines start, read once;
// nil when the file cannot be read (an embedded std module).
func (cj *CheckJSON) lineStarts(file string) []int {
	if starts, ok := cj.sources[file]; ok {
		return starts
	}
	var starts []int
	if filepath.IsAbs(file) {
		if data, err := os.ReadFile(file); err == nil {
			starts = []int{0}
			for i, b := range data {
				if b == '\n' {
					starts = append(starts, i+1)
				}
			}
		}
	}
	cj.sources[file] = starts
	return starts
}
