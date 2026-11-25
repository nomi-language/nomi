package stdcompress

import (
	"context"
	"io"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/nomi-language/nomi/rt"
)

func frame() *rt.Frame { return rt.NewFrame(context.Background()) }

// readAll reads r to its end in reads of size, as a program's loop does.
func readAll(fr *rt.Frame, r rt.Reader, size int64) (string, *rt.IOError) {
	var out []byte
	for {
		chunk := rt.ReaderRead(fr, r, rt.SmallInt(size))
		if chunk.Tag != rt.TagOk {
			return string(out), &chunk.Err
		}
		if chunk.Ok.Tag != rt.TagSome {
			return string(out), nil
		}
		out = append(out, chunk.Ok.Some...)
	}
}

// A reader decodes a source many times its read buffer, in small reads and
// whole, and closing it leaves its source open.
func TestReader_DecodesASourceLargerThanItsBuffer(t *testing.T) {
	text := mixed(1 << 20)
	for format := int64(formatGzip); format <= formatDeflate; format++ {
		source := rt.ReaderFromBytes(encodePlain(format, 6, text))
		r := Reader(format, source, noDict)
		got, err := readAll(frame(), r, 1000)
		if err != nil || got != text {
			t.Errorf("format %d: %d bytes, error %+v", format, len(got), err)
		}
		if res := rt.ReaderClose(r); res.Tag != rt.TagOk {
			t.Errorf("format %d: close %+v", format, res)
		}
		if res := rt.ReaderRead(frame(), source, rt.SmallInt(1)); res.Tag != rt.TagOk {
			t.Errorf("format %d: the source after the reader's close: %+v, want it open", format, res)
		}
	}
}

// A stream that is not valid fails its read with Corrupt or Truncated,
// naming the stream, and a failed read of the source is the source's error.
func TestReader_AnInvalidStreamFailsItsRead(t *testing.T) {
	gz := fixture(t, "fox.txt.gz")
	for _, c := range []struct {
		name   string
		format int64
		source rt.Reader
		want   rt.IOError
	}{
		{"cut short", formatGzip, rt.ReaderFromBytes(gz[:len(gz)-6]), rt.IOError{Tag: rt.TagIOTruncated, Path: "<gzip>"}},
		{"another format", formatZlib, rt.ReaderFromBytes(gz), rt.IOError{Tag: rt.TagIOCorrupt, Path: "<zlib>", Reason: "the stream is gzip data, not zlib"}},
		{"a bad checksum", formatZlib, rt.ReaderFromBytes(fixture(t, "fox.zlib")[:len(fixture(t, "fox.zlib"))-1] + "\x00"), rt.IOError{Tag: rt.TagIOCorrupt, Path: "<zlib>", Reason: "the checksum does not match the data"}},
		{"a failing source", formatGzip, rt.ReaderFromFn(func(*rt.Frame) rt.Result[rt.Option[rt.Bytes], rt.IOError] {
			return rt.Err[rt.Option[rt.Bytes]](rt.IOError{Tag: rt.TagIOTimedOut})
		}), rt.IOError{Tag: rt.TagIOTimedOut}},
	} {
		_, err := readAll(frame(), Reader(c.format, c.source, noDict), 64)
		if err == nil || *err != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, err, c.want)
		}
	}
}

