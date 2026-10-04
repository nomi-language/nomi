package irbuild

import (
	"strings"
	"testing"
)

// A `try` whose boundary is a walk-only test body retains, and an Err or None
// ends the case with an early-return report: the try's line,
// its text as the formatter renders it (the whole pipe for the pipe spelling)
// and the propagated variant inspected structurally.
func TestIRTestBody_TryEndsTheCaseEarly(t *testing.T) {
	const src = `fn parse(n: Int): Result<Int, String> {
  if n < 0 {
    Err("negative")
  } else {
    Ok(n)
  }
}

fn half(n: Int): Maybe<Int> {
  if n % 2 == 0 {
    Some(n / 2)
  } else {
    None
  }
}

test "an Ok passes through" {
  v = try parse(3)
  assert v == 3
}

test "an Err ends the case" {
  v = try parse(-1)
  assert v == 1
}

test "a None ends the case" {
  h = try half(3)
  assert h == 1
}

test "a piped try" {
  v = -1 |> try parse()
  assert v == 0
}

test "a try after an assertion that passed" {
  assert 1 == 1
  _ = try half(4)
  _ = try half(5)
  assert 2 == 3
}
`
	out := irTestBodyVM(t, src, 5)
	for _, want := range []string{
		"test returned early",
		`Err("negative")`,
		"None",
		"-1 |> try parse()",
		"try half(5)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
}

// A try over an AssertionFailure payload reports the failed check rather than
// an early return, which the VM runner does not build, so the case is not
// retained.
func TestIRTestBody_TryOverACheckKeepsNative(t *testing.T) {
	const src = `import std/testing

test "a try over a check" {
  passed = try testing.check(1 == 2)
  assert passed
}
`
	irTestBodyVM(t, src, 0)
}
