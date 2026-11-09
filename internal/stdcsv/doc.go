// Package stdcsv is std/csv_host: reading CSV records from a Reader with
// Go's encoding/csv, and writing one record as CSV text. std/csv is Nomi,
// lowered with each program, over it; internal/hostgen/stdtable's
// alsoImplements names this package as std/csv_host's.
//
// A reader is a handle (rt.CsvRecords) over a csv.Reader that reads a
// lineinput.Input over the program's Reader, so a string, a file and a
// stream are one path: `csv.decode(text)` is `csv.records` over
// `Reader.from_text`, and a stream is read a record at a time. The input
// drops a leading UTF-8 byte order mark and keeps the lines read since the
// oldest one an error may still name, so an error's column is in code
// points, as std/json reports it, rather than Go's byte column.
//
// Each crossing sets the input's frame before it reads, since a Reader is
// read on the frame of the crossing that reads it. A read that unwinds goes
// through Go's parser as a panic, and the handle is not used again.
//
// Go's own error texts never reach Nomi: each failure is reported with
// std/csv's wording. Writing a record is Go's csv.Writer, one record per
// crossing, except a record of one empty field, which Go writes as an
// empty line a reader skips; it is written `""`.
package stdcsv
