package vmhost_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// loadModule writes files into a fresh module directory and loads its
// main.nomi as `nomi run` does.
func loadModule(t *testing.T, files map[string]string) (*vmhost.Program, error) {
	t.Helper()
	dir := t.TempDir()
	files["nomi.toml"] = "[package]\nname = \"app\"\nentry_points = [\"main\"]\n"
	writeFiles(t, dir, files)
	return vmhost.Load(filepath.Join(dir, "main.nomi"))
}

// A program's own type imported as `Iter` has a `loop` that returns: the
// statement after a call of it runs. Read by the name, the call was taken
// for std's `Iter.loop`, and the next line was refused as unreachable.
func TestLoopNeverReturns_AnotherTypeNamedIterReturns(t *testing.T) {
	p, err := loadModule(t, map[string]string{
		"counter.nomi": `pub struct Counter
    n: Int
end

impl Counter
    pub fn loop(from: Int, step: (Int) -> Int) -> Int
        step(from)
    end
end
`,
		"main.nomi": `import
    std::io
    counter.{Counter as Iter}
end

fn main()
    n = Iter.loop(1, fn i -> i + 1)
    io.print(n)
end
`,
	})
	if err != nil {
		t.Fatalf("the front end refuses a call of a program's own loop: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if got, want := out.String(), "2\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// std's `Iter` imported under another name is std's: a `Seq.loop` whose
// callback has no `break` never returns. A statement after it is
// unreachable code, and as a body's last statement it lowers as the loop
// that never ends. Read by the name, neither was seen as such a loop: the
// first was admitted and the second declined, each as "not supported yet".
func TestLoopNeverReturns_IterUnderAnotherNameNeverReturns(t *testing.T) {
	_, err := loadModule(t, map[string]string{
		"main.nomi": `import
    std::io
    std::iter.{Iter as Seq}
end

fn main()
    n = Seq.loop(1, fn i -> i + 1)
    io.print(n)
end
`,
	})
	if err == nil {
		t.Fatal("the front end admits a statement after a loop that never breaks")
	}
	if want := "main.nomi:8:5: unreachable code: the statement above always loops forever"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q lacks %q", err, want)
	}

	if _, err := loadModule(t, map[string]string{
		"main.nomi": `import
    std::io
    std::iter.{Iter as Seq}
end

fn main()
    io.print("start")
    Seq.loop(fn ->
        io.print("tick")
    end)
end
`,
	}); err != nil {
		t.Fatalf("a body ending in a loop that never breaks does not load: %v", err)
	}
}
