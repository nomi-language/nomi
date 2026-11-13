package stdhttp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/rt"
)

// HTTP on the wire: messages read from and written to a program's Reader
// and Writer, httputil's dumps and chunked streams, and multipart bodies
// read and written part by part.

// writerOutput is a Writer as an io.Writer.
type writerOutput struct {
	fr *rt.Frame
	w  rt.Writer
}

func (o writerOutput) Write(p []byte) (int, error) {
	if r := rt.WriterWrite(o.fr, o.w, rt.Bytes(append([]byte(nil), p...))); r.Tag != rt.TagOk {
		return 0, rt.IOFailure{E: r.Err}
	}
	return len(p), nil
}

// byteAtATime reads one byte per Read, so the bufio.Reader Go's parsers
// need takes no byte past the message it reads, and the next message on
// the same Reader is left whole.
type byteAtATime struct{ r io.Reader }

func (b byteAtATime) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return b.r.Read(p[:1])
}

// wireMessage is what a Handle from wire_read_request or
// wire_read_response holds: the message and its body, read whole.
type wireMessage struct {
	req  *http.Request
	resp *http.Response
	body []byte
}

func wireOf(h rt.HTTPHandle) *wireMessage {
	m, ok := h.Value().(*wireMessage)
	if !ok {
		rt.Trap("http: a Handle holds no message")
	}
	return m
}

// WireReadRequest is `std::http::host.wire_read_request`: Go's ReadRequest over
// r, its body read whole. An Err is Go's reason.
func WireReadRequest(fr *rt.Frame, r rt.Reader) rt.Result[rt.HTTPHandle, string] {
	br := bufio.NewReader(byteAtATime{readerInput{fr: fr, r: r}})
	req, err := http.ReadRequest(br)
	if err != nil {
		return rt.Err[rt.HTTPHandle](err.Error())
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return rt.Err[rt.HTTPHandle](err.Error())
	}
	return rt.Ok[rt.HTTPHandle, string](rt.NewHTTPHandle(&wireMessage{req: req, body: body}))
}

// WireReadResponse is `std::http::host.wire_read_response`: Go's ReadResponse
// over r for a request of method, its body read whole.
func WireReadResponse(fr *rt.Frame, r rt.Reader, method string) rt.Result[rt.HTTPHandle, string] {
	br := bufio.NewReader(byteAtATime{readerInput{fr: fr, r: r}})
	resp, err := http.ReadResponse(br, &http.Request{Method: method})
	if err != nil {
		return rt.Err[rt.HTTPHandle](err.Error())
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return rt.Err[rt.HTTPHandle](err.Error())
	}
	return rt.Ok[rt.HTTPHandle, string](rt.NewHTTPHandle(&wireMessage{resp: resp, body: body}))
}

// WireParts is `std::http::host.wire_parts`: [method, target, major, minor, host,
// content length, close, status], then the headers, the trailers and the
// transfer encoding.
func WireParts(h rt.HTTPHandle) *rt.List[*rt.List[string]] {
	m := wireOf(h)
	var head []string
	var hdr, trailer http.Header
	var te []string
	if r := m.req; r != nil {
		head = []string{r.Method, r.RequestURI, strconv.Itoa(r.ProtoMajor), strconv.Itoa(r.ProtoMinor), r.Host,
			strconv.FormatInt(r.ContentLength, 10), strconv.FormatBool(r.Close), "0"}
		hdr, trailer, te = r.Header, r.Trailer, r.TransferEncoding
	} else {
		r := m.resp
		head = []string{"", "", strconv.Itoa(r.ProtoMajor), strconv.Itoa(r.ProtoMinor), "",
			strconv.FormatInt(r.ContentLength, 10), strconv.FormatBool(r.Close), strconv.Itoa(r.StatusCode)}
		hdr, trailer, te = r.Header, r.Trailer, r.TransferEncoding
	}
	return rt.Cons(listOf(head), rt.Cons(headerList(hdr), rt.Cons(headerList(trailer),
		rt.Cons(listOf(te), (*rt.List[*rt.List[string]])(nil)))))
}

