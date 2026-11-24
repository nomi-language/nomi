package stdcompress

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// Formats, as std/compress.nomi numbers a Format.
const (
	formatGzip    = 0
	formatZlib    = 1
	formatDeflate = 2
)

// Failure kinds, as std/compress.nomi reads them into an Error.
const (
	kindNone        = 0
	kindCorrupt     = 1
	kindTruncated   = 2
	kindTooLarge    = 3
	kindWrongFormat = 4
)

// noFormat is a sniffed format when the header shows none.
const noFormat = -1

// streamNames are the names a stream's io.Error carries, by format.
var streamNames = [...]string{formatGzip: "<gzip>", formatZlib: "<zlib>", formatDeflate: "<deflate>"}

func formatOf(format int64) int64 {
	if format < formatGzip || format > formatDeflate {
		return formatGzip
	}
	return format
}

// levelOf is level as Go takes it: -1 to 9, and the default otherwise.
func levelOf(level int64) int {
	if level < flate.DefaultCompression || level > flate.BestCompression {
		return flate.DefaultCompression
	}
	return int(level)
}

// header is what a gzip writer writes before the data.
type header struct {
	name, comment string
	modified      int64
	os            int64
}

func headerOf(name, comment string, modified, os rt.Int) header {
	return header{name: latin1(name), comment: latin1(comment), modified: modified.Saturated(), os: os.Saturated()}
}

// latin1 is s with each character gzip's header cannot hold, one outside
// ISO 8859-1 or a NUL, as `?`.
func latin1(s string) string {
	return strings.Map(func(r rune) rune {
		if r == 0 || r > 0xff {
			return '?'
		}
		return r
	}, s)
}

// encoder is a compressor: gzip's, zlib's and flate's writers.
type encoder interface {
	io.WriteCloser
	Flush() error
}

// newEncoder is a compressor into w. No operand makes it fail.
func newEncoder(w io.Writer, format, level int64, h header, dict rt.Option[rt.Bytes]) encoder {
	lv := levelOf(level)
	switch formatOf(format) {
	case formatZlib:
		var d []byte
		if dict.Tag == rt.TagSome {
			d = []byte(dict.Some)
		}
		z, _ := zlib.NewWriterLevelDict(w, lv, d)
		return z
	case formatDeflate:
		var d []byte
		if dict.Tag == rt.TagSome {
			d = []byte(dict.Some)
		}
		f, _ := flate.NewWriterDict(w, lv, d)
		return f
	}
	g, _ := gzip.NewWriterLevel(w, lv)
	g.Name, g.Comment = h.name, h.comment
	if h.modified > 0 && h.modified <= math.MaxUint32 {
		g.ModTime = time.Unix(h.modified, 0)
	}
	g.OS = 255
	if h.os >= 0 && h.os <= 255 {
		g.OS = byte(h.os)
	}
	return g
}

// newDecoder is a decompressor of r. gzip and zlib read their header here.
func newDecoder(r io.Reader, format int64, dict rt.Option[rt.Bytes]) (io.Reader, error) {
	var d []byte
	if dict.Tag == rt.TagSome {
		d = []byte(dict.Some)
	}
	switch formatOf(format) {
	case formatZlib:
		return zlib.NewReaderDict(r, d)
	case formatDeflate:
		return flate.NewReaderDict(r, d), nil
	}
	return gzip.NewReader(r)
}

// sniff is the format a stream's first bytes show, or noFormat.
func sniff(start []byte) int64 {
	if len(start) < 2 {
		return noFormat
	}
	if start[0] == 0x1f && start[1] == 0x8b {
		return formatGzip
	}
	if start[0]&0x0f == 8 && start[0]>>4 <= 7 && (uint16(start[0])<<8|uint16(start[1]))%31 == 0 {
		return formatZlib
	}
	return noFormat
}

// failure is the kind, the format the header shows when that is another,
// and the reason for err, a decoder's failure on a stream of format whose
// first bytes are start.
func failure(err error, format int64, start []byte) (kind, found int64, reason string) {
	format = formatOf(format)
	header := errors.Is(err, gzip.ErrHeader) || errors.Is(err, zlib.ErrHeader)
	var corrupt flate.CorruptInputError
	if header || (format == formatDeflate && errors.As(err, &corrupt)) {
		if s := sniff(start); s != noFormat && s != format {
			return kindWrongFormat, s, ""
		}
	}
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return kindTruncated, noFormat, ""
	case header:
		return kindCorrupt, noFormat, "the header is not valid"
	case errors.Is(err, gzip.ErrChecksum), errors.Is(err, zlib.ErrChecksum):
		return kindCorrupt, noFormat, "the checksum does not match the data"
	case errors.Is(err, zlib.ErrDictionary):
		return kindCorrupt, noFormat, "the stream needs the preset dictionary it was written with"
	case errors.As(err, &corrupt):
		return kindCorrupt, noFormat, fmt.Sprintf("the compressed data is not valid at byte %d", int64(corrupt))
	}
	return kindCorrupt, noFormat, err.Error()
}

