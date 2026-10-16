package vmhost_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// Each program's only call that answers a prelude `Maybe` is a std owner
// call whose builder arm asks whether `Maybe` is anchored. The anchors were
// loaded on first use, so the arm declined unless something earlier in the
// same program (a `Map.size` before the `Map.get`) had loaded them.
var preludeOrderPrograms = []struct{ name, src, want string }{
	{"Map.get", `fn main() {
    config = {"host" => "localhost"}
    dbg Map.get(config, "host")
}
`, "dbg line 3: Map.get(config, \"host\") = Some(\"localhost\")\n"},
	{"Map.get of a list", `fn main() { m = {"a" => [1, 2]} dbg Map.get(m, "a") return }
`, "dbg line 1: Map.get(m, \"a\") = Some([1, 2])\n"},
	{"Vector.get", `fn main() {
    v = #[1, 2, 3]
    dbg Vector.get(v, 1)
}
`, "dbg line 3: Vector.get(v, 1) = Some(2)\n"},
}

// preludeOrderWarmup is loaded and run first in the warm process, so its
// std instances, caches and prelude uses exist before the programs above.
const preludeOrderWarmup = `fn main() {
    m = {"a" => 1}
    dbg Map.size(m)
    dbg Map.get(m, "a")
    dbg List.head([Some(1)])
    dbg Iter.get(#["x"], 0)
}
`

func runPreludeOrderProgram(t *testing.T, src string) string {
	t.Helper()
	p, err := vmhost.Load(writeProgram(t, src))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		if b, blocked := vmhost.IsBlocked(err); blocked {
			t.Fatalf("blocked: %v (%q)", err, b.Reasons)
		}
		t.Fatal(err)
	}
	return out.String()
}

// A program runs the same in a fresh process and in one that has already
// loaded and run other programs.
func TestPreludeAnchorsDoNotDependOnOrder(t *testing.T) {
	if name := os.Getenv("NOMI_PRELUDE_ORDER_CHILD"); name != "" {
		for _, c := range preludeOrderPrograms {
			if c.name == name {
				if got := runPreludeOrderProgram(t, c.src); got != c.want {
					t.Fatalf("got %q, want %q", got, c.want)
				}
				return
			}
		}
		t.Fatalf("no program %q", name)
	}
	for _, c := range preludeOrderPrograms {
		t.Run("fresh process/"+c.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestPreludeAnchorsDoNotDependOnOrder$", "-test.count=1")
			cmd.Env = append(os.Environ(), "NOMI_PRELUDE_ORDER_CHILD="+c.name)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the program fails in a fresh process: %v\n%s", err, out)
			}
		})
	}
	t.Run("after other programs", func(t *testing.T) {
		runPreludeOrderProgram(t, preludeOrderWarmup)
		for _, c := range preludeOrderPrograms {
			if got := runPreludeOrderProgram(t, c.src); got != c.want {
				t.Errorf("%s: got %q, want %q", c.name, got, c.want)
			}
		}
	})
}