// WireBody is `std::http::host.wire_body`.
func WireBody(h rt.HTTPHandle) rt.Bytes { return rt.Bytes(wireOf(h).body) }

func header(flat *rt.List[string]) http.Header {
	h := http.Header{}
	for l := flat; l != nil && l.Tail != nil; l = l.Tail.Tail {
		h.Add(l.Head, l.Tail.Head)
	}
	return h
}

// WireRequest is `std::http::host.wire_request`: a request from its parts,
// written to w (op 0), as a proxy writes it (1), or dumped: as a server
// received it with its body (2) or without (3), or as a client sends it
// with its body (4) or without (5). A dump answers its text.
func WireRequest(fr *rt.Frame, op int64, w rt.Writer, method, target string, headers *rt.List[string], host string,
	contentLength int64, transferEncoding *rt.List[string], closes bool, trailers *rt.List[string], body rt.Bytes) rt.Result[string, string] {
	u, err := url.Parse(target)
	if err != nil {
		return rt.Err[string](err.Error())
	}
	req := &http.Request{Method: method, URL: u, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: header(headers), Host: host, ContentLength: contentLength, TransferEncoding: strsOf(transferEncoding),
		Close: closes, RequestURI: u.RequestURI()}
	if req.Host == "" {
		req.Host = u.Host
	}
	if trailers != nil {
		req.Trailer = header(trailers)
	}
	if len(body) > 0 {
		req.Body = io.NopCloser(bytes.NewReader([]byte(body)))
		if req.ContentLength < 0 && len(req.TransferEncoding) == 0 {
			req.ContentLength = int64(len(body))
		}
	} else {
		req.Body = http.NoBody
		if req.ContentLength < 0 {
			req.ContentLength = 0
		}
	}
	var out []byte
	switch op {
	case 0:
		err = req.Write(writerOutput{fr: fr, w: w})
	case 1:
		err = req.WriteProxy(writerOutput{fr: fr, w: w})
	case 2, 3:
		out, err = httputil.DumpRequest(req, op == 2)
	default:
		out, err = httputil.DumpRequestOut(req, op == 4)
	}
	if err != nil {
		return rt.Err[string](err.Error())
	}
	return rt.Ok[string, string](string(out))
}

// WireResponse is `std::http::host.wire_response`: a response to a request of
// method from its parts, written to w (op 0) or dumped with its body (1)
// or without (2).
func WireResponse(fr *rt.Frame, op int64, w rt.Writer, status, major, minor int64, headers *rt.List[string],
	contentLength int64, transferEncoding *rt.List[string], closes bool, trailers *rt.List[string], body rt.Bytes,
	method string) rt.Result[string, string] {
	resp := &http.Response{StatusCode: int(status), ProtoMajor: int(major), ProtoMinor: int(minor),
		Proto:  "HTTP/" + strconv.FormatInt(major, 10) + "." + strconv.FormatInt(minor, 10),
		Header: header(headers), ContentLength: contentLength, TransferEncoding: strsOf(transferEncoding),
		Close: closes, Request: &http.Request{Method: method}, Body: io.NopCloser(bytes.NewReader([]byte(body)))}
	if trailers != nil {
		resp.Trailer = header(trailers)
	}
	if resp.ContentLength < 0 && len(resp.TransferEncoding) == 0 {
		resp.ContentLength = int64(len(body))
	}
	var out []byte
	var err error
	switch op {
	case 0:
		err = resp.Write(writerOutput{fr: fr, w: w})
	default:
		out, err = httputil.DumpResponse(resp, op == 1)
	}
	if err != nil {
		return rt.Err[string](err.Error())
	}
	return rt.Ok[string, string](string(out))
}

