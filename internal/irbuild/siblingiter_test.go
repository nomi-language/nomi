package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// `Iter.*` owner calls run over a type whose `impl Iter` another file
// declares: a monomorphic enum with a written `known_count`, which
// `Iter.count` answers as std's dispatch would, and a generic one, viewed through the sibling's `each_while` as the declaring file's own
// calls are.
func TestSiblingIter_OwnerCallsOverAnotherFilesImplRun(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	files := map[string]string{
		"main.nomi": `import std/io
import chains.Chain
import stacks.Stack

fn main() {
    chain = #Chain.Link{value: 1, next: #Chain.Link{value: 2, next: Chain.End}}
    io.inspect(Iter.count(chain))
    io.inspect(Iter.known_count(chain))
    io.inspect(Iter.to_list(chain))
    io.inspect(chain |> Iter.map(fn x -> x * 10) |> Iter.to_list())
    io.inspect(Iter.reduce(chain, fn acc, x -> acc + x, from: 0))
    stack: Stack<String> = #Stack.Push{top: "a", rest: #Stack.Push{top: "b", rest: Stack.Bottom}}
    io.inspect(Iter.count(stack))
    io.inspect(stack |> Iter.map(String.uppercase) |> Iter.to_list())
}
`,
		"chains.nomi": `pub enum Chain {
    End
    Link {value: Int, next: Chain}
}

impl Iter<Int> for Chain {
    fn each_while(chain: Chain, yield: (Int) -> Bool) -> Bool {
        case chain {
            .End -> True
            #.Link{value, next} -> yield(value) and each_while(next, yield)
        }
    }

    fn known_count(_chain: Chain) -> Maybe<Int> {
        Some(7)
    }
}
`,
		"stacks.nomi": `pub enum Stack<T> {
    Bottom
    Push {top: T, rest: Stack<T>}
}

impl Iter<T> for Stack<T> {
    fn each_while(stack: Stack<T>, yield: (T) -> Bool) -> Bool {
        case stack {
            .Bottom -> True
            #.Push{top, rest} -> yield(top) and each_while(rest, yield)
        }
    }
}
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.nomi")
	if _, err := Analyze(entry); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	got := vmReference(entry)
	want := "7\nSome(7)\n[1, 2]\n[10, 20]\n3\n2\n[\"A\", \"B\"]\n"
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
}
