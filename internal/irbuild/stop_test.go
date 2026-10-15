package irbuild

import (
	"errors"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// GenerateIR asks Program.Stop between its phases and files, answers
// ErrStopped at whichever question Stop first reports true, asks nothing
// after it, and lowers as usual when Stop never does.
func TestGenerateIR_StopsWhenAsked(t *testing.T) {
	const src = "import other\n\nfn double(n: Int): Int {\n    n * 2\n}\n\nfn main() {\n    _ = double(other.three())\n}\n"
	program := func() *Program {
		t.Helper()
		p, err := AnalyzeVirtual("main", src, frontend.Config{VirtualFiles: map[string]string{"other": "pub fn three(): Int {\n    3\n}\n"}})
		if err != nil {
			t.Fatalf("the front end rejects this: %v", err)
		}
		if len(p.Modules) != 2 {
			t.Fatalf("the program has %d files, want 2", len(p.Modules))
		}
		return p
	}
	p := program()
	questions := 0
	p.Stop = func() bool {
		questions++
		return false
	}
	if res, _, err := GenerateIR(p); err != nil || res == nil || !res.HasMain {
		t.Fatalf("never stopped: GenerateIR answered %v, %v", res, err)
	}
	// Twice per file in the per-file phases, at least.
	if questions < 8 {
		t.Fatalf("Stop was asked %d times for two files", questions)
	}
	for stopAt := 1; stopAt <= questions; stopAt++ {
		p := program()
		asked := 0
		p.Stop = func() bool {
			asked++
			return asked >= stopAt
		}
		res, _, err := GenerateIR(p)
		if !errors.Is(err, ErrStopped) || res != nil {
			t.Fatalf("stopped at question %d: GenerateIR answered %v, %v; want ErrStopped", stopAt, res, err)
		}
		if asked != stopAt {
			t.Fatalf("stopped at question %d, and Stop was asked %d times", stopAt, asked)
		}
	}
}
