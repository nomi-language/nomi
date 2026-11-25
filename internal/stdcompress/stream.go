package stdcompress

import (
	"bytes"
	"errors"
	"io"

	"github.com/nomi-language/nomi/rt"
)

// chunkSize is how much a stream reads of its source, or decodes, at once.
const chunkSize = 32 << 10

// sourceInput is a Reader as an io.Reader, read on the frame of the read
// being answered (fr, set before each), keeping the first two bytes for
// sniff.
type sourceInput struct {
	fr    *rt.Frame
	r     rt.Reader
	start []byte
}

func (in *sourceInput) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	chunk := rt.ReaderRead(in.fr, in.r, rt.SmallInt(int64(len(p))))
	if chunk.Tag != rt.TagOk {
		return 0, rt.IOFailure{E: chunk.Err}
	}
	if chunk.Ok.Tag != rt.TagSome {
		return 0, io.EOF
	}
	n := copy(p, chunk.Ok.Some)
	if len(in.start) < 2 {
		in.start = append(in.start, p[:min(n, 2-len(in.start))]...)
	}
	return n, nil
}

// sinkOutput is a Writer as an io.Writer, written on the frame of the
// operation being answered.
type sinkOutput struct {
	fr *rt.Frame
	w  rt.Writer
}

func (o *sinkOutput) Write(p []byte) (int, error) {
	if r := rt.WriterWrite(o.fr, o.w, rt.Bytes(p)); r.Tag != rt.TagOk {
		return 0, rt.IOFailure{E: r.Err}
	}
	return len(p), nil
}

// ioFailure is the io.Error for err, a failure of operation on the stream
// named name: the wrapped stream's own, Corrupt or Truncated for a read of
// a stream that is not valid, else as rt.IOErrorOf sorts it.
func ioFailure(err error, operation, name string, format int64, start []byte) rt.IOError {
	var f rt.IOFailure
	if errors.As(err, &f) {
		return f.E
	}
	if operation != "read" {
		return rt.IOErrorOf(operation, name, err)
	}
	kind, found, reason := failure(err, format, start)
	if kind == kindTruncated {
		return rt.IOError{Tag: rt.TagIOTruncated, Path: name}
	}
	if kind == kindWrongFormat {
		reason = "the stream is " + formatNames[found] + " data, not " + formatNames[formatOf(format)]
	}
	return rt.IOError{Tag: rt.TagIOCorrupt, Path: name, Reason: reason}
}

// formatNames are the formats as an error's reason names them.
var formatNames = [...]string{formatGzip: "gzip", formatZlib: "zlib", formatDeflate: "raw deflate"}

// interrupted is the error a stream answers after a read of it was stopped
// part-way, which leaves its decoder or encoder part-way too.
func interrupted(name string) rt.IOError {
	return rt.IOError{Tag: rt.TagIOOther, Operation: "read", Path: name, Code: "EINTR"}
}

// decoding is the state of a reader that decompresses its source.
type decoding struct {
	format int64
	name   string
	dict   rt.Option[rt.Bytes]
	src    *sourceInput
	dec    io.Reader
	buf    []byte
	failed *rt.IOError
	done   bool
}

// Reader is `std::compress::host.reader`: a Reader of what source's bytes,
// compressed in format, decompress to, with dict as zlib's or raw
// deflate's preset dictionary. Closing it leaves source open.
func Reader(format int64, source rt.Reader, dict rt.Option[rt.Bytes]) rt.Reader {
	format = formatOf(format)
	d := &decoding{format: format, name: streamNames[format], dict: dict, src: &sourceInput{r: source}}
	return rt.NewPullReader(d.name, d.pull, nil)
}

func (d *decoding) pull(fr *rt.Frame) (res rt.Result[rt.Option[rt.Bytes], rt.IOError]) {
	if d.failed != nil {
		return rt.Err[rt.Option[rt.Bytes]](*d.failed)
	}
	if d.done {
		return rt.Ok[rt.Option[rt.Bytes], rt.IOError](rt.None[rt.Bytes]())
	}
	d.src.fr = fr
	finished := false
	defer func() {
		if !finished {
			e := interrupted(d.name)
			d.failed = &e
		}
	}()
	if d.dec == nil {
		dec, err := newDecoder(d.src, d.format, d.dict)
		if err != nil {
			finished = true
			return d.fail(err)
		}
		d.dec, d.buf = dec, make([]byte, chunkSize)
	}
	n, err := d.dec.Read(d.buf)
	finished = true
	if n > 0 {
		// A decoder answers its error again on the next read.
		return rt.Ok[rt.Option[rt.Bytes], rt.IOError](rt.Some(rt.Bytes(d.buf[:n])))
	}
	if errors.Is(err, io.EOF) {
		d.done = true
		return rt.Ok[rt.Option[rt.Bytes], rt.IOError](rt.None[rt.Bytes]())
	}
	if err != nil {
		return d.fail(err)
	}
	return rt.Ok[rt.Option[rt.Bytes], rt.IOError](rt.Some(rt.Bytes("")))
}

