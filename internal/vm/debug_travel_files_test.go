package vm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

const travelFilesSrc = `import {
    std/fs
    std/io
}

fn main() {
    path = "PATH"
    _ = fs.write_text(path, "one", mode: .Append)
    text = fs.read_text(path)
    io.inspect(text)
}
`

// Stepping back over std/fs on the disk answers its crossings from the
// recording: the replay neither writes the file a second time nor reads
// what is there now.
func TestTravel_StepBackNeverWritesAFileTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.txt")
	s := travelProgram(t, strings.ReplaceAll(travelFilesSrc, "PATH", path), "")
	s.breakAt(10)
	s.runPasses()
	st := s.stop()
	s.line(st, vm.StopBreakpoint, 10)
	if recorded := s.d.Stats().Recorded; recorded != 2 {
		t.Fatalf("recorded %d crossings, want 2 (the write and the read): %+v", recorded, s.d.Recording())
	}
	for _, want := range []int{9, 8} {
		if err := s.d.StepBack(st.Thread); err != nil {
			t.Fatal(err)
		}
		st = s.stop()
		s.line(st, vm.StopStep, want)
	}
	s.d.Continue()
	st = s.stop()
	s.line(st, vm.StopBreakpoint, 10)
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "one" {
		t.Fatalf("the file holds %q after a step back; the replay wrote it again", data)
	}
	if out := s.shown.String(); out != "Ok(\"one\")\n" {
		t.Fatalf("output %q", out)
	}
}

const travelSandboxSrc = `import {
    std/fs
    std/fs_testing.{self, Sandbox}
    std/io
}

fn main() {
    ran = fs_testing.run(#Sandbox{files: %{"in.txt" => "a"}}, fn -> {
        _ = fs.write_text("in.txt", "b", mode: .Append)
        fs.read_text("in.txt")
    })
    io.inspect(ran.value)
}
`

// Inside fs_testing.run the replay builds the sandbox again and runs its file
// operations again, so the sandbox the program sees after a step back is the
// one it saw before.
func TestTravel_StepBackInsideASandboxRunsItAgain(t *testing.T) {
	s := travelProgram(t, travelSandboxSrc, "")
	s.breakAt(10, 12)
	s.runPasses()
	st := s.stop()
	s.line(st, vm.StopBreakpoint, 10)
	if err := s.d.StepBack(st.Thread); err != nil {
		t.Fatal(err)
	}
	st = s.stop()
	s.line(st, vm.StopStep, 9)
	s.d.Continue()
	st = s.stop()
	s.line(st, vm.StopBreakpoint, 10)
	s.d.Continue()
	st = s.stop()
	s.line(st, vm.StopBreakpoint, 12)
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if out := s.shown.String(); out != "Ok(\"ab\")\n" {
		t.Fatalf("output %q, want the append once", out)
	}
	for _, c := range s.d.Recording() {
		if strings.HasPrefix(c.Name, "fs_host.") || strings.HasPrefix(c.Name, "io.Reader.") || strings.HasPrefix(c.Name, "io.Writer.") {
			t.Fatalf("recorded %s; a sandbox's operations run again and are not recorded: %+v", c.Name, s.d.Recording())
		}
	}
}
