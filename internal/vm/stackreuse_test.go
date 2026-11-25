package vm_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A task runs on a register stack the last task parked. The first task's
// body returns a tuple the VM keeps split, so the stack's last return is in
// parts, and parking a stack drops the parts' pack. The second task's body
// traps in a function with a deferred call, and running that defer boxes the
// stack's last return first: a stack parked with its return class still in
// parts and no pack failed the task with a Go nil dereference, reported as
// `Panicked`, in place of the trap's own fault.
func TestStackReuse_ATrapAfterASplitReturnKeepsItsFault(t *testing.T) {
	p, err := vmhost.LoadSource("main", `import std::io
import std::tasks.Task

fn pair(n: Int) -> (Int, Int)
    (n, n + 1)
end

fn noted()
end

fn guarded() -> Int
    defer noted()
    Iter.find([1], &(&1 > 5))!
end

fn main()
    ended =
        concurrent
            first = Task.spawn(fn -> pair(1))
            _ = Task.await(first)
            second = Task.spawn(fn -> guarded())
            Task.outcome(second)
        end

    io.inspect(ended)
end
`)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Failed(Errored(") || !strings.Contains(got, "found nothing") {
		t.Fatalf("the trapping task ended %q; want Failed(Errored(...)) with the trap's fault", got)
	}
}
