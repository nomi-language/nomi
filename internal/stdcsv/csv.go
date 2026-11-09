package stdcsv

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/lineinput"
	"github.com/nomi-language/nomi/rt"
)

// records is one reader's state.
type records struct {
	in  *lineinput.Input
	csv *csv.Reader
	// invalid is why the options cannot be used, or "".
	invalid string
	// line is the line the last record read starts on.
	line int64
	// stop is how reading stopped: kind 0 while records remain or at a
	// clean end, else a failure.
	stop failure
	done bool
}

// failure is a stop: kind 1 a syntax error, 2 the Reader failed, 3 the
// options are invalid.
type failure struct {
	kind    int64
	line    int64
	column  int64
	message string
}

// Open is a reader of the CSV in r. delimiter and comment are code
// points, comment 0 for none; fields is the field count every record must
// have, 0 for the first record's and -1 for any.
func Open(r rt.Reader, delimiter, comment int64, trim, lazy bool, fields int64) rt.CsvRecords {
	in := lineinput.New(r)
	c := csv.NewReader(in)
	c.Comma = rune(delimiter)
	c.Comment = rune(comment)
	c.TrimLeadingSpace = trim
	c.LazyQuotes = lazy
	c.FieldsPerRecord = int(fields)
	rec := &records{in: in, csv: c}
	switch {
	case !validDelim(rune(delimiter)):
		rec.invalid = "the delimiter " + strconv.QuoteRune(rune(delimiter)) +
			" cannot be used: it may not be a quote, a line break or U+FFFD"
	case comment != 0 && !validDelim(rune(comment)):
		rec.invalid = "the comment character " + strconv.QuoteRune(rune(comment)) +
			" cannot be used: it may not be a quote, a line break or U+FFFD"
	case comment != 0 && comment == delimiter:
		rec.invalid = "the comment character and the delimiter are both " + strconv.QuoteRune(rune(delimiter))
	case fields < -1:
		rec.invalid = "the field count " + strconv.FormatInt(fields, 10) + " is negative"
	}
	return rt.NewCsvRecords(rec)
}

func validDelim(r rune) bool {
	return r > 0 && r != '"' && r != '\r' && r != '\n' && utf8.ValidRune(r) && r != utf8.RuneError
}

func of(h rt.CsvRecords) *records {
	rec, ok := h.Records().(*records)
	if !ok {
		rt.Trap("csv: the value is not a CSV reader")
	}
	return rec
}

// Next is the next record's fields, or an empty list when reading has
// stopped: at the end, or on a failure Stopped describes.
func Next(fr *rt.Frame, h rt.CsvRecords) *rt.List[string] {
	rec := of(h)
	if rec.done {
		return nil
	}
	if rec.invalid != "" {
		rec.done = true
		rec.stop = failure{kind: 3, message: rec.invalid}
		return nil
	}
	rec.in.Frame = fr
	defer func() { rec.in.Frame = nil }()
	fields, err := rec.csv.Read()
	if err == nil && len(fields) > 0 {
		line, _ := rec.csv.FieldPos(0)
		rec.line = int64(line)
		rec.in.Forget(line)
		for i, f := range fields {
			if !utf8.ValidString(f) {
				fl, fc := rec.csv.FieldPos(i)
				col := rec.in.Column(fl, fc)
				rec.done = true
				rec.stop = failure{kind: 1, line: int64(fl), column: int64(col) + int64(invalidAt(f)),
					message: "the text is not UTF-8"}
				return nil
			}
		}
		return rt.ListFromGo(fields, func(s string) string { return s })
	}
	rec.done = true
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	var failed rt.IOFailure
	if errors.As(err, &failed) {
		rec.stop = failure{kind: 2}
		return nil
	}
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		rec.stop = failure{kind: 1, line: int64(pe.Line), column: int64(rec.in.Column(pe.Line, pe.Column)),
			message: message(pe, len(fields), rec.csv.FieldsPerRecord)}
		return nil
	}
	rec.stop = failure{kind: 1, line: int64(rec.line), column: 1, message: "malformed CSV"}
	return nil
}

// invalidAt is the code point offset of the first byte of s that is not
// UTF-8.
func invalidAt(s string) int {
	n := 0
	for i, r := range s {
		if r == utf8.RuneError {
			if _, size := utf8.DecodeRuneInString(s[i:]); size == 1 {
				return n
			}
		}
		n++
	}
	return n
}

func message(pe *csv.ParseError, got, want int) string {
	switch {
	case errors.Is(pe.Err, csv.ErrBareQuote):
		return "a quote in a field that is not quoted; double it inside a quoted field, or allow lazy quotes"
	case errors.Is(pe.Err, csv.ErrQuote):
		if pe.StartLine != pe.Line {
			return "a quoted field that starts on line " + strconv.Itoa(pe.StartLine) +
				" is not closed, or has a quote in it that is not doubled"
		}
		return "a quoted field is not closed, or has a quote in it that is not doubled"
	case errors.Is(pe.Err, csv.ErrFieldCount):
		return "the record has " + strconv.Itoa(got) + " fields, not " + strconv.Itoa(want)
	}
	return "malformed CSV"
}

// Line is the line the last record Next returned starts on.
func Line(h rt.CsvRecords) int64 { return of(h).line }

// Stopped is why reading stopped, as [kind, line, column]: kind 0 at the end of the input or while
// it has not, 1 a syntax error at line and column, 2 the Reader failed
// (Failure has its error), 3 invalid options, with the message.
func Stopped(h rt.CsvRecords) *rt.List[int64] {
	s := of(h).stop
	return rt.ListFromGo([]int64{s.kind, s.line, s.column}, func(n int64) int64 { return n })
}

// Message is the message of the failure Stopped describes.
func Message(h rt.CsvRecords) string { return of(h).stop.message }

// Failure is the io.Error the Reader failed with, after Stopped said kind
// 2.
func Failure(h rt.CsvRecords) rt.IOError {
	rec := of(h)
	if rec.in.Failure == nil {
		rt.Trap("csv: the reader did not fail")
	}
	return *rec.in.Failure
}

// Encode is one record as a CSV line: each field quoted when it holds the
// delimiter, a quote, a line break or leading space (or is `\.`), its
// quotes doubled, and the line ended with "\n", or "\r\n" with crlf.
func Encode(fields *rt.List[string], delimiter int64, crlf bool) string {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Comma = rune(delimiter)
	w.UseCRLF = crlf
	var items []string
	for l := fields; l != nil; l = l.Tail {
		items = append(items, l.Head)
	}
	if !validDelim(rune(delimiter)) {
		rt.Trap("csv: the delimiter " + strconv.QuoteRune(rune(delimiter)) +
			" cannot be used: it may not be a quote, a line break or U+FFFD")
	}
	if len(items) == 1 && items[0] == "" {
		// encoding/csv writes a lone empty field as an empty line, which
		// a reader skips; quote it so it reads back as one empty field.
		if crlf {
			return "\"\"\r\n"
		}
		return "\"\"\n"
	}
	if err := w.Write(items); err != nil {
		rt.Trap("csv: " + err.Error())
	}
	w.Flush()
	return buf.String()
}
