package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/vmhost"
)

// `nomi check --format json` reports as JSON Lines on stdout, as `nomi test
// --format json` does: one record per diagnostic, one per checked file and
// a summary last. The records are for tools and agents, so every position
// is given as a line and byte column and as a byte offset, and a fix is
// text edits a tool applies as they are.

// checkJSONRecord is one line of `nomi check --format json`.
type checkJSONRecord struct {
	Type string `json:"type"`
	File string `json:"file,omitempty"`

	// A "diagnostic" record.
	checkJSONSpan
	Severity string             `json:"severity,omitempty"`
	Code     string             `json:"code,omitempty"`
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

type checkJSONFix struct {
	Title string          `json:"title"`
	Edits []checkJSONEdit `json:"edits"`
}

// checkJSONEdit replaces the span's text with Text; an empty span inserts.
type checkJSONEdit struct {
	checkJSONSpan
	Text string `json:"text"`
}

// checkJSON writes the records of one `nomi check --format json` run.
type checkJSON struct {
	enc      *json.Encoder
	sources  map[string][]int // a file's line start offsets, nil when unreadable
	reported map[string]bool
	files    int
	failed   int
	diags    int
}

func newCheckJSON(w io.Writer) *checkJSON {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &checkJSON{enc: enc, sources: map[string][]int{}, reported: map[string]bool{}}
}

// file reports the check of the entry at path: its diagnostics, each once
// across the run, then its "file" record.
func (cj *checkJSON) file(path string, err error) {
	cj.files++
	if err == nil {
		cj.write(checkJSONRecord{Type: "file", File: path, Status: "ok"})
		return
	}
	cj.failed++
	var ds vmhost.Diagnostics
	if !errors.As(err, &ds) || len(ds) == 0 {
		cj.diags++
		cj.write(checkJSONRecord{Type: "diagnostic", File: path, Severity: "error", Message: err.Error()})
	} else {
		for _, d := range ds {
			key := d.String()
			if cj.reported[key] {
				continue
			}
			cj.reported[key] = true
			cj.diags++
			cj.write(cj.diagnostic(path, d))
		}
	}
	cj.write(checkJSONRecord{Type: "file", File: path, Status: "failed"})
}

// warnings reports a program's warnings, each once across the run, as
// diagnostics of severity "warning", or "hint" for one marked Hint (`nomi
// check --hints`), which fail no file.
func (cj *checkJSON) warnings(ds vmhost.Diagnostics) {
	for _, d := range ds {
		key := d.String()
		if cj.reported[key] {
			continue
		}
		cj.reported[key] = true
		r := cj.diagnostic(d.Path, d)
		r.Severity = "warning"
		if d.Hint {
			r.Severity = "hint"
		}
		cj.write(r)
	}
}

// through reports a file checked through the importer that loads it.
func (cj *checkJSON) through(path, importer string) {
	cj.files++
	cj.write(checkJSONRecord{Type: "file", File: path, Status: "ok", Through: importer})
}

func (cj *checkJSON) summary() {
	files, failed, diags := cj.files, cj.failed, cj.diags
	cj.write(checkJSONRecord{Type: "summary", Files: &files, Failed: &failed, Diagnostics: &diags})
}

func (cj *checkJSON) write(r checkJSONRecord) {
	_ = cj.enc.Encode(r)
}

func (cj *checkJSON) diagnostic(entry string, d vmhost.Diagnostic) checkJSONRecord {
	file := d.Path
	if file == "" {
		file = entry
	}
	r := checkJSONRecord{
		Type: "diagnostic", File: file, Severity: "error", Code: d.Code,
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
		fix := &checkJSONFix{Title: d.Fix.Title}
		for _, e := range d.Fix.Edits {
			fix.Edits = append(fix.Edits, cj.edit(file, e))
		}
		r.Fix = fix
	}
	return r
}

func (cj *checkJSON) edit(file string, e parser.Edit) checkJSONEdit {
	sp := cj.span(file, e.Start.Line, e.Start.Col, e.End.Line, e.End.Col)
	if sp.EndLine == 0 {
		// An insertion is an empty span: its end is its start.
		sp.EndLine, sp.EndCol, sp.EndByte = sp.Line, sp.Col, sp.StartByte
	}
	return checkJSONEdit{checkJSONSpan: sp, Text: e.Text}
}

// span is the range from line:col to endLine:endCol in file, with byte
// offsets when the file can be read. A zero endLine is a point.
func (cj *checkJSON) span(file string, line, col, endLine, endCol int) checkJSONSpan {
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
func (cj *checkJSON) lineStarts(file string) []int {
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

// runCheckJSON is runCheck in json mode, writing to w.
func runCheckJSON(w io.Writer, path string) error {
	cj := newCheckJSON(w)
	files, through, _, err := discoverCheckFiles(path)
	if err != nil {
		abs, _ := filepath.Abs(path)
		cj.file(abs, pathError(path, err))
		cj.summary()
		return errCheckFailed
	}
	failed := map[string]bool{}
	for _, file := range files {
		err := checkFile(file, vmhost.WithWarningsTo(cj.warnings))
		failed[file] = err != nil
		cj.file(file, err)
	}
	helpers := make([]string, 0, len(through))
	for helper := range through {
		helpers = append(helpers, helper)
	}
	sort.Strings(helpers)
	for _, helper := range helpers {
		ok := true
		for _, importer := range through[helper] {
			ok = ok && !failed[importer]
		}
		if ok {
			cj.through(helper, through[helper][0])
		}
	}
	cj.summary()
	if cj.failed > 0 {
		return errCheckFailed
	}
	return nil
}
