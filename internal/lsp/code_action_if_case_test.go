package lsp

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/format"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const (
	toCase   = "Convert to case"
	toIfElse = "Convert to if/else"
)

// assertFormatted fails unless src, without its marker, is nomi fmt output.
func assertFormatted(t *testing.T, src string) {
	t.Helper()
	clean := strings.Replace(src, "‸", "", 1)
	if out, err := format.Format(clean); err != nil || out != clean {
		t.Fatalf("source is not nomi fmt output (err %v); formatted:\n%s", err, out)
	}
}

// convertIfCase applies title at src's marker and asserts the result.
func convertIfCase(t *testing.T, src, title, want string) string {
	t.Helper()
	assertFormatted(t, src)
	got := checkRefactor(t, src, title, protocol.CodeActionKindRefactorRewrite)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	return got
}

// ifCaseRoundTrip converts src, its marker at the start of an `if` or
// `case` keyword, with there, then the result back with back from the same
// offset, and asserts it is src again.
func ifCaseRoundTrip(t *testing.T, src, there, back string) {
	t.Helper()
	assertFormatted(t, src)
	at := strings.Index(src, "‸")
	clean := strings.Replace(src, "‸", "", 1)
	mid := checkRefactor(t, src, there, protocol.CodeActionKindRefactorRewrite)
	// The formatter may add blank lines above the rewritten node.
	kw := "case"
	if there == toIfElse {
		kw = "if"
	}
	at += strings.Index(mid[at:], kw+" ")
	again := checkRefactor(t, mid[:at]+"‸"+mid[at:], back, protocol.CodeActionKindRefactorRewrite)
	if again != clean {
		t.Errorf("round trip:\n%s\nvia:\n%s\nback:\n%s", clean, mid, again)
	}
}

func TestIfCase_ChainToSubjectlessCase(t *testing.T) {
	src := fnMain(`x = 3

label = ‸if x > 10 {
    "big"
} else if x > 0 {
    "positive"
} else {
    "other"
}

io.print(label)`)
	convertIfCase(t, src, toCase, fnMain(`x = 3

label = case {
    x > 10 -> "big"
    x > 0 -> "positive"
    _ -> "other"
}

io.print(label)`))
}

func TestIfCase_SubjectlessCaseToChain(t *testing.T) {
	src := fnMain(`x = 3

label = ‸case {
    x > 10 -> "big"
    x > 0 -> "positive"
    _ -> "other"
}

io.print(label)`)
	convertIfCase(t, src, toIfElse, fnMain(`x = 3

label = if x > 10 {
    "big"
} else if x > 0 {
    "positive"
} else {
    "other"
}

io.print(label)`))
}

func TestIfCase_RoundTrips(t *testing.T) {
	cases := map[string]struct{ src, there, back string }{
		"chain": {fnMain(`x = 3

‸if x > 10 {
    io.print("big")
} else if x > 0 {
    io.print("positive")
} else {
    io.print("other")
}`), toCase, toIfElse},
		"case to chain": {fnMain(`x = 3

‸case {
    x > 10 -> io.print("big")
    x > 0 -> io.print("positive")
    _ -> io.print("other")
}`), toIfElse, toCase},
		"block arms": {fnMain(`x = 3

label = ‸case {
    x > 10 -> {
        y = x * 2
        "big ${y}"
    }

    _ ->
        "small"
}

io.print(label)`), toIfElse, toCase},
		"unit no else": {fnMain(`x = 3

‸if x > 10 { io.print("big") }`), toCase, toIfElse},
		"pattern if": {fnMain(`m = Some(3)

n = ‸if Some(v) = m { v } else { 0 }

io.print(n)`), toCase, toIfElse},
		"pattern case": {fnMain(`m = Some(3)

n = ‸case m {
    Some(v) -> v
    _ -> 0
}

io.print(n)`), toIfElse, toCase},
		"pattern if unit": {fnMain(`m = Some(3)

‸if Some(v) = m { io.print(v) }`), toCase, toIfElse},
		"pattern if else if": {fnMain(`m = Some(3)
x = 2

n = ‸if Some(v) = m {
    v
} else if x > 1 {
    x
} else {
    0
}

io.print(n)`), toCase, toIfElse},
		"literal case": {fnMain(`n = 2

label = ‸case n {
    1 -> "one"
    -2 -> "minus two"
    _ -> "many"
}

io.print(label)`), toIfElse, "Convert to case on 'n'"},
		"literal string field": {fnMainAfter("struct P {\n    name: String\n}\n\n", `p = P{name: "a"}

‸if p.name == "a" {
    io.print(1)
} else if p.name == "b" {
    io.print(2)
}`), "Convert to case on 'p.name'", toIfElse},
		"in a lambda": {fnMain(`xs = [1, 2, 3]

Iter.each(xs, |x| {
    y = x + 1

    ‸if y > 1 { io.print(x) } else { io.print(0) }
})`), toCase, toIfElse},
		"control flow arms": {fnMain(`xs = [1, 2, 3]

Iter.each(xs, |x| {
    y = x + 1

    ‸if y == 2 { continue } else { io.print(x) }
})`), toCase, toIfElse},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { ifCaseRoundTrip(t, c.src, c.there, c.back) })
	}
}

func TestIfCase_LiteralChainToCase(t *testing.T) {
	src := fnMain(`n = 2

label = ‸if n == 1 {
    "one"
} else if n == -2 {
    "minus two"
} else {
    "many"
}

io.print(label)`)
	convertIfCase(t, src, "Convert to case on 'n'", fnMain(`n = 2

label = case n {
    1 -> "one"
    -2 -> "minus two"
    _ -> "many"
}

io.print(label)`))
	// The same chain also converts to a subject-less case.
	convertIfCase(t, src, toCase, fnMain(`n = 2

label = case {
    n == 1 -> "one"
    n == -2 -> "minus two"
    _ -> "many"
}

io.print(label)`))
}

