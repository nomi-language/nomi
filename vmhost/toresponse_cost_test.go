package vmhost_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A handler that answers a Response, held where std::http expects a
// `(Request) -> ToResponse`, is the function itself, with no adapter
// closure around it (irfuncwiden.go): calling it and converting what it
// answers costs one allocation per request more than calling a `(Request)
// -> Response`, the dispatch of `to_response`. Counted per call as the
// difference between 2000 calls and 1000.
func TestToResponse_AResponseHandlerCostsOneAllocationPerCall(t *testing.T) {
	perCall := func(call string) float64 {
		allocs := func(n int) float64 {
			src := fmt.Sprintf(`import std::http.{Body, Method, Request, Response, ToResponse}
import std::http_testing

fn hello(_request: Request) -> Response {
    Response{body: Body.Text("hi")}
}

fn loop(request: Request, n: Int, wide: (Request) -> ToResponse, narrow: (Request) -> Response) -> Int {
    if n == 0 {
        0
    } else {
        _ = %s
        loop(request, n - 1, wide, narrow)
    }
}

test "answers" {
    assert loop(http_testing.request(Method.Get, "/"), %d, hello, hello) == 0
}
`, call, n)
			p, err := vmhost.Load(writeProgram(t, src))
			if err != nil {
				t.Fatalf("the front end rejects this: %v", err)
			}
			return testing.AllocsPerRun(5, func() {
				if r := p.Cases(&bytes.Buffer{}, vmhost.TestOptions{}); r[0].Err != nil {
					t.Fatal(r[0].Err)
				}
			})
		}
		return (allocs(2000) - allocs(1000)) / 1000
	}
	narrow := perCall("narrow(request)")
	wide := perCall("ToResponse.to_response(wide(request))")
	t.Logf("narrow %.2f, wide %.2f allocations per call", narrow, wide)
	if wide-narrow > 1.05 {
		t.Errorf("a Response handler through ToResponse costs %.2f allocations per call, %.2f more than through Response", wide, wide-narrow)
	}
}