func (d *decoding) fail(err error) rt.Result[rt.Option[rt.Bytes], rt.IOError] {
	e := ioFailure(err, "read", d.name, d.format, d.src.start)
	var f rt.IOFailure
	if !errors.As(err, &f) {
		// A stream that is not valid stays so; the source's own failure
		// may pass, and the decoder answers it again in any case.
		d.failed = &e
	}
	return rt.Err[rt.Option[rt.Bytes]](e)
}

// Writer is `std::compress::host.writer`: a Writer that compresses what is
// written to it in format at level into sink. Its flush flushes the
// compressor and then sink; closing it writes the format's end into sink
// and leaves sink open.
func Writer(
	format int64, sink rt.Writer, level int64,
	name, comment string, modified, os rt.Int,
	dict rt.Option[rt.Bytes],
) rt.Writer {
	format = formatOf(format)
	out := &sinkOutput{w: sink}
	enc := newEncoder(out, format, level, headerOf(name, comment, modified, os), dict)
	streamName := streamNames[format]
	result := func(operation string, err error) rt.Result[rt.Unit, rt.IOError] {
		if err != nil {
			return rt.Err[rt.Unit](ioFailure(err, operation, streamName, format, nil))
		}
		return rt.Ok[rt.Unit, rt.IOError](rt.Unit{})
	}
	write := func(fr *rt.Frame, chunk rt.Bytes) rt.Result[rt.Unit, rt.IOError] {
		out.fr = fr
		_, err := io.WriteString(enc, string(chunk))
		return result("write", err)
	}
	flush := func(fr *rt.Frame) rt.Result[rt.Unit, rt.IOError] {
		out.fr = fr
		if err := enc.Flush(); err != nil {
			return result("write", err)
		}
		return rt.WriterFlush(fr, sink)
	}
	closeFn := func(fr *rt.Frame) rt.Result[rt.Unit, rt.IOError] {
		out.fr = fr
		return result("close", enc.Close())
	}
	return rt.NewPushWriter(streamName, write, flush, closeFn, false)
}

// encoding is the state of a reader that compresses its source.
type encoding struct {
	src  rt.Reader
	enc  encoder
	out  bytes.Buffer
	name string
	done bool
}

// EncodingReader is `std::compress::host.encoding_reader`: a Reader of
// source's bytes compressed in format at level. Each read of source is
// flushed through the compressor, so a source that arrives slowly is
// passed on as it arrives. Closing it leaves source open.
func EncodingReader(
	format int64, source rt.Reader, level int64,
	name, comment string, modified, os rt.Int,
	dict rt.Option[rt.Bytes],
) rt.Reader {
	format = formatOf(format)
	e := &encoding{src: source, name: streamNames[format]}
	e.enc = newEncoder(&e.out, format, level, headerOf(name, comment, modified, os), dict)
	return rt.NewPullReader(e.name, e.pull, nil)
}

func (e *encoding) pull(fr *rt.Frame) rt.Result[rt.Option[rt.Bytes], rt.IOError] {
	for e.out.Len() == 0 {
		if e.done {
			return rt.Ok[rt.Option[rt.Bytes], rt.IOError](rt.None[rt.Bytes]())
		}
		chunk := rt.ReaderRead(fr, e.src, rt.SmallInt(chunkSize))
		if chunk.Tag != rt.TagOk {
			return rt.Err[rt.Option[rt.Bytes]](chunk.Err)
		}
		// Writes into a bytes.Buffer do not fail.
		if chunk.Ok.Tag != rt.TagSome {
			_ = e.enc.Close()
			e.done = true
			continue
		}
		_, _ = io.WriteString(e.enc, string(chunk.Ok.Some))
		_ = e.enc.Flush()
	}
	data := rt.Bytes(e.out.String())
	e.out.Reset()
	return rt.Ok[rt.Option[rt.Bytes], rt.IOError](rt.Some(data))
}
