package stdhttp_test

import (
	"strings"
	"testing"
)

const wireProgram = `import std/http
import std/http.{Body, FormPart, Headers, Method, Part, Request, Response, Status}
import std/io.{self, Reader, Writer}
import std/url.Url

fn main() {
    request = Request{method: Method.Post, url: Url.parse("http://example.com/a?b=1")!, body: Body.Text("hello")}
    #(_, wire) = Writer.collect(fn w -> Request.write_to(request, w))
    back = Request.read(Reader.from_bytes(wire))!
    io.print("request ${back.method} ${back.url.path} ${back.host} ${Body.read_all(back.body) |> Result.map(fn b -> Bytes.to_string(b)!)}")
    #(_, proxied) = Writer.collect(fn w -> Request.write_to(request, w, proxy: True))
    io.print("proxy ${String.starts_with(Bytes.to_string(proxied)!, "POST http://example.com/a?b=1 HTTP/1.1")}")
    io.print("dump ${Request.dump_outgoing(request, body: False) |> Result.map(fn d -> String.starts_with(d, "POST /a?b=1 HTTP/1.1"))}")

    response = Response{status: Status.created, headers: Headers[#("X-A", "1")], body: Body.Text("made")}
    #(_, rwire) = Writer.collect(fn w -> Response.write_to(response, w))
    rback = Response.read(Reader.from_bytes(rwire), request)!
    io.print("response ${Status.code(rback.status)} ${Headers.get(rback.headers, "X-A")} ${Response.text(rback)}")
    io.print("response dump ${Response.dump(response) |> Result.map(fn d -> String.contains(d, "made"))}")

    #(_, chunked) = Writer.collect(fn w -> {
        cw = http.chunked_writer(w)
        _ = Writer.write_text(cw, "abc")
        Writer.close(cw)
    })
    io.print("chunked ${Reader.read_all_text(http.chunked_reader(Reader.from_bytes(chunked)))}")

    parts = [FormPart.Field{name: "a", value: "one"}, FormPart.File{field: "f", filename: "x.txt", content: String.to_bytes("data")}]
    #(_, form) = Writer.collect(fn w -> http.write_multipart(w, "XYZ", parts))
    upload = Request{
        method: Method.Post,
        url: Url.parse("http://x/")!,
        headers: Headers[#("Content-Type", "multipart/form-data; boundary=XYZ")],
        body: Body.Bytes(form),
    }

    seen = Request.with_parts(upload, fn each -> {
        each |> Iter.map(fn p -> p |> Result.map(fn part -> "${Part.form_name(part)} ${Part.file_name(part)} ${Reader.read_all_text(part.body)}")) |> Iter.to_list()
    })

    io.print("parts ${seen}")
    io.print("form ${http.read_multipart_form(Reader.from_bytes(form), "XYZ") |> Result.map(fn f -> f.values)}")
}
`

// HTTP on the wire: a request and a response written and read back, a
// proxy write, dumps, a chunked stream, and a multipart body written part
// by part and read the same way and whole.
func TestWire_RoundTrips(t *testing.T) {
	got := run(t, wireProgram)
	for _, w := range []string{
		"request POST /a Some(example.com) Ok(hello)",
		"proxy True",
		"dump Ok(True)",
		"response 201 Some(1) Ok(made)",
		"response dump Ok(True)",
		"chunked Ok(abc)",
		`parts Ok([Ok(Some(a) None Ok(one)), Ok(Some(f) Some(x.txt) Ok(data))])`,
		"form Ok(",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}
