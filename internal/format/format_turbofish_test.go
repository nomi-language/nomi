package format

import (
	"strings"
	"testing"
)

// The formatter must preserve turbofish type arguments on calls — without
// this it silently drops `<Int>` on every `fmt -w`.
func TestFormat_TurbofishPreserved(t *testing.T) {
	got, err := Format("fn main(): Unit {\n  ch = channel.new<Int>(capacity: 4)\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "channel.new<Int>(") {
		t.Fatalf("turbofish dropped by formatter; got:\n%s", got)
	}
}

func TestFormat_TurbofishMultipleArgs(t *testing.T) {
	got, err := Format("fn main(): Unit {\n  x = make<String, Int>(y)\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "make<String, Int>(") {
		t.Fatalf("multi-arg turbofish not preserved; got:\n%s", got)
	}
}