// A writer compresses into its sink, its flush passes what was written on
// so a reader of the sink decodes it, and its close writes the end and
// leaves the sink open.
func TestWriter_FlushPassesDataOnAndCloseLeavesTheSinkOpen(t *testing.T) {
	sink := rt.WriterToMemory()
	w := Writer(formatGzip, sink, 6, "", "", rt.SmallInt(0), rt.SmallInt(255), noDict)
	fr := frame()
	if res := rt.WriterWriteText(fr, w, "first "); res.Tag != rt.TagOk {
		t.Fatal(res)
	}
	if res := rt.WriterFlush(fr, w); res.Tag != rt.TagOk {
		t.Fatal(res)
	}
	partial := rt.WriterCollected(sink)
	got, err := readAll(fr, Reader(formatGzip, rt.ReaderFromBytes(partial), noDict), 64)
	if got != "first " || err == nil || err.Tag != rt.TagIOTruncated {
		t.Errorf("after a flush: %q, %+v; want the text so far and then Truncated", got, err)
	}
	_ = rt.WriterWriteText(fr, w, "second")
	if res := rt.WriterClose(fr, w); res.Tag != rt.TagOk {
		t.Fatal(res)
	}
	if out, kind, _, _ := Decode(formatGzip, rt.WriterCollected(sink), rt.SmallInt(100), noDict); kind != kindNone || out != "first second" {
		t.Errorf("after the close: %q, kind %d", out, kind)
	}
	if res := rt.WriterWriteText(fr, sink, "!"); res.Tag != rt.TagOk {
		t.Errorf("the sink after the writer's close: %+v, want it open", res)
	}
}

// A sink that fails midway fails the write, flush or close that reaches
// it with the sink's error, and every later one.
func TestWriter_ASinkThatFailsMidwayFailsTheWriter(t *testing.T) {
	written := 0
	sink := rt.WriterFromFn(func(_ *rt.Frame, chunk rt.Bytes) rt.Result[rt.Unit, rt.IOError] {
		if written+len(chunk) > 100<<10 {
			return rt.Err[rt.Unit](rt.IOError{Tag: rt.TagIONoSpace, Path: "disk"})
		}
		written += len(chunk)
		return rt.Ok[rt.Unit, rt.IOError](rt.Unit{})
	}, func(*rt.Frame) rt.Result[rt.Unit, rt.IOError] { return rt.Ok[rt.Unit, rt.IOError](rt.Unit{}) }, true)
	w := Writer(formatDeflate, sink, 0, "", "", rt.SmallInt(0), rt.SmallInt(255), noDict)
	fr := frame()
	want := rt.IOError{Tag: rt.TagIONoSpace, Path: "disk"}
	failedAt := -1
	for i := range 64 {
		if res := rt.WriterWriteText(fr, w, mixed(16<<10)); res.Tag != rt.TagOk {
			if res.Err != want {
				t.Fatalf("write %d: %+v, want %+v", i, res.Err, want)
			}
			failedAt = i
			break
		}
	}
	if failedAt < 0 {
		t.Fatal("1 MiB stored into a sink that takes 100 KiB never failed")
	}
	if res := rt.WriterClose(fr, w); res.Tag != rt.TagErr || res.Err != want {
		t.Errorf("close after the failure: %+v, want %+v", res, want)
	}
}

// An encoding reader compresses its source as it is read, each read of the
// source passed on: a source of three chunks gives at least three reads,
// and they decode to the source.
func TestEncodingReader_PassesEachSourceReadOn(t *testing.T) {
	chunks := []string{"one ", "two ", "three"}
	source := rt.ReaderFromFn(func(*rt.Frame) rt.Result[rt.Option[rt.Bytes], rt.IOError] {
		if len(chunks) == 0 {
			return rt.Ok[rt.Option[rt.Bytes], rt.IOError](rt.None[rt.Bytes]())
		}
		next := chunks[0]
		chunks = chunks[1:]
		return rt.Ok[rt.Option[rt.Bytes], rt.IOError](rt.Some(rt.Bytes(next)))
	})
	r := EncodingReader(formatGzip, source, 6, "", "", rt.SmallInt(0), rt.SmallInt(255), noDict)
	fr := frame()
	var packed rt.Bytes
	reads := 0
	for {
		chunk := rt.ReaderRead(fr, r, rt.SmallInt(1<<20))
		if chunk.Tag != rt.TagOk {
			t.Fatal(chunk.Err)
		}
		if chunk.Ok.Tag != rt.TagSome {
			break
		}
		reads++
		packed += chunk.Ok.Some
		if reads == 1 {
			// The first read already holds the first chunk.
			got, _ := readAll(fr, Reader(formatGzip, rt.ReaderFromBytes(packed), noDict), 64)
			if got != "one " {
				t.Errorf("the first read decodes to %q, want %q", got, "one ")
			}
		}
	}
	if out, kind, _, _ := Decode(formatGzip, packed, rt.SmallInt(100), noDict); kind != kindNone || out != "one two three" || reads < 3 {
		t.Errorf("%q, kind %d, %d reads", out, kind, reads)
	}
}

