package stdhttp

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// zeroBomb is n zero bytes, gzipped: about a thousandth of n.
func zeroBomb(t *testing.T, n int) []byte {
	t.Helper()
	var packed bytes.Buffer
	w, _ := gzip.NewWriterLevel(&packed, gzip.BestCompression)
	if _, err := w.Write(make([]byte, n)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return packed.Bytes()
}

// Send reads a fetched response's body through readAtMost after Go's
// transport has decoded it. A gzip body of 64 MiB of zeros, about 64 KiB
// on the wire, read under a 1 MiB limit is too large once the decoder has
// given the limit and one byte: the read asks it for no more. A body within
// the limit, and one with no limit, is read whole.
func TestReadAtMost_AsksTheDecoderForNoMoreThanTheLimitAndAByte(t *testing.T) {
	const decoded = 64 << 20
	bomb := zeroBomb(t, decoded)
	if len(bomb) > decoded/500 {
		t.Fatalf("the bomb is %d bytes on the wire; want about a thousandth of %d", len(bomb), decoded)
	}
	const limit = 1 << 20
	zr, err := gzip.NewReader(bytes.NewReader(bomb))
	if err != nil {
		t.Fatal(err)
	}
	made := &countingReader{r: zr}
	body, tooLarge, err := readAtMost(made, limit)
	if err != nil || !tooLarge {
		t.Fatalf("tooLarge %v, err %v; want too large", tooLarge, err)
	}
	if made.n != limit+1 || int64(len(body)) != limit+1 {
		t.Errorf("the decoder gave %d bytes and the read kept %d, want the limit and one, %d", made.n, len(body), limit+1)
	}
	t.Logf("decoded %d of %d bytes", made.n, decoded)

	for _, c := range []struct {
		name string
		max  int64
		body string
	}{
		{"exactly the limit", 5, "hello"},
		{"under the limit", 100, "hello"},
		{"no limit", -1, strings.Repeat("x", 1<<16)},
	} {
		body, tooLarge, err := readAtMost(strings.NewReader(c.body), c.max)
		if err != nil || tooLarge || string(body) != c.body {
			t.Errorf("%s: %d bytes, tooLarge %v, err %v", c.name, len(body), tooLarge, err)
		}
	}
	if _, tooLarge, _ := readAtMost(strings.NewReader("hello!"), 5); !tooLarge {
		t.Errorf("a body one byte past the limit is not too large")
	}
}
