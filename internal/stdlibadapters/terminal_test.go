package stdlibadapters

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/rt"
)

// TestRtFuncs_TerminalRowsOffATerminal holds std/io's `stream_is_terminal`
// and std/terminal/host's rows to what they answer through the generated
// adapters where no stream is a terminal: standard input over a file and
// over memory, output into a buffer, and a frame with no streams. No stream
// is a terminal and none has a size or colour; a password read and raw
// mode fail with ENOTTY; leaving raw mode that was never entered does
// nothing; a byte read answers the input's bytes, then -2 at its end. On a
// terminal, cmd/nomi's TestTerminal_OnAPseudoTerminal runs each row.
func TestRtFuncs_TerminalRowsOffATerminal(t *testing.T) {
	table, err := Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	call := func(fr *rt.Frame, key string, args ...any) string {
		t.Helper()
		v, err := table[key](fr, args)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return canonText(v, false)
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "input"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var out bytes.Buffer
	frames := map[string]*rt.Frame{
		"a file":    rt.NewFrame(rt.WithStreams(rt.WithInput(context.Background(), rt.NewInput(file)), file, file)),
		"memory":    rt.NewFrame(rt.WithStreams(rt.WithInput(context.Background(), rt.NewInput(strings.NewReader("ab"))), &out, &out)),
		"no stream": rt.NewFrame(context.Background()),
	}
	for name, fr := range frames {
		for _, stream := range []int64{0, 1, 2} {
			if got := call(fr, "io.stream_is_terminal", stream); got != "Bool(false)" {
				t.Errorf("%s: stream %d is a terminal: %s", name, stream, got)
			}
			if got := call(fr, "terminal/host.size", stream); got != "/3{0=Int(0), 1=Int(0)}" {
				t.Errorf("%s: stream %d has a size: %s", name, stream, got)
			}
			if got := call(fr, "terminal/host.supports_color", stream); got != "Bool(false)" {
				t.Errorf("%s: stream %d supports colour: %s", name, stream, got)
			}
		}
		if got := call(fr, "terminal/host.read_password", "password: "); !strings.Contains(got, `code=String("ENOTTY")`) {
			t.Errorf("%s: a password read answered %s, want ENOTTY", name, got)
		}
		if got := call(fr, "terminal/host.enter_raw", false, false); !strings.Contains(got, `code=String("ENOTTY")`) {
			t.Errorf("%s: raw mode answered %s, want ENOTTY", name, got)
		}
		if got := call(fr, "terminal/host.leave_raw", int64(1)); got != "Unit" {
			t.Errorf("%s: leaving raw mode never entered answered %s", name, got)
		}
	}
	if out.Len() != 0 {
		t.Errorf("a password read off a terminal wrote its prompt: %q", out.String())
	}
	memory := frames["memory"]
	for _, want := range []string{"97", "98", "-2"} {
		if got := call(memory, "terminal/host.read_byte", int64(-1)); got != "results.Result/4.Ok{0=Int("+want+")}" {
			t.Errorf("a byte read answered %s, want %s", got, want)
		}
	}
	if got := call(frames["no stream"], "terminal/host.read_byte", int64(1000)); got != "results.Result/4.Ok{0=Int(-2)}" {
		t.Errorf("a byte read with no input answered %s, want the end", got)
	}
}
