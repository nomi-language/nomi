// Package stdcsv is std/csv/host: reading CSV records from a Reader with
// Go's encoding/csv, and writing one record as CSV text. std/csv is Nomi,
// lowered with each program, over it; internal/hostgen/stdtable's
// alsoImplements names this package as std/csv/host's.
//
// One reader reads every input: a csv.Reader over a lineinput.Input. Over
// the program's Reader it is a handle (rt.CsvRecords), and a stream is read
// a record at a time, a crossing each (Next). Over text held whole
// (lineinput.Text) it is Decode, which `csv.decode`, `decode_bytes` and
// `decode_rows` call: it reads every record in one crossing, reads no
// Reader and makes no handle, so its row is Pure, and it reports a stop
// as Stopped and Message do. The input drops a leading UTF-8 byte order
// mark and keeps the lines read since the oldest one an error may still
// name, so an error's column is in code points, as std/json reports it,
// rather than Go's byte column.
//
// Each crossing over a Reader sets the input's frame before it reads, since a Reader is
// read on the frame of the crossing that reads it. A read that unwinds goes
// through Go's parser as a panic, and the handle is not used again.
//
// Go's own error texts never reach Nomi: each failure is reported with
// std/csv's wording. Writing a record is Go's csv.Writer, one record per
// crossing, except a record of one empty field, which Go writes as an
// empty line a reader skips; it is written `""`.
package stdcsv
