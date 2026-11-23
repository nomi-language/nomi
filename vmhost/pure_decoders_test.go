package vmhost_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

// decoders is a module of functions that each call std functions whose
// operands and result are plain values and which make no handle that
// outlives the call, and what each answers. A request's body is read
// through the stream the request holds, which is no effect, or from the
// bytes it holds.
var decoders = struct {
	src   string
	calls []decoderCall
}{
	src: `import
    std::csv
    std::http.{self, Body, FormPart, Headers, Method, Request, Response}
    std::io.Reader
    std::url.{Query, Url}
    std::xml
end

pub fn csv_decode() -> Result<List<List<String>>, csv.Error>
    csv.decode("a,b\n\"c\nd\",e\n")
end

pub fn csv_decode_bytes() -> Result<List<List<String>>, csv.Error>
    csv.decode_bytes(String.to_bytes("a,\"b"))
end

pub fn csv_decode_rows() -> Result<List<Int>, csv.Error>
    rows = csv.decode_rows("name,age\n\nAda,36\n\"Al\nan\",41\n")?
    Ok(rows |> Iter.map(.line) |> Iter.to_list())
end

pub fn xml_decode() -> Result<String, xml.Error>
    root = xml.decode("<?xml version='1.0'?><!-- c --><a x='1'>t<b/>u<?p d?></a>")?
    xml.encode(root)
end

pub fn xml_decode_bytes() -> Result<xml.Element, xml.Error>
    xml.decode_bytes(String.to_bytes("<a>\n<b>é</a>"))
end

fn posted(content_type: String, body: Body) -> Request
    {
        method:  Method.Post,
        url:     Url"https://example.com/?c=3",
        headers: Headers[("Content-Type", content_type)],
        body:,
    }
end

fn form() -> Request
    posted("application/x-www-form-urlencoded", Body.Text("a=1&b=2"))
end

fn upload() -> Request
    parts = [
        FormPart.Field{name: "note", value: "hi"},
        FormPart.File{field: "doc", filename: "a.txt", content: String.to_bytes("abc")},
    ]

    posted(FormPart.encode(parts).content_type, Body.Multipart(parts))
end

pub fn http_text() -> Result<String, http.Error>
    Request.text(posted("text/plain", Body.Text("hello")))
end

pub fn http_too_long() -> Result<String, http.Error>
    Request.text({..posted("text/plain", Body.Text("hello")), body_limit: Some(4)})
end

pub fn http_form() -> Result<Option<String>, http.Error>
    Request.form(form()) |> Result.map(fn q -> Query.get(q, "c"))
end

pub fn http_form_values() -> (Option<String>, Option<String>)
    (Request.form_value(form(), "a"), Request.post_form_value(form(), "b"))
end

pub fn http_multipart_form() -> Result<Option<String>, http.Error>
    Request.multipart_form(upload()) |> Result.map(fn f -> Query.get(f.values, "note"))
end

pub fn http_form_file() -> Result<String, http.Error>
    Request.form_file(upload(), "doc") |> Result.map(.filename)
end

pub fn http_json_as() -> Result<Map<String, Int>, http.Error>
    Request.json_as(posted("application/json", Body.Text(` + "`" + `{"a": 1}` + "`" + `)))
end

pub fn http_read_multipart_form(r: Reader) -> Result<Option<String>, http.Error>
    http.read_multipart_form(r, "b") |> Result.map(fn f -> Query.get(f.values, "note"))
end

pub fn http_method_override() -> Result<String, http.Error>
    app = http.method_override(fn r -> Response{body: Body.Text(Method.text(r.method))})
    Response.text(app(posted("application/x-www-form-urlencoded", Body.Text("_method=delete"))))
end
`,
	calls: []decoderCall{
		{"csv_decode", nil, `Ok([["a", "b"], ["c\nd", "e"]])`},
		{"csv_decode_bytes", nil, `Err(Syntax{column: 5, line: 1, message: "a quoted field is not closed, or has a quote in it that is not doubled"})`},
		{"csv_decode_rows", nil, `Ok([3, 4])`},
		{"xml_decode", nil, `Ok("<a x=\"1\">t<b/>u<?p d?></a>")`},
		{"xml_decode_bytes", nil, `Err(Syntax{column: 5, line: 2, message: "<b> is closed by </a>"})`},
		{"http_text", nil, `Ok("hello")`},
		{"http_too_long", nil, `Err(BodyTooLarge{limit: 4})`},
		{"http_form", nil, `Ok(Some("3"))`},
		{"http_form_values", nil, `(Some("1"), Some("2"))`},
		{"http_multipart_form", nil, `Ok(Some("hi"))`},
		{"http_form_file", nil, `Ok("a.txt")`},
		{"http_json_as", nil, `Ok({"a" => 1})`},
		{"http_read_multipart_form", []any{rt.ReaderFromText("--b\r\nContent-Disposition: form-data; name=\"note\"\r\n\r\nhi\r\n--b--\r\n")}, `Ok(Some("hi"))`},
		{"http_method_override", nil, `Ok("DELETE")`},
	},
}

type decoderCall struct {
	fn   string
	args []any
	want string
}

// Each decoder has no effect: a module whose list is empty calls it, and
// it answers as it does over a stream.
func TestEffects_DecodersOfPlainValuesHaveNone(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"nomi.toml":     "[module]\nname = \"app\"\n\n[effects.self]\nstd = []\n",
		"decoders.nomi": decoders.src,
	})
	p, err := vmhost.Load(filepath.Join(dir, "decoders.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range decoders.calls {
		v, err := p.Call(context.Background(), c.fn, c.args...)
		var ce *vmhost.EffectError
		switch {
		case errors.As(err, &ce):
			t.Errorf("%s has an effect: %v", c.fn, err)
		case err != nil:
			t.Errorf("%s: %v", c.fn, err)
		case rt.DisplayText(v) != c.want:
			t.Errorf("%s = %s, want %s", c.fn, rt.DisplayText(v), c.want)
		}
	}
}

// A refinement's rule may decode text, since decoding has no effect: a
// literal is checked by decoding it when the program is compiled.
func TestEffects_ARuleMayDecodeText(t *testing.T) {
	const decls = `import
    std::csv
    std::xml
end

refine CsvText String when fn text -> csv.decode(text) |> Result.is_ok()

refine XmlText String when fn text -> xml.decode(text) |> Result.is_ok()

`
	if _, err := vmhost.LoadSource("main", decls+"fn main()\n    _: CsvText = \"a,b\\n1,2\\n\"\n    _: XmlText = \"<a>b</a>\"\nend\n"); err != nil {
		t.Fatalf("literals that decode: %v", err)
	}
	for _, c := range []struct{ literal, want string }{
		{"_: CsvText = \"a,\\\"b\"", "is not a CsvText: `fn text -> csv.decode(text) |> Result.is_ok()` is false"},
		{"_: XmlText = \"<a>\"", "is not a XmlText: `fn text -> xml.decode(text) |> Result.is_ok()` is false"},
	} {
		_, err := vmhost.LoadSource("main", decls+"fn main()\n    "+c.literal+"\nend\n")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error reading %q", c.literal, err, c.want)
		}
	}
}
