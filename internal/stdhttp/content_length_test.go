package stdhttp

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// A response that carries its own Content-Length, as Response.content's
// does (Go's ServeContent sets one), goes out with one Content-Length line.
// The server once wrote its own and then added the response's, and a proxy
// that refuses a repeated Content-Length (envoy) reset every such response.
// A HEAD answer, with no body the server measures, keeps the response's.
func TestRespond_WritesOneContentLength(t *testing.T) {
	defer rt.ResetSupervisors()
	durations := rt.Cons(int64(0), rt.Cons(int64(0), rt.Cons(int64(0), rt.Cons(int64(0), rt.Cons(int64(5*time.Second), (*rt.List[int64])(nil))))))
	flags := rt.Cons(true, rt.Cons(true, rt.Cons(true, (*rt.List[bool])(nil))))
	res := Listen("127.0.0.1:0", durations, nil, flags, nil, false, nil, nil, nil, false, nil, false, nil, false, nil, false)
	if res.Tag != 1 {
		t.Fatalf("Listen failed: %v", res.Err)
	}
	l := res.Ok
	go func() {
		for {
			next := Next(nil, l)
			if next.Tag != 1 {
				return
			}
			in := incomingOf(next.Some)
			headers := listOf([]string{"Content-Type", "text/plain", "content-length", "5"})
			if in.r.Method == "HEAD" {
				Respond(nil, next.Some, 200, headers, nil, 0, rt.Bytes(""), rt.Reader{}, -1, 0, false)
			} else {
				Respond(nil, next.Some, 200, headers, nil, bodyBytes, rt.Bytes("hello"), rt.Reader{}, -1, 0, false)
			}
			Finish(next.Some, nil)
		}
	}()

	for _, method := range []string{"GET", "HEAD"} {
		conn, err := net.Dial("tcp", Address(l))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(conn, method+" /f HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
		r := bufio.NewReader(conn)
		var lengths []string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("%s: reading the head: %v", method, err)
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				lengths = append(lengths, line)
			}
		}
		conn.Close()
		if len(lengths) != 1 || lengths[0] != "Content-Length: 5" {
			t.Errorf("%s: Content-Length lines on the wire = %q, want one, of 5", method, lengths)
		}
	}
}
