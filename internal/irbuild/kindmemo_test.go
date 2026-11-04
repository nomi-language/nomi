package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// kindMemoHTTPProgram is the HTTP chapter's first tour block: it reaches
// std/http, std/http_testing, std/url and the program std modules they
// import, whose lowering asks the retention predicates most of their
// questions.
const kindMemoHTTPProgram = `import {
    std::http.{self, Response, Status}
    std::http_testing
    std::url.Url
}

fn greeting(name: String) -> Result<String, http.Error> {
    response = http.get(Url"https://api.example.com/greet/${name}")?
    if not Status.is_success(response.status) { return Ok("no greeting: ${response.status}") }
    Response.text(response)
}

fn main() {
    ran = http_testing.run(fn request -> {
        Ok(Response{body: .Text("hello from ${request.url.path}")})
    }, fn -> {
        greeting("ann b")
    })

    dbg ran.value
}
`

// TestKindMemo_AnswersMostQueriesWithoutAWalk holds the retention
// predicates' memo (kindmemo.go) to its savings, by counting: lowering the
// HTTP block asks the predicates hundreds of thousands of times, and at most
// a quarter of those may walk the type. Without the memo every query walks,
// and each walk asks again about every type it reaches. Under `go test` the
// check re-walks each memoized answer, and its walks' questions count as
// queries, so the count here is what lowering without the memo asks; the
// walks are the memo's own.
//
// Sequential, not t.Parallel: the counters are process-wide.
func TestKindMemo_AnswersMostQueriesWithoutAWalk(t *testing.T) {
	stdlibLowering()
	p, err := AnalyzeVirtual("main", kindMemoHTTPProgram, frontend.Config{})
	if err != nil {
		t.Fatalf("the front end rejects the HTTP block: %v", err)
	}
	queries, walks := kindMemoQueries.Load(), kindMemoWalks.Load()
	if _, _, err := GenerateIR(p); err != nil {
		t.Fatal(err)
	}
	queries, walks = kindMemoQueries.Load()-queries, kindMemoWalks.Load()-walks
	t.Logf("%d queries, %d walks", queries, walks)
	// The positive control: the block's types are asked about, so a small
	// count means the counter is not counting.
	if queries < 1000 {
		t.Fatalf("lowering the HTTP block made %d retention queries; kindMemoQueries is not counting, so this test measures nothing", queries)
	}
	if walks*4 > queries {
		t.Fatalf("lowering the HTTP block walked for %d of %d retention queries; the budget is a quarter.\n"+
			"Look for a changingTypes bracket around code that writes no existing typeDef (a constructor of a new one "+
			"takes constructingType), or one opened per query rather than per change (internal/irbuild/kindmemo.go).",
			walks, queries)
	}
}

// TestKindMemo_TheCheckCatchesAWriteOutsideABracket plants a write to a
// typeDef outside any bracket and requires the check `go test` runs on every
// memoized answer to report it, and a write inside a bracket to pass.
func TestKindMemo_TheCheckCatchesAWriteOutsideABracket(t *testing.T) {
	if !kindMemoVerify {
		t.Fatal("kindMemoVerify is off under go test")
	}
	d := &typeDef{nomi: "Planted", lowerable: true}
	stale := func() (msg string) {
		defer func() {
			if r := recover(); r != nil {
				msg, _ = r.(string)
			}
		}()
		irRetainedStructKind(d)
		return ""
	}
	// Another test's bracket in the window advances the epoch and turns the
	// planted stale answer into a miss, so the plant is retried.
	for attempt := 0; attempt < 20; attempt++ {
		if !irRetainedStructKind(d) {
			t.Fatal("a field-less lowerable struct is not retained")
		}
		epoch := typeEpoch.Load()
		d.lowerable = false
		msg := stale()
		done := changingTypes()
		d.lowerable = true
		done()
		if msg != "" {
			if !strings.Contains(msg, "outside a changingTypes bracket") {
				t.Fatalf("the check panicked with %q", msg)
			}
			if !irRetainedStructKind(d) {
				t.Fatal("after a bracketed write the memo still answers the stale value")
			}
			return
		}
		if typeEpoch.Load() == epoch {
			t.Fatal("a write outside a bracket went unreported while no bracket opened")
		}
	}
	t.Fatal("every attempt was invalidated by another bracket; the check was never exercised")
}