// A task stopped while a read waits on the source unwinds there, and the
// reader answers later reads with EINTR, since its decoder stopped
// part-way.
func TestReader_ATaskStoppedDuringAReadUnwinds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fr := rt.NewHostFrame(ctx)
	packed := encodePlain(formatZlib, 6, mixed(200<<10))
	sent := false
	source := rt.ReaderFromFn(func(fr *rt.Frame) rt.Result[rt.Option[rt.Bytes], rt.IOError] {
		if sent {
			cancel()
			rt.CancelIfDone(fr)
		}
		sent = true
		return rt.Ok[rt.Option[rt.Bytes], rt.IOError](rt.Some(packed[:1000]))
	})
	r := Reader(formatZlib, source, noDict)
	unwound := func() (stopped bool) {
		defer func() { stopped = rt.Unwound(recover()) }()
		_, _ = readAll(fr, r, 64<<10)
		return false
	}()
	if !unwound {
		t.Fatal("the read did not unwind when its task was stopped")
	}
	got := rt.ReaderRead(frame(), r, rt.SmallInt(10))
	want := rt.IOError{Tag: rt.TagIOOther, Operation: "read", Path: "<zlib>", Code: "EINTR"}
	if got.Tag != rt.TagErr || got.Err != want {
		t.Errorf("a read after the stop: %+v, want %+v", got, want)
	}
}

// A reader the program drops before its end holds nothing once collected:
// no goroutine, and its source is not closed for it.
func TestReader_AnAbandonedReaderLeavesNothingRunning(t *testing.T) {
	settle := func() int {
		for range 5 {
			runtime.GC()
			time.Sleep(10 * time.Millisecond)
		}
		return runtime.NumGoroutine()
	}
	before := settle()
	packed := encodePlain(formatGzip, 6, mixed(256<<10))
	sources := make([]rt.Reader, 0, 100)
	for range 100 {
		source := rt.ReaderFromBytes(packed)
		sources = append(sources, source)
		r := Reader(formatGzip, source, noDict)
		if res := rt.ReaderRead(frame(), r, rt.SmallInt(10)); res.Tag != rt.TagOk {
			t.Fatal(res)
		}
	}
	if after := settle(); after > before {
		t.Errorf("%d goroutines after 100 abandoned readers, %d before", after, before)
	}
	for i, s := range sources {
		if res := rt.ReaderRead(frame(), s, rt.SmallInt(1)); res.Tag != rt.TagOk {
			t.Fatalf("source %d after its reader was dropped: %+v, want it open", i, res)
		}
	}
}

// A write's or a close's failure that is not the wrapped stream's is sorted
// by rt's one table: each of these was Other.
func TestIOFailure_AWriteFailureIsSortedByTheTable(t *testing.T) {
	for _, c := range []struct {
		err  error
		want rt.IOError
	}{
		{io.ErrClosedPipe, rt.IOError{Tag: rt.TagIOClosed}},
		{syscall.EPIPE, rt.IOError{Tag: rt.TagIOBrokenPipe}},
		{syscall.ENOSPC, rt.IOError{Tag: rt.TagIONoSpace, Path: "<gzip>"}},
	} {
		if got := ioFailure(c.err, "write", "<gzip>", formatGzip, nil); got != c.want {
			t.Errorf("write failing with %v: %+v, want %+v", c.err, got, c.want)
		}
	}
}