// ChunkedReader is `std::http::host.chunked_reader`: httputil's NewChunkedReader
// over r.
func ChunkedReader(fr *rt.Frame, r rt.Reader) rt.Reader {
	return rt.NewReader(rt.ReadSource{Name: "<chunked>", R: httputil.NewChunkedReader(readerInput{fr: fr, r: r})})
}

// ChunkedWriter is `std::http::host.chunked_writer`: httputil's
// NewChunkedWriter over w; closing it writes the last, empty chunk.
func ChunkedWriter(fr *rt.Frame, w rt.Writer) rt.Writer {
	cw := httputil.NewChunkedWriter(writerOutput{fr: fr, w: w})
	return rt.NewWriter(rt.WriteSink{Name: "<chunked>", W: cw, Close: cw.Close})
}

// partsReader is what a Handle from multipart_reader holds.
type partsReader struct {
	r   *multipart.Reader
	raw bool
}

// MultipartReader is `std::http::host.multipart_reader`: Go's multipart.Reader
// over r with boundary; with raw, parts are not decoded
// (NextRawPart).
func MultipartReader(fr *rt.Frame, r rt.Reader, boundary string, raw bool) rt.HTTPHandle {
	return rt.NewHTTPHandle(&partsReader{r: multipart.NewReader(readerInput{fr: fr, r: r}, boundary), raw: raw})
}

// NextPart is `std::http::host.next_part`: the next part as a Handle, None after
// the last. Reading it closes the part before.
func NextPart(h rt.HTTPHandle) rt.Result[rt.Maybe[rt.HTTPHandle], string] {
	pr, ok := h.Value().(*partsReader)
	if !ok {
		rt.Trap("http: a Handle holds no multipart reader")
	}
	var p *multipart.Part
	var err error
	if pr.raw {
		p, err = pr.r.NextRawPart()
	} else {
		p, err = pr.r.NextPart()
	}
	if errors.Is(err, io.EOF) {
		return rt.Ok[rt.Maybe[rt.HTTPHandle], string](rt.None[rt.HTTPHandle]())
	}
	if err != nil {
		return rt.Err[rt.Maybe[rt.HTTPHandle]](err.Error())
	}
	return rt.Ok[rt.Maybe[rt.HTTPHandle], string](rt.Some(rt.NewHTTPHandle(p)))
}

func partOf(h rt.HTTPHandle) *multipart.Part {
	p, ok := h.Value().(*multipart.Part)
	if !ok {
		rt.Trap("http: a Handle holds no part")
	}
	return p
}

// PartHeaders is `std::http::host.part_headers`.
func PartHeaders(h rt.HTTPHandle) *rt.List[string] { return headerList(http.Header(partOf(h).Header)) }

// PartReader is `std::http::host.part_reader`: the part's body.
func PartReader(h rt.HTTPHandle) rt.Reader {
	return rt.NewReader(rt.ReadSource{Name: "<part>", R: partOf(h)})
}

// PartFormName is `std::http::host.part_form_name`: "" when it has none.
func PartFormName(h rt.HTTPHandle) string { return partOf(h).FormName() }

// PartFileName is `std::http::host.part_file_name`: "" when it has none.
func PartFileName(h rt.HTTPHandle) string { return partOf(h).FileName() }

// MultipartWrite is `std::http::host.multipart_write`: parts, as EncodeMultipart
// takes them, written to w with boundary as they go, then the closing
// boundary.
func MultipartWrite(fr *rt.Frame, w rt.Writer, boundary string, parts *rt.List[rt.Bytes]) rt.Result[rt.Unit, string] {
	mw := multipart.NewWriter(writerOutput{fr: fr, w: w})
	if err := mw.SetBoundary(boundary); err != nil {
		return rt.Err[rt.Unit](err.Error())
	}
	writeParts(mw, listItems(parts))
	if err := mw.Close(); err != nil {
		return rt.Err[rt.Unit](strings.TrimSpace(err.Error()))
	}
	return rt.Ok[rt.Unit, string](rt.Unit{})
}
