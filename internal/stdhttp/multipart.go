package stdhttp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"sort"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/rt"
)

// Multipart forms cross as flat lists of Bytes, every element UTF-8 text
// but a file's content:
//
//	a field: "v", name, value
//	a file:  "f", field, file name, header count, each header's name and
//	         value, content
//
// ParseMultipart answers that list; EncodeMultipart takes a request's
// parts as "v", name, value / "f", field, file name, content type, content
// / "r", header count, names and values, content.

func bytesList(items []rt.Bytes) *rt.List[rt.Bytes] {
	var l *rt.List[rt.Bytes]
	for i := len(items) - 1; i >= 0; i-- {
		l = rt.Cons(items[i], l)
	}
	return l
}

func listItems(l *rt.List[rt.Bytes]) []rt.Bytes {
	var out []rt.Bytes
	for ; l != nil; l = l.Tail {
		out = append(out, l.Head)
	}
	return out
}

// ParseMultipart is `std::http::host.parse_multipart`: the multipart/form-data
// body data under contentType, Go's Request.ParseMultipartForm over bytes
// already read. An Err is the reason, Go's text.
func ParseMultipart(contentType string, data rt.Bytes) rt.Result[*rt.List[rt.Bytes], string] {
	fail := func(err error) rt.Result[*rt.List[rt.Bytes], string] {
		return rt.Err[*rt.List[rt.Bytes]](err.Error())
	}
	media, params, err := mime.ParseMediaType(contentType)
	if contentType == "" || err != nil || media != "multipart/form-data" {
		return fail(errors.New("request Content-Type isn't multipart/form-data"))
	}
	boundary, ok := params["boundary"]
	if !ok {
		return fail(errors.New("no multipart boundary param in Content-Type"))
	}
	form, err := multipart.NewReader(strings.NewReader(string(data)), boundary).ReadForm(int64(len(data)) + 1)
	if err != nil {
		return fail(err)
	}
	defer form.RemoveAll()
	var out []rt.Bytes
	text := func(s string) { out = append(out, rt.Bytes(s)) }
	for _, name := range sortedKeys(form.Value) {
		for _, v := range form.Value[name] {
			text("v")
			text(name)
			text(v)
		}
	}
	for _, field := range sortedKeys(form.File) {
		for _, fh := range form.File[field] {
			f, err := fh.Open()
			if err != nil {
				return fail(err)
			}
			content, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				return fail(err)
			}
			text("f")
			text(field)
			text(fh.Filename)
			names := sortedKeys(fh.Header)
			var headers []string
			for _, k := range names {
				for _, v := range fh.Header[k] {
					headers = append(headers, k, v)
				}
			}
			text(strconv.Itoa(len(headers) / 2))
			for _, h := range headers {
				text(h)
			}
			out = append(out, rt.Bytes(content))
		}
	}
	return rt.Ok[*rt.List[rt.Bytes], string](bytesList(out))
}

// EncodeMultipart is `std::http::host.encode_multipart`: a multipart/form-data
// body of parts, answered as its Content-Type and its bytes. The boundary
// is a digest of the parts, so encoding the same parts twice answers the
// same bytes; it is Go's multipart.Writer otherwise.
func EncodeMultipart(parts *rt.List[rt.Bytes]) *rt.List[rt.Bytes] {
	items := listItems(parts)
	sum := sha256.New()
	for _, it := range items {
		sum.Write([]byte(strconv.Itoa(len(it))))
		sum.Write([]byte(it))
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.SetBoundary(hex.EncodeToString(sum.Sum(nil))[:30])
	writeParts(w, items)
	_ = w.Close()
	return bytesList([]rt.Bytes{rt.Bytes(w.FormDataContentType()), rt.Bytes(body.Bytes())})
}

// FileContentDisposition is `std::http::host.file_content_disposition`: Go's
// multipart.FileContentDisposition.
func FileContentDisposition(field, filename string) string {
	return multipart.FileContentDisposition(field, filename)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// writeParts writes items, parts as EncodeMultipart takes them, to w.
func writeParts(w *multipart.Writer, items []rt.Bytes) {
	for i := 0; i < len(items); {
		switch string(items[i]) {
		case "v":
			_ = w.WriteField(string(items[i+1]), string(items[i+2]))
			i += 3
		case "f":
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", multipart.FileContentDisposition(string(items[i+1]), string(items[i+2])))
			h.Set("Content-Type", string(items[i+3]))
			pw, _ := w.CreatePart(h)
			_, _ = pw.Write([]byte(items[i+4]))
			i += 5
		case "r":
			n, _ := strconv.Atoi(string(items[i+1]))
			h := textproto.MIMEHeader{}
			for j := 0; j < n; j++ {
				h.Add(string(items[i+2+2*j]), string(items[i+3+2*j]))
			}
			pw, _ := w.CreatePart(h)
			_, _ = pw.Write([]byte(items[i+2+2*n]))
			i += 3 + 2*n
		default:
			rt.Trap("http: a multipart part of unknown kind " + strings.TrimSpace(string(items[i])))
		}
	}
}
