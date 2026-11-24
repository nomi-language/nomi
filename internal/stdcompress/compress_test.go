package stdcompress

import (
	"bytes"
	"compress/gzip"
	"io"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

var noDict = rt.None[rt.Bytes]()

func fixture(t *testing.T, name string) rt.Bytes {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return rt.Bytes(data)
}

func encodePlain(format, level int64, data string) rt.Bytes {
	return Encode(format, rt.Bytes(data), level, "", "", rt.SmallInt(0), rt.SmallInt(255), noDict)
}

// mixed is n bytes of text and noise, so each level compresses it
// differently and none stores it as one repeated run.
func mixed(n int) string {
	r := rand.New(rand.NewPCG(1, 2))
	var b strings.Builder
	for b.Len() < n {
		if r.IntN(3) == 0 {
			b.WriteByte(byte(r.IntN(256)))
		} else {
			b.WriteString("the quick brown fox ")
		}
	}
	return b.String()[:n]
}

// Each format at each level decodes to what it encoded: empty, one byte,
// and several megabytes.
func TestEncodeDecode_RoundTripsEachFormatAndLevel(t *testing.T) {
	inputs := []string{"", "x", mixed(3 << 20)}
	cases := 0
	for format := int64(formatGzip); format <= formatDeflate; format++ {
		for _, level := range []int64{-1, 0, 1, 6, 9, 42} {
			for _, in := range inputs {
				packed := encodePlain(format, level, in)
				out, kind, _, reason := Decode(format, packed, rt.SmallInt(int64(len(in))), noDict)
				if kind != kindNone || string(out) != in {
					t.Errorf("format %d level %d, %d bytes: kind %d (%s), %d bytes back", format, level, len(in), kind, reason, len(out))
				}
				cases++
			}
		}
	}
	t.Logf("%d round trips", cases)
}

// Streams the system's gzip and Python's zlib wrote decode to their text.
func TestDecode_ReadsWhatAnotherEncoderWrote(t *testing.T) {
	fox := string(fixture(t, "fox.txt"))
	for _, c := range []struct {
		file   string
		format int64
		dict   rt.Option[rt.Bytes]
		want   string
	}{
		{"fox.txt.gz", formatGzip, noDict, fox},
		{"fox.zlib", formatZlib, noDict, fox},
		{"fox.deflate", formatDeflate, noDict, fox},
		{"fox-dict.zlib", formatZlib, rt.Some(rt.Bytes("The quick brown fox jumps over the lazy dog.")), fox},
		{"two-members.gz", formatGzip, noDict, fox + "second member\n"},
	} {
		out, kind, _, reason := Decode(c.format, fixture(t, c.file), rt.SmallInt(1000), c.dict)
		if kind != kindNone || string(out) != c.want {
			t.Errorf("%s: kind %d (%s), got %q", c.file, kind, reason, out)
		}
	}
}

// Each way a stream can fail is its own kind: a corrupt block, a stream
// cut short, another format's header, a checksum that does not match, and
// a zlib stream without its preset dictionary.
func TestDecode_EachFailureIsItsKind(t *testing.T) {
	gz := fixture(t, "fox.txt.gz")
	zl := fixture(t, "fox.zlib")
	raw := fixture(t, "fox.deflate")
	flip := func(data rt.Bytes, at int) rt.Bytes {
		b := []byte(data)
		if at < 0 {
			at += len(b)
		}
		b[at] ^= 0xff
		return rt.Bytes(b)
	}
	for _, c := range []struct {
		name   string
		format int64
		data   rt.Bytes
		kind   int64
		found  int64
		reason string
	}{
		{"a corrupt deflate block", formatDeflate, flip(raw, 0), kindCorrupt, noFormat, "the compressed data is not valid at byte 3"},
		{"gzip cut short", formatGzip, gz[:len(gz)-6], kindTruncated, noFormat, ""},
		{"zlib cut short", formatZlib, zl[:5], kindTruncated, noFormat, ""},
		{"empty gzip", formatGzip, "", kindTruncated, noFormat, ""},
		{"empty deflate", formatDeflate, "", kindTruncated, noFormat, ""},
		{"zlib read as gzip", formatGzip, zl, kindWrongFormat, formatZlib, ""},
		{"gzip read as zlib", formatZlib, gz, kindWrongFormat, formatGzip, ""},
		{"gzip read as raw deflate", formatDeflate, gz, kindWrongFormat, formatGzip, ""},
		{"plain text as gzip", formatGzip, "plain text", kindCorrupt, noFormat, "the header is not valid"},
		{"gzip's CRC-32", formatGzip, flip(gz, -5), kindCorrupt, noFormat, "the checksum does not match the data"},
		{"zlib's Adler-32", formatZlib, flip(zl, -1), kindCorrupt, noFormat, "the checksum does not match the data"},
		{"a missing dictionary", formatZlib, fixture(t, "fox-dict.zlib"), kindCorrupt, noFormat, "the stream needs the preset dictionary it was written with"},
	} {
		_, kind, found, reason := Decode(c.format, c.data, rt.SmallInt(1000), noDict)
		if kind != c.kind || found != c.found || reason != c.reason {
			t.Errorf("%s: (%d, %d, %q), want (%d, %d, %q)", c.name, kind, found, reason, c.kind, c.found, c.reason)
		}
	}
	_, kind, _, reason := Decode(formatZlib, fixture(t, "fox-dict.zlib"), rt.SmallInt(1000), rt.Some(rt.Bytes("another dictionary")))
	if kind != kindCorrupt || reason != "the stream needs the preset dictionary it was written with" {
		t.Errorf("a wrong dictionary: (%d, %q)", kind, reason)
	}
}

// counting counts the bytes read through it.
type counting struct {
	r io.Reader
	n int64
}

func (c *counting) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// A few kilobytes that expand to 64 MiB stop at a 1 MiB limit: the decoder
// makes the limit and a byte, and reads a sliver of its input.
func TestDecode_ABombStopsAtTheLimitWithoutMakingTheRest(t *testing.T) {
	var packed bytes.Buffer
	w, _ := gzip.NewWriterLevel(&packed, gzip.BestCompression)
	zeros := make([]byte, 1<<20)
	for range 64 {
		_, _ = w.Write(zeros)
	}
	_ = w.Close()
	if packed.Len() > 100<<10 {
		t.Fatalf("the bomb is %d bytes; want a few kilobytes", packed.Len())
	}
	const limit = 1 << 20
	_, kind, _, _ := Decode(formatGzip, rt.Bytes(packed.String()), rt.SmallInt(limit), noDict)
	if kind != kindTooLarge {
		t.Fatalf("kind %d, want TooLarge", kind)
	}

	in := &counting{r: bytes.NewReader(packed.Bytes())}
	dec, err := newDecoder(in, formatGzip, noDict)
	if err != nil {
		t.Fatal(err)
	}
	made := &counting{r: dec}
	_, kind, _, _ = readLimited(made, limit, formatGzip, nil)
	if kind != kindTooLarge {
		t.Fatalf("kind %d, want TooLarge", kind)
	}
	if made.n != limit+1 {
		t.Errorf("the decoder made %d bytes, want the limit and one, %d", made.n, limit+1)
	}
	if in.n > int64(packed.Len())/4 {
		t.Errorf("the decoder read %d of the bomb's %d bytes, want a quarter at most", in.n, packed.Len())
	}
	t.Logf("made %d bytes, read %d of %d", made.n, in.n, packed.Len())
}

// A limit of exactly the data's length is enough, and of one less is not.
func TestDecode_TheLimitIsInclusive(t *testing.T) {
	packed := encodePlain(formatZlib, -1, "hello")
	if out, kind, _, _ := Decode(formatZlib, packed, rt.SmallInt(5), noDict); kind != kindNone || out != "hello" {
		t.Errorf("limit 5: kind %d, %q", kind, out)
	}
	if _, kind, _, _ := Decode(formatZlib, packed, rt.SmallInt(4), noDict); kind != kindTooLarge {
		t.Errorf("limit 4: kind %d, want TooLarge", kind)
	}
}

// A gzip header the system's gzip wrote reads back its fields, and one
// encoded here reads back what was given, a character outside ISO 8859-1
// as `?`.
func TestGzipHeader_ReadsEachField(t *testing.T) {
	name, comment, modified, osNumber, kind, _, _ := GzipHeader(fixture(t, "fox.txt.gz"), rt.SmallInt(0))
	if kind != kindNone || name != "fox.txt" || comment != "" || modified != 0x69577bf5 || osNumber != 3 {
		t.Errorf("gzip's header: %q %q %d %d (kind %d)", name, comment, modified, osNumber, kind)
	}
	packed := Encode(formatGzip, "x", -1, "naïve 名.txt", "nul\x00", rt.SmallInt(1_767_225_600), rt.SmallInt(11), noDict)
	name, comment, modified, osNumber, kind, _, _ = GzipHeader(packed, rt.SmallInt(0))
	if kind != kindNone || name != "naïve ?.txt" || comment != "nul?" || modified != 1_767_225_600 || osNumber != 11 {
		t.Errorf("an encoded header: %q %q %d %d (kind %d)", name, comment, modified, osNumber, kind)
	}
	packed = Encode(formatGzip, "x", -1, "", "", rt.SmallInt(-5), rt.SmallInt(4000), noDict)
	if _, _, modified, osNumber, _, _, _ = GzipHeader(packed, rt.SmallInt(0)); modified != 0 || osNumber != 255 {
		t.Errorf("a time before 1970 and an OS past 255: %d %d, want 0 255", modified, osNumber)
	}
}

// A file of two members reads one member at a time, each ending where the
// next begins.
func TestDecodeAt_ReadsOneGzipMemberAtATime(t *testing.T) {
	data := fixture(t, "two-members.gz")
	first, next, kind, _, _ := DecodeAt(formatGzip, data, rt.SmallInt(0), rt.SmallInt(1000), noDict)
	if kind != kindNone || string(first) != string(fixture(t, "fox.txt")) || next != int64(len(fixture(t, "fox.txt.gz"))) {
		t.Fatalf("first member: %q, next %d, kind %d", first, next, kind)
	}
	second, end, kind, _, _ := DecodeAt(formatGzip, data, rt.SmallInt(next), rt.SmallInt(1000), noDict)
	if kind != kindNone || second != "second member\n" || end != int64(len(data)) {
		t.Fatalf("second member: %q, end %d, kind %d", second, end, kind)
	}
	if _, _, kind, _, _ := DecodeAt(formatGzip, data, rt.SmallInt(0), rt.SmallInt(10), noDict); kind != kindTooLarge {
		t.Errorf("a member past the limit: kind %d", kind)
	}
}

// A stream of each format followed by other bytes decodes to its data and
// ends where the stream does, read from the start of the data or from an
// offset into it: streams this package encoded, at each level and with a
// preset dictionary, and streams the system's gzip and Python's zlib wrote.
// The bytes after it are never read.
func TestDecodeAt_EndsWhereTheStreamEnds(t *testing.T) {
	const after = "PK\x07\x08 and the rest of the archive"
	dict := rt.Some(rt.Bytes("The quick brown fox jumps over the lazy dog."))
	fox := string(fixture(t, "fox.txt"))
	type stream struct {
		name   string
		format int64
		packed rt.Bytes
		dict   rt.Option[rt.Bytes]
		want   string
	}
	streams := []stream{
		{"fox.txt.gz", formatGzip, fixture(t, "fox.txt.gz"), noDict, fox},
		{"fox.zlib", formatZlib, fixture(t, "fox.zlib"), noDict, fox},
		{"fox.deflate", formatDeflate, fixture(t, "fox.deflate"), noDict, fox},
		{"fox-dict.zlib", formatZlib, fixture(t, "fox-dict.zlib"), dict, fox},
	}
	for format := int64(formatGzip); format <= formatDeflate; format++ {
		for _, level := range []int64{-1, 0, 1, 9} {
			for _, in := range []string{"", "x", mixed(200 << 10)} {
				streams = append(streams, stream{"encoded", format, encodePlain(format, level, in), noDict, in})
			}
		}
		if format != formatGzip {
			packed := Encode(format, rt.Bytes(fox), -1, "", "", rt.SmallInt(0), rt.SmallInt(255), dict)
			streams = append(streams, stream{"encoded with a dictionary", format, packed, dict, fox})
		}
	}
	cases := 0
	for _, s := range streams {
		for _, prefix := range []string{"", "header"} {
			data := rt.Bytes(prefix + string(s.packed) + after)
			out, end, kind, _, reason := DecodeAt(s.format, data, rt.SmallInt(int64(len(prefix))), rt.SmallInt(int64(len(s.want))), s.dict)
			if kind != kindNone || string(out) != s.want || end != int64(len(prefix)+len(s.packed)) {
				t.Errorf("%s (format %d, %d bytes) after %q: kind %d (%s), %d bytes back, end %d, want %d",
					s.name, s.format, len(s.want), prefix, kind, reason, len(out), end, len(prefix)+len(s.packed))
			}
			cases++
		}
	}
	t.Logf("%d streams ended where they end", cases)
}

// A stream that fails fails as Decode's does: cut short, or another
// format's, or past the limit.
func TestDecodeAt_FailsAsDecodeDoes(t *testing.T) {
	gz := fixture(t, "fox.txt.gz")
	for _, c := range []struct {
		name   string
		format int64
		data   rt.Bytes
		max    int64
		kind   int64
	}{
		{"raw deflate cut short", formatDeflate, fixture(t, "fox.deflate")[:10], 1000, kindTruncated},
		{"zlib read as gzip", formatGzip, fixture(t, "fox.zlib"), 1000, kindWrongFormat},
		{"gzip read as zlib", formatZlib, gz, 1000, kindWrongFormat},
		{"past the limit", formatZlib, fixture(t, "fox.zlib"), 10, kindTooLarge},
		{"an offset past the end", formatGzip, gz, 1000, kindTruncated},
	} {
		offset := int64(0)
		if c.name == "an offset past the end" {
			offset = int64(len(gz)) + 5
		}
		out, end, kind, _, _ := DecodeAt(c.format, c.data, rt.SmallInt(offset), rt.SmallInt(c.max), noDict)
		if kind != c.kind || out != "" || end != 0 {
			t.Errorf("%s: kind %d, %d bytes, end %d, want kind %d", c.name, kind, len(out), end, c.kind)
		}
	}
}
