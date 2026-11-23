package stdlibadapters

import (
	"context"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/rt"
)

// TestRtFuncs_CsvAndXmlReaders holds std/csv/host's and std/xml/host's
// rows to what they do through the generated adapters: a reader handle
// over a Reader yields records or tokens one call at a time, then stops
// with the kind, position and message of the failure, and the writer
// helpers escape and quote. std/csv's and std/xml's attached tests and the
// corpus run each through Nomi programs.
func TestRtFuncs_CsvAndXmlReaders(t *testing.T) {
	table, err := Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	fr := rt.NewFrame(context.Background())
	call := func(key string, args ...any) any {
		t.Helper()
		v, err := table[key](fr, args)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return v
	}
	text := func(v any) string { return canonText(v, false) }
	want := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s is %s, want %s", what, got, want)
		}
	}

	records := call("csv/host.open", rt.ReaderFromText("\ufeffa,\"b\"\"c\"\nd,é\"x"), hCodepoint(','), rb.Variant("option.Option", "None"), false, false, int64(0))
	want("the first record", text(call("csv/host.next", records)), `[String("a"), String("b\"c")]`)
	want("its line", text(call("csv/host.line", records)), `Int(1)`)
	want("the second read", text(call("csv/host.next", records)), `[]`)
	want("the stop", text(call("csv/host.stopped", records)), `[Int(1), Int(2), Int(4)]`)
	want("the message", text(call("csv/host.message", records)), `String("a quote in a field that is not quoted; double it inside a quoted field, or allow lazy quotes")`)

	invalid := call("csv/host.open", rt.ReaderFromText("a"), hCodepoint(';'), rb.Variant("option.Option", "Some", hCodepoint(';')), false, false, int64(0))
	want("a comment that is the delimiter's read", text(call("csv/host.next", invalid)), `[]`)
	want("its stop", text(call("csv/host.stopped", invalid)), `[Int(3), Int(0), Int(0)]`)

	failing := rt.ReaderFromText("a")
	rt.ReaderClose(failing)
	closed := call("csv/host.open", failing, hCodepoint(','), rb.Variant("option.Option", "None"), false, false, int64(-1))
	call("csv/host.next", closed)
	want("a closed reader's stop", text(call("csv/host.stopped", closed)), `[Int(2), Int(0), Int(0)]`)
	want("its failure", text(call("csv/host.failure", closed)), `io.Error/4.Closed{}`)

	none := rb.Variant("option.Option", "None")
	want("text read whole", text(call("csv/host.decode", rt.Bytes("\xef\xbb\xbfa,b\n\n\"c\nd\",é\n"), hCodepoint(','), none, false, false, int64(0))),
		`/3{0=[[String("a"), String("b")], [String("c\nd"), String("é")]], 1=[Int(1), Int(3)], 2=[Int(0), Int(0), Int(0)], 3=String("")}`)
	// The same failures as the reader's above, at the same place.
	want("text read whole up to a failure", text(call("csv/host.decode", rt.Bytes("a,\"b\"\"c\"\nd,é\"x"), hCodepoint(','), none, false, false, int64(0))),
		`/3{0=[[String("a"), String("b\"c")]], 1=[Int(1)], 2=[Int(1), Int(2), Int(4)], 3=String("a quote in a field that is not quoted; double it inside a quoted field, or allow lazy quotes")}`)
	want("text read whole with invalid options", text(call("csv/host.decode", rt.Bytes("a"), hCodepoint(';'), rb.Variant("option.Option", "Some", hCodepoint(';')), false, false, int64(0))),
		`/3{0=[], 1=[], 2=[Int(3), Int(0), Int(0)], 3=String("the comment character and the delimiter are both ';'")}`)

	want("an encoded record", text(call("csv/host.encode", rtStrings("a", "b,c", " d", "e\"f"), hCodepoint(','), true)),
		`String("a,\"b,c\",\" d\",\"e\"\"f\"\r\n")`)

	tokens := call("xml/host.open", rt.ReaderFromText(`<?xml version="1.0"?><p:a xmlns:p="urn:p" x="1">t</p:a>`), false)
	want("the start", text(call("xml/host.next", tokens)), `Int(1)`)
	want("its parts", text(call("xml/host.current", tokens)), `[[String("p"), String("a"), String("urn:p")], [String(""), String("x"), String(""), String("1")], [String("p"), String("urn:p")]]`)
	want("the text", text(call("xml/host.next", tokens)), `Int(3)`)
	want("its content", text(call("xml/host.current", tokens)), `[[String("t")]]`)
	want("the end", text(call("xml/host.next", tokens)), `Int(2)`)
	want("its name", text(call("xml/host.current", tokens)), `[[String("p"), String("a"), String("urn:p")]]`)
	want("the stop", text(call("xml/host.next", tokens)), `Int(0)`)
	want("a clean stop", text(call("xml/host.stopped", tokens)), `[Int(0), Int(0), Int(0)]`)

	pi := call("xml/host.open", rt.ReaderFromText(`<a><?go fast?></a>`), false)
	call("xml/host.next", pi)
	want("an instruction", text(call("xml/host.next", pi)), `Int(5)`)
	want("its target and data", text(call("xml/host.current", pi)), `[[String("go"), String("fast")]]`)

	doctype := call("xml/host.open", rt.ReaderFromText(`<!DOCTYPE a [<!ENTITY e SYSTEM "file:///etc/passwd">]><a>&e;</a>`), false)
	want("a DOCTYPE's read", text(call("xml/host.next", doctype)), `Int(0)`)
	want("its stop", text(call("xml/host.stopped", doctype)), `[Int(1), Int(1), Int(1)]`)
	want("its message", text(call("xml/host.message", doctype)), `String("a DOCTYPE is refused: std/xml reads no DTD; allow_doctype skips it")`)

	// The same documents read whole: the tokens' kinds and parts, then the
	// same stop as the reader's.
	want("a document read whole", text(call("xml/host.decode", rt.Bytes(`<?xml version="1.0"?><p:a xmlns:p="urn:p" x="1">t<?go fast?></p:a>`), false)),
		`/3{0=[Int(1), Int(3), Int(5), Int(2)], 1=[[[String("p"), String("a"), String("urn:p")], [String(""), String("x"), String(""), String("1")], [String("p"), String("urn:p")]], [[String("t")]], [[String("go"), String("fast")]], [[String("p"), String("a"), String("urn:p")]]], 2=[Int(0), Int(0), Int(0)], 3=String("")}`)
	want("a DOCTYPE read whole", text(call("xml/host.decode", rt.Bytes(`<!DOCTYPE a [<!ENTITY e SYSTEM "file:///etc/passwd">]><a>&e;</a>`), false)),
		`/3{0=[], 1=[], 2=[Int(1), Int(1), Int(1)], 3=String("a DOCTYPE is refused: std/xml reads no DTD; allow_doctype skips it")}`)
	want("a mismatched end tag read whole, its column in code points", text(call("xml/host.decode", rt.Bytes("<a>\n<b>é</a>"), false)),
		`/3{0=[Int(1), Int(3), Int(1), Int(3)], 1=[[[String(""), String("a"), String("")], [], []], [[String("\n")]], [[String(""), String("b"), String("")], [], []], [[String("é")]]], 2=[Int(1), Int(2), Int(5)], 3=String("<b> is closed by </a>")}`)

	shut := rt.ReaderFromText("<a/>")
	rt.ReaderClose(shut)
	broken := call("xml/host.open", shut, false)
	call("xml/host.next", broken)
	want("a closed reader's failure", text(call("xml/host.failure", broken)), `io.Error/4.Closed{}`)

	want("escaped text", text(call("xml/host.escape_text", "a<&>\r\"\x01")), `String("a&lt;&amp;&gt;&#13;\"�")`)
	want("an escaped attribute", text(call("xml/host.escape_attribute", "a<&>\"\t\n")), `String("a&lt;&amp;>&quot;&#9;&#10;")`)
	want("a name", text(call("xml/host.is_name", "é-1")), `Bool(true)`)
	want("a name with a colon", text(call("xml/host.is_name", "a:b")), `Bool(false)`)
}

func rtStrings(items ...string) *rt.List[any] {
	out := make([]any, len(items))
	for i, s := range items {
		out[i] = s
	}
	return rtList(out...)
}