func TestIfCase_PatternCaseToIf(t *testing.T) {
	src := fnMain(`m = Some(3)

n = ‸case m {
    Some(v) -> v
    _ -> 0
}

io.print(n)`)
	convertIfCase(t, src, toIfElse, fnMain(`m = Some(3)

n = if Some(v) = m { v } else { 0 }

io.print(n)`))
}

func TestIfCase_NestedInArm(t *testing.T) {
	src := fnMain(`x = 3
m = Some(1)

label = case m {
    Some(v) -> ‸if v > x { "more" } else { "less" }
    None -> "none"
}

io.print(label)`)
	convertIfCase(t, src, toCase, fnMain(`x = 3
m = Some(1)

label = case m {
    Some(v) ->
        case {
            v > x -> "more"
            _ -> "less"
        }

    None ->
        "none"
}

io.print(label)`))
}

func TestIfCase_ElseIfRungHeaderConvertsWholeChain(t *testing.T) {
	src := fnMain(`x = 3

label = if x > 10 {
    "big"
} el‸se if x > 0 {
    "positive"
} else {
    "other"
}

io.print(label)`)
	convertIfCase(t, src, toCase, fnMain(`x = 3

label = case {
    x > 10 -> "big"
    x > 0 -> "positive"
    _ -> "other"
}

io.print(label)`))
}

func TestIfCase_CommentsKept(t *testing.T) {
	src := fnMain(`x = 3

label = ‸if x > 10 {
    // large
    "big"
} else {
    "other" // fallback
}

io.print(label)`)
	ifCaseRoundTrip(t, src, toCase, toIfElse)
	convertIfCase(t, src, toCase, fnMain(`x = 3

label = case {
    x > 10 -> {
        // large
        "big"
    }

    _ -> {
        "other" // fallback
    }
}

io.print(label)`))

	// An arm's own comments move into the branch block.
	convertIfCase(t, fnMain(`x = 3

label = ‸case {
    // large
    x > 10 -> "big"
    _ -> "other" // fallback
}

io.print(label)`), toIfElse, fnMain(`x = 3

label = if x > 10 {
    // large
    "big"
} else {
    "other" // fallback
}

io.print(label)`))
}

func TestIfCase_Refusals(t *testing.T) {
	cases := map[string]string{
		"in the body": fnMain(`x = 3

label = if x > 10 { "bi‸g" } else { "other" }

io.print(label)`),
		"guard": fnMain(`m = Some(3)

label = ‸case m {
    Some(v) when v > 1 -> "big"
    _ -> "other"
}

io.print(label)`),
		"binding arm": fnMain(`n = 3

label = ‸case n {
    1 -> "one"
    other -> "${other}"
}

io.print(label)`),
		"three pattern arms": fnMain(`r: Result<Int, String> = Ok(1)

label = ‸case r {
    Ok(1) -> "one"
    Ok(_) -> "other"
    Err(_) -> "err"
}

io.print(label)`),
		"effectful subject": fnMainAfter("fn next(): Int {\n    3\n}\n\n", `label = ‸case next() {
    1 -> "one"
    _ -> "many"
}

io.print(label)`),
		"float literal": fnMain(`f = 1.5

label = ‸case f {
    1.5 -> "x"
    _ -> "y"
}

io.print(label)`),
		"pattern rung later": fnMain(`x = 3
m = Some(1)

label = ‸if x > 1 {
    "a"
} else if Some(v) = m {
    "${v}"
} else {
    "c"
}

io.print(label)`),
		"pipe stage case": fnMain(`m = Some(3)

n = m |> ‸case {
            Some(v) -> v
            _ -> 0
        }

io.print(n)`),
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			assertFormatted(t, src)
			refuseRefactor(t, src, toCase)
			refuseRefactor(t, src, toIfElse)
		})
	}
}

func TestIfCase_LiteralCaseNeedsOneSubject(t *testing.T) {
	s := NewServer()
	actions, _ := refactorActions(t, s, fnMain(`a = 1
b = 2

label = ‸if a == 1 {
    "x"
} else if b == 2 {
    "y"
} else {
    "z"
}

io.print(label)`))
	titles := actionTitles(actions)
	joined := strings.Join(titles, "|")
	if strings.Contains(joined, "case on") || !strings.Contains(joined, toCase) {
		t.Errorf("offered %q", titles)
	}
}

func TestIfCase_RefusedWhenACommentWouldBeLost(t *testing.T) {
	// The comment after the last arm belongs to the case; an if chain has
	// no place for it.
	src := fnMain(`x = 3

label = ‸case {
    x > 10 -> "big"
    _ -> "other"
    // end
}

io.print(label)`)
	assertFormatted(t, src)
	refuseRefactor(t, src, toIfElse)
}

func TestIfCase_StructLiteralConditionRefused(t *testing.T) {
	// `p == P{x: 1}` is a condition a case arm can hold and an `if` cannot:
	// in an `if` condition the `{` opens the branch.
	src := fnMainAfter("struct P {\n    x: Int\n}\n\n", `p = P{x: 1}

label = ‸case {
    p == P{x: 1} -> "one"
    _ -> "other"
}

io.print(label)`)
	assertFormatted(t, src)
	refuseRefactor(t, src, toIfElse)
}
