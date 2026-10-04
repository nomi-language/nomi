package vmhost_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmhost"
)

// An assertion inside a `concurrent` block exits the block as
// Err(AssertionFailure), as `try` does (spec §36): the block's in-flight
// tasks are cancelled, the block's value is the Err, and a test whose final
// value is that Err fails with the assertion's report. The slow task would
// sleep five seconds and print; it is cancelled, so neither happens.
func TestTest_AnAssertionEndsItsConcurrentBlock(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, `import std/assertions.{AssertionFailure}
import std/duration.{Duration}
import std/io
import std/tasks.{Task}
import std/timer

fn slow(): Int {
  timer.sleep(Duration.seconds(5))
  io.print("slow finished")
  7
}

test "the block's value is the failure" {
  r: Result<Int, AssertionFailure> = concurrent {
    s = Task.spawn(|| slow())
    t = Task.spawn(|| 1)
    x = Task.await(t)
    assert x == 2
    Ok(x + Task.await(s))
  }
  refute r
}

test "a block that is the body's value fails the case" {
  concurrent {
    t = Task.spawn(|| 1)
    x = Task.await(t)
    assert x == 2
    Ok(x)
  }
}

test "a passing assertion continues the block" {
  v = concurrent {
    t = Task.spawn(|| 2)
    x = Task.await(t)
    assert x == 2
    Ok(x)
  }
  assert v == Ok(2)
}
`))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	start := time.Now()
	cases := p.Cases(&out, vmhost.TestOptions{})
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("the cases took %s: the slow task was not cancelled", elapsed)
	}
	if len(cases) != 3 {
		t.Fatalf("%d cases, want 3: %+v", len(cases), cases)
	}
	for i, c := range cases {
		if c.Blocked != nil {
			t.Fatalf("case %q is blocked: %v", c.Name, c.Blocked)
		}
		if fails := i == 1; (c.Err != nil) != fails {
			t.Fatalf("case %q: err %v, want failing=%v", c.Name, c.Err, fails)
		}
	}
	if !strings.Contains(cases[1].Err.Error(), "line 28: assertion failed") {
		t.Fatalf("the failing case reports %q", cases[1].Err)
	}
	if strings.Contains(out.String(), "slow finished") {
		t.Fatalf("the cancelled task ran to completion: %q", out.String())
	}
}
