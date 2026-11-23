package vmhost

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// reachedText is each call of calls in the file named name, as `line:text`
// with the text src holds at its span and `*` before a marked one, in
// position order.
func reachedText(calls []ReachedCall, name, src string) []string {
	lines := strings.Split(src, "\n")
	var out []string
	for _, c := range calls {
		if c.Path != name || c.Uses || c.Line != c.EndLine {
			continue
		}
		mark := ""
		if c.Marked {
			mark = "*"
		}
		out = append(out, fmt.Sprintf("%s%d:%s %s", mark, c.Line, lines[c.Line-1][c.Col-1:c.EndCol-1], c.Text))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// effectErrorsBlock is a block whose describe misses a variant and prints,
// beside a main that checks and a helper that holds a `dbg`.
const effectErrorsBlock = `import std::io

enum Payment
    Card String
    Cash
end

fn describe(payment: Payment) -> String
    io.print("describing")
    case payment
        .Card(last4) -> "card ${last4}"
    end
end

fn noted(n: Int) -> Int
    dbg n
end

fn main()
    dbg describe(.Card("4242"))
    io.print("hi")
    _ = noted(1)
end
`

// The tour's editors show a block that does not compile with the marks
// and lines of the functions that check, as the language server does
// (CheckLowering): describe, which does not, has none, its io.print
// included, and the call of it says nothing, while main's `dbg`, its
// io.print and its call of noted have their lines. Loading the block still
// fails with the error.
func TestReachedMarked_ABlockWithAFunctionThatDoesNotCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	const missing = "non-exhaustive case on Payment: missing Cash"
	if _, _, err := LoadMarked(effectErrorsBlock, t.TempDir()); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("loading the block answers %v; want it refused with %q", err, missing)
	}
	calls, err := ReachedMarked(effectErrorsBlock, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("ReachedMarked answers %v; want the block's error %q", err, missing)
	}
	want := []string{
		"*15:noted Has effects: `dbg`.",
		"*19:main Has effects: `dbg`, `std::io`.",
		"*21:io.print Has effects: `std::io`.",
		"*22:noted Has effects: `dbg`.",
		"16:dbg Has effects: `dbg`.",
		"20:dbg Has effects: `dbg`.",
	}
	if got := reachedText(calls, "main.nomi", effectErrorsBlock); !slices.Equal(got, want) {
		t.Errorf("the block's calls are\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A function that does not check in a file the entry imports leaves the
// entry's marks, and the imported file's other functions, as they are; a
// call of it says nothing.
func TestReachedMarked_AFunctionThatDoesNotCheckInAnImportedFile(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	const store = `import std::io

pub fn save(n: Int)
    io.print("${n}")
end

pub fn broken(n: Int) -> Int
    io.print("lost")
    label: Int = "n"
    n + label
end
`
	const main = `import store

fn main()
    store.save(1)
    _ = store.broken(2)
end
`
	src := "// FILE: main.nomi\n" + main + "\n// FILE: store.nomi\n" + store
	calls, err := ReachedMarked(src, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "type mismatch: expected Int, got String") {
		t.Fatalf("ReachedMarked answers %v; want the block's type error", err)
	}
	if got, want := reachedText(calls, "main.nomi", main), []string{
		"*3:main Has effects: `std::io` through `save`.",
		"*4:store.save Has effects: `std::io`.",
	}; !slices.Equal(got, want) {
		t.Errorf("main.nomi's calls are %q, want %q", got, want)
	}
	if got, want := reachedText(calls, "store.nomi", store), []string{
		"*3:save Has effects: `std::io`.",
		"*4:io.print Has effects: `std::io`.",
	}; !slices.Equal(got, want) {
		t.Errorf("store.nomi's calls are %q, want %q", got, want)
	}
}

// An error outside every function and test, here a field's type, can
// change what any body means, so the program is not lowered and nothing is
// marked, as before any program is.
func TestCheckLowering_AnErrorOutsideAFunctionMarksNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	const src = `import std::io

struct Order
    total: Amount
end

fn main()
    io.print("hi")
end
`
	dir := writeProject(t, map[string]string{"main.nomi": src, "nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"})
	found, err := CheckLowering(context.Background(), filepath.Join(dir, "main.nomi"), src)
	if err == nil || !strings.Contains(err.Error(), "Amount") {
		t.Fatalf("CheckLowering answers %v; want the unknown type refused", err)
	}
	if found.Problems != nil || len(found.Reached) != 0 {
		t.Errorf("CheckLowering found %v and %+v; want nothing", found.Problems, found.Reached)
	}
}
