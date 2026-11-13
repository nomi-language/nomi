package stdhttp

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"

	"github.com/nomi-language/nomi/rt"
)

// Response compression for std/http's `compress` middleware: gzip over a
// whole body, over a stream as it is read, and over a writer as a handler
// writes to it. Go's standard library has no brotli encoder.

// gzipLevel is level as compress/gzip takes it: 1 to 9, and Go's default
// for any other.
func gzipLevel(level int64) int {
	if level < gzip.BestSpeed || level > gzip.BestCompression {
		return gzip.DefaultCompression
	}
	return int(level)
}

// GzipBytes is `std::http::host.gzip_bytes`: data compressed with gzip at level.
func GzipBytes(data rt.Bytes, level int64) rt.Bytes {
	var out bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&out, gzipLevel(level))
	_, _ = gz.Write([]byte(data))
	_ = gz.Close()
	return rt.Bytes(out.String())
}

// Gunzip is `std::http::host.gunzip`: the bytes gzip data holds, or why it is not
// gzip.
func Gunzip(data rt.Bytes) rt.Result[rt.Bytes, string] {
	gz, err := gzip.NewReader(bytes.NewReader([]byte(data)))
	if err != nil {
		return rt.Err[rt.Bytes](err.Error())
	}
	out, err := io.ReadAll(gz)
	if err != nil {
		return rt.Err[rt.Bytes](err.Error())
	}
	return rt.Ok[rt.Bytes, string](rt.Bytes(out))
}

// GzipWriter is `std::http::host.gzip_writer`: a writer that compresses into w
// at level. Writer.flush flushes the compressor and then w, so what was
// written so far reaches the client; closing it writes gzip's end and
// leaves w open.
func GzipWriter(fr *rt.Frame, w rt.Writer, level int64) rt.Writer {
	gz, _ := gzip.NewWriterLevel(writerOutput{fr: fr, w: w}, gzipLevel(level))
	flush := func() error {
		if err := gz.Flush(); err != nil {
			return err
		}
		if r := rt.WriterFlush(fr, w); r.Tag != rt.TagOk {
			return rt.IOFailure{E: r.Err}
		}
		return nil
	}
	return rt.NewWriter(rt.WriteSink{Name: "<gzip>", W: gz, Close: gz.Close, Flush: flush})
}

// GzipReader is `std::http::host.gzip_reader`: a reader of r's bytes compressed
// with gzip at level. Each read of r is flushed through the compressor, so
// a stream that arrives slowly is sent as it arrives.
func GzipReader(fr *rt.Frame, r rt.Reader, level int64) rt.Reader {
	g := &gzipping{src: readerInput{fr: fr, r: r}, buf: make([]byte, 32<<10)}
	g.gz, _ = gzip.NewWriterLevel(&g.out, gzipLevel(level))
	return rt.NewReader(rt.ReadSource{Name: "<gzip>", R: g})
}

type gzipping struct {
	src  io.Reader
	gz   *gzip.Writer
	out  bytes.Buffer
	buf  []byte
	done bool
}

func (g *gzipping) Read(p []byte) (int, error) {
	for g.out.Len() == 0 {
		if g.done {
			return 0, io.EOF
		}
		n, err := g.src.Read(g.buf)
		if n > 0 {
			if _, werr := g.gz.Write(g.buf[:n]); werr != nil {
				return 0, werr
			}
		}
		switch {
		case errors.Is(err, io.EOF):
			if cerr := g.gz.Close(); cerr != nil {
				return 0, cerr
			}
			g.done = true
		case err != nil:
			return 0, err
		case n > 0:
			if ferr := g.gz.Flush(); ferr != nil {
				return 0, ferr
			}
		}
	}
	return g.out.Read(p)
}