// Encode is `std::compress::host.encode`: data compressed in format at
// level, a gzip stream with the header's fields and a zlib or raw deflate
// one with dict as its preset dictionary.
func Encode(
	format int64, data rt.Bytes, level int64,
	name, comment string, modified, os rt.Int,
	dict rt.Option[rt.Bytes],
) rt.Bytes {
	var out bytes.Buffer
	enc := newEncoder(&out, format, level, headerOf(name, comment, modified, os), dict)
	_, _ = io.WriteString(enc, string(data))
	_ = enc.Close()
	return rt.Bytes(out.String())
}

// Decode is `std::compress::host.decode`: what data holds in format,
// decoded with dict, and a failure's kind (kindNone for none), the format
// the header shows, and the reason. A stream that decodes to more than
// max bytes is kindTooLarge, found once max and one byte are made.
func Decode(format int64, data rt.Bytes, maxArg rt.Int, dict rt.Option[rt.Bytes]) (rt.Bytes, int64, int64, string) {
	start := []byte(data[:min(len(data), 2)])
	dec, err := newDecoder(strings.NewReader(string(data)), format, dict)
	if err != nil {
		kind, found, reason := failure(err, format, start)
		return "", kind, found, reason
	}
	out, kind, found, reason := readLimited(dec, maxArg.Saturated(), format, start)
	return rt.Bytes(out), kind, found, reason
}

// readLimited reads dec to its end, or to max and one byte past it, which
// is kindTooLarge.
func readLimited(dec io.Reader, max, format int64, start []byte) ([]byte, int64, int64, string) {
	limit := max
	if limit < math.MaxInt64 {
		limit++
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(io.LimitReader(dec, limit)); err != nil {
		kind, found, reason := failure(err, format, start)
		return nil, kind, found, reason
	}
	if int64(out.Len()) > max {
		return nil, kindTooLarge, noFormat, ""
	}
	return out.Bytes(), kindNone, noFormat, ""
}

// clampOffset is offset as an index into data: one outside it is data's
// end, where a stream reads as empty.
func clampOffset(data rt.Bytes, offsetArg rt.Int) int64 {
	offset := offsetArg.Saturated()
	if offset < 0 || offset > int64(len(data)) {
		return int64(len(data))
	}
	return offset
}

// GzipHeader is `std::compress::host.gzip_header`: the name, comment,
// modification time in Unix seconds (0 for none) and OS of the gzip
// member at offset in data, read without decoding its data, and a failure
// as Decode's.
func GzipHeader(data rt.Bytes, offsetArg rt.Int) (string, string, int64, int64, int64, int64, string) {
	offset := clampOffset(data, offsetArg)
	z, err := gzip.NewReader(strings.NewReader(string(data[offset:])))
	if err != nil {
		kind, found, reason := failure(err, formatGzip, []byte(data[offset:min(int64(len(data)), offset+2)]))
		return "", "", 0, 0, kind, found, reason
	}
	var modified int64
	if !z.ModTime.IsZero() {
		modified = z.ModTime.Unix()
	}
	return z.Name, z.Comment, modified, int64(z.OS), kindNone, noFormat, ""
}

// DecodeAt is `std::compress::host.decode_at`: the stream at offset in
// data, in format, decoded with dict up to max bytes (a gzip stream's
// first member only), the offset just past the stream's end, and a
// failure as Decode's. The decoders read a strings.Reader, an
// io.ByteReader, so none reads a byte past the stream's end: gzip's
// trailer, zlib's checksum or raw deflate's final block.
func DecodeAt(format int64, data rt.Bytes, offsetArg, maxArg rt.Int, dict rt.Option[rt.Bytes]) (rt.Bytes, int64, int64, int64, string) {
	offset := clampOffset(data, offsetArg)
	start := []byte(data[offset:min(int64(len(data)), offset+2)])
	rest := strings.NewReader(string(data[offset:]))
	dec, err := newDecoder(rest, format, dict)
	if err != nil {
		kind, found, reason := failure(err, format, start)
		return "", 0, kind, found, reason
	}
	if z, ok := dec.(*gzip.Reader); ok {
		z.Multistream(false)
	}
	out, kind, found, reason := readLimited(dec, maxArg.Saturated(), format, start)
	if kind != kindNone {
		return "", 0, kind, found, reason
	}
	return rt.Bytes(out), int64(len(data)) - int64(rest.Len()), kindNone, noFormat, ""
}
