package frontend_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

func checkSource(t *testing.T, src string) error {
	t.Helper()
	_, err := frontend.New(frontend.Config{}).CheckSource("main", src, frontend.Mode{})
	return err
}

// An iter-sensitive function whose only callback use is in a test block is
// accepted: test bodies and assertion operands are call sites.
func TestIterCallback_ATestBodyIsACallSite(t *testing.T) {
	const src = `fn skip_odd(n: Int): Int {
  if n % 2 == 1 {
    continue
  }
  n
}

test "evens" {
  assert Iter.map([1, 2, 3, 4], skip_odd) |> Iter.to_list() == [2, 4]
}
`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("the front end rejects a callback use inside a test: %v", err)
	}
}

// A `break` written straight in a test body has no iteration to stop.
func TestIterCallback_AStrayBreakInATestBodyIsReported(t *testing.T) {
	const src = `test "stray" {
  if True {
    break
  }
  assert True
}
`
	err := checkSource(t, src)
	if err == nil || !strings.Contains(err.Error(), "break") {
		t.Fatalf("a stray break in a test body was admitted: %v", err)
	}
}

// Without a seed the first element is the accumulator, so a callback whose
// accumulator type is not the element type is rejected, whether it is a named
// function, an annotated lambda or a piped call. A seed, or matching types,
// is accepted.
func TestIterCallback_SeedlessReduceNeedsTheElementType(t *testing.T) {
	const prelude = `fn count_len(acc: Int, s: String): Int {
  acc + String.length(s)
}

fn add(a: Int, b: Int): Int {
  a + b
}

fn main() {
  `
	const want = "Iter.reduce without an initial value starts from the first element, so the accumulator must have the element type; this callback's accumulator is Int and its element is String"
	for _, bad := range []string{
		`_ = Iter.reduce(["ab"], count_len)`,
		`_ = ["ab"] |> Iter.reduce(count_len)`,
		`_ = Iter.reduce(["ab"], |acc: Int, s: String| acc + String.length(s))`,
	} {
		err := checkSource(t, prelude+bad+"\n}\n")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want the seedless-reduce error, got %v", bad, err)
		}
	}
	for _, good := range []string{
		`_ = Iter.reduce(["ab"], |acc = 0, s| acc + String.length(s))`,
		`_ = Iter.reduce([1, 2], add)`,
		`_ = Iter.reduce([1, 2], |a, b| a + b)`,
	} {
		if err := checkSource(t, prelude+good+"\n}\n"); err != nil {
			t.Errorf("%s: rejected: %v", good, err)
		}
	}
}
