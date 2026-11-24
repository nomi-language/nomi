//go:build darwin || linux

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestTerminal_OnAPseudoTerminal runs `nomi run` on a pseudo-terminal and
// reads the terminal's mode back from this side while the program runs:
// std::terminal's raw mode is set, and the mode it found is back after the
// body returns, traps or is cancelled, after a nested raw mode, and after
// the process is interrupted inside raw mode, by a signal from outside or
// by Ctrl-C the terminal sends; a password read turns echo off, echoes
// nothing and turns echo back on; the size is the one set on the terminal;
// keys decode from the bytes the terminal sends.
func TestTerminal_OnAPseudoTerminal(t *testing.T) {
	t.Run("raw mode nests and each return restores what it found", func(t *testing.T) {
		s := startOnPTY(t, `
import
    std::io
    std::terminal.{self, RawMode}
end

fn main() -> Result<Unit, terminal.Error>
    outer()?
    io.print("restored")
    _ = io.read_line()
    Ok(Unit)
end

fn outer() -> Result<Unit, terminal.Error>
    raw = terminal.raw_mode(keep_output_processing: True)?
    defer RawMode.restore(raw)
    inner()?
    io.write("outer\r\n")
    _ = terminal.read_key()?
    Ok(Unit)
end

fn inner() -> Result<Unit, terminal.Error>
    raw = terminal.raw_mode()?
    defer RawMode.restore(raw)
    io.write("inner\r\n")
    _ = terminal.read_key()?
    Ok(Unit)
end
`)
		s.waitFor("inner")
		inner := s.mode()
		if !isRaw(inner) || inner.Oflag&unix.OPOST != 0 || inner.Lflag&unix.ISIG != 0 {
			t.Fatalf("inside the inner raw mode the terminal is %s; want raw with no output processing and no signal keys", describe(inner))
		}
		s.write("x")
		s.waitFor("outer")
		outer := s.mode()
		if !isRaw(outer) || outer.Oflag&unix.OPOST == 0 {
			t.Fatalf("after the inner restore the terminal is %s; want the outer raw mode, which keeps output processing", describe(outer))
		}
		s.write("x")
		s.waitFor("restored")
		s.wantOriginal("after the outer restore")
		s.write("\n")
		s.wantExit(0)
	})

	t.Run("a trap in the body restores", func(t *testing.T) {
		s := startOnPTY(t, `
import
    std::io
    std::tasks.{Outcome, Task}
    std::terminal.{self, RawMode}
end

fn enter() -> Result<Unit, terminal.Error>
    raw = terminal.raw_mode()?
    defer RawMode.restore(raw)
    io.write("raw\r\n")
    _ = terminal.read_key()?
    _ = String.to_int("not a number")!
    Ok(Unit)
end

fn main()
    outcome =
        concurrent
            task = Task.spawn(fn -> enter())
            Task.outcome(task)
        end

    if outcome is Outcome.Failed(_) then io.print("trapped")
    _ = io.read_line()
end
`)
		s.waitFor("raw")
		if m := s.mode(); !isRaw(m) {
			t.Fatalf("inside raw mode the terminal is %s", describe(m))
		}
		s.write("x")
		s.waitFor("trapped")
		s.wantOriginal("after the body trapped")
		s.write("\n")
		s.wantExit(0)
	})

	t.Run("a stopped task restores", func(t *testing.T) {
		s := startOnPTY(t, `
import
    std::channels.{Channel, Receiver, Sender}
    std::io
    std::tasks.{Outcome, Task}
    std::terminal.{self, RawMode}
end

fn main()
    outcome =
        concurrent
            read: Channel<Unit> = Channel.unbuffered()
            never: Channel<Unit> = Channel.unbuffered()

            task = Task.spawn(fn ->
                raw = terminal.raw_mode()!
                defer RawMode.restore(raw)
                io.write("raw\r\n")
                _ = terminal.read_key()
                _ = Sender.send(read.sender, Unit)
                _ = Receiver.receive(never.receiver)
            end)

            _ = Receiver.receive(read.receiver)
            Task.cancel(task)
            Task.outcome(task)
        end

    if outcome is Outcome.Cancelled then io.print("cancelled")
    _ = io.read_line()
end
`)
		s.waitFor("raw")
		if m := s.mode(); !isRaw(m) {
			t.Fatalf("inside raw mode the terminal is %s", describe(m))
		}
		s.write("x")
		s.waitFor("cancelled")
		s.wantOriginal("after the task was cancelled")
		s.write("\n")
		s.wantExit(0)
	})

	interrupted := `
import
    std::io
    std::terminal.{self, RawMode}
end

fn main() -> Result<Unit, terminal.Error>
    raw = terminal.raw_mode(keep_signals: KEEP)?
    defer RawMode.restore(raw)
    io.write("raw\r\n")
    _ = terminal.read_key()?
    io.write("not interrupted\r\n")
    Ok(Unit)
end
`
	t.Run("an interrupt from outside restores", func(t *testing.T) {
		s := startOnPTY(t, strings.Replace(interrupted, "KEEP", "False", 1))
		s.waitFor("raw")
		if m := s.mode(); !isRaw(m) {
			t.Fatalf("inside raw mode the terminal is %s", describe(m))
		}
		// The program's process group: the shell holding the session
		// ignores SIGINT.
		if err := syscall.Kill(-s.cmd.Process.Pid, syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		s.wantExit(130)
		s.wantOriginal("after SIGINT ended the program")
	})

	t.Run("Ctrl-C with the signal keys kept restores", func(t *testing.T) {
		s := startOnPTY(t, strings.Replace(interrupted, "KEEP", "True", 1))
		s.waitFor("raw")
		if m := s.mode(); !isRaw(m) || m.Lflag&unix.ISIG == 0 {
			t.Fatalf("inside raw mode with the signal keys kept the terminal is %s", describe(m))
		}
		s.write("\x03")
		s.wantExit(130)
		s.wantOriginal("after Ctrl-C ended the program")
	})

	t.Run("a password is not echoed and echo comes back", func(t *testing.T) {
		s := startOnPTY(t, `
import
    std::io
    std::secret.Secret
    std::terminal
end

fn main() -> Result<Unit, terminal.Error>
    password = terminal.read_password(prompt: "password: ")?
    io.print("read ${String.length(Secret.expose(password))} characters")
    _ = io.read_line()
    Ok(Unit)
end
`)
		s.waitFor("password: ")
		if m := s.mode(); m.Lflag&unix.ECHO != 0 || m.Lflag&unix.ICANON == 0 {
			t.Fatalf("while the password is read the terminal is %s; want echo off and lines on", describe(m))
		}
		s.write("hunter2\n")
		s.waitFor("read 7 characters")
		if strings.Contains(s.output(), "hunter2") {
			t.Fatalf("the password was echoed: %q", s.output())
		}
		s.wantOriginal("after the password was read")
		s.write("\n")
		s.wantExit(0)
	})

	t.Run("the size and the streams are the terminal's", func(t *testing.T) {
		s := startOnPTY(t, `
import
    std::io
    std::terminal
end

fn main()
    io.print(Debug.inspect(terminal.size()))
    io.print("${io.is_terminal(.Stdin)} ${io.is_terminal(.Stdout)} ${io.is_terminal(.Stderr)}")
    io.print("${io.capture("", fn -> io.is_terminal(.Stdout)).value}")
end
`)
		s.waitFor("Some(Size{columns: 100, rows: 40})")
		s.waitFor("True True True")
		s.waitFor("False")
		s.wantExit(0)
	})

	t.Run("keys decode from what the terminal sends", func(t *testing.T) {
		s := startOnPTY(t, `
import
    std::duration.Duration
    std::io
    std::terminal.{self, RawMode}
end

fn main() -> Result<Unit, terminal.Error>
    raw = terminal.raw_mode()?
    defer RawMode.restore(raw)
    io.write("none yet: ${Debug.inspect(terminal.read_key_within(Duration.milliseconds(10))?)}\r\n")
    Iter.each(1..=5, fn n ->
        key = terminal.read_key()!
        io.write("key ${n}: ${Debug.inspect(key)}\r\n")
    end)
    Ok(Unit)
end
`)
		s.waitFor("none yet: None")
		s.write("\x1b[Aa")
		s.waitFor("key 1: Up")
		s.waitFor("key 2: Char(Codepoint(97))")
		s.write("\x1b")
		s.waitFor("key 3: Escape")
		s.write("\x1b[15~")
		s.waitFor("key 4: F(5)")
		s.write("\xc3\xa9")
		s.waitFor("key 5: Char(Codepoint(233))")
		s.wantExit(0)
		s.wantOriginal("after the program returned")
	})
}

// ptySession is a `nomi run` whose standard streams are a pseudo-terminal's
// terminal side, which this side holds open too, to read its mode. A shell
// that ignores SIGINT leads the terminal's session and runs the program in
// it, then says how it exited and waits for a line: the terminal stays
// usable after the program exits, where the system would take it from
// every holder once the session's leader exited, and Ctrl-C reaches the
// program as it does in a shell.
type ptySession struct {
	t        *testing.T
	cmd      *exec.Cmd
	master   *os.File
	terminal *os.File
	original unix.Termios
	mu       sync.Mutex
	out      bytes.Buffer
}

func startOnPTY(t *testing.T, src string) *ptySession {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	if err := os.WriteFile(path, []byte(strings.TrimLeft(src, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	master, name, err := openPTY()
	if err != nil {
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	terminal, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	if err := unix.IoctlSetWinsize(int(terminal.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 100}); err != nil {
		t.Fatal(err)
	}
	original, err := unix.IoctlGetTermios(int(terminal.Fd()), ptyGetTermios)
	if err != nil {
		t.Fatal(err)
	}
	s := &ptySession{t: t, master: master, terminal: terminal, original: *original}
	s.cmd = exec.Command("/bin/sh", "-c", `trap "" INT; "$0" run "$1"; echo "[exited $?]"; read line`, nomiBin, path)
	s.cmd.Stdin, s.cmd.Stdout, s.cmd.Stderr = terminal, terminal, terminal
	s.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			s.mu.Lock()
			s.out.Write(buf[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
		master.Close()
		terminal.Close()
	})
	return s
}

func (s *ptySession) output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.String()
}

// waitFor waits until the program has written text, and takes what it
// wrote up to it out of the output, so a later wait looks past it.
func (s *ptySession) waitFor(text string) {
	s.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		out := s.out.String()
		if i := strings.Index(out, text); i >= 0 {
			s.out.Reset()
			s.out.WriteString(out[i+len(text):])
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	s.t.Fatalf("the program did not write %q in 60s; it wrote %q", text, s.output())
}

func (s *ptySession) write(text string) {
	s.t.Helper()
	if _, err := s.master.Write([]byte(text)); err != nil {
		s.t.Fatal(err)
	}
}

func (s *ptySession) mode() unix.Termios {
	s.t.Helper()
	m, err := unix.IoctlGetTermios(int(s.terminal.Fd()), ptyGetTermios)
	if err != nil {
		s.t.Fatal(err)
	}
	return *m
}

// wantOriginal fails unless the terminal's mode is the one it had before
// the program started.
func (s *ptySession) wantOriginal(when string) {
	s.t.Helper()
	m, o := s.mode(), s.original
	if m.Iflag != o.Iflag || m.Oflag != o.Oflag || m.Lflag != o.Lflag || m.Cflag != o.Cflag ||
		m.Cc[unix.VMIN] != o.Cc[unix.VMIN] || m.Cc[unix.VTIME] != o.Cc[unix.VTIME] {
		s.t.Fatalf("%s the terminal is %s; want the mode it started in, %s", when, describe(m), describe(o))
	}
}

// wantExit waits for the program to exit with code. The shell then waits
// for a line, holding the terminal, until the test ends.
func (s *ptySession) wantExit(code int) {
	s.t.Helper()
	s.waitFor(fmt.Sprintf("[exited %d]", code))
}

func isRaw(m unix.Termios) bool {
	return m.Lflag&(unix.ECHO|unix.ICANON) == 0 && m.Iflag&unix.ICRNL == 0
}

func describe(m unix.Termios) string {
	var on []string
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"ECHO", m.Lflag&unix.ECHO != 0},
		{"ICANON", m.Lflag&unix.ICANON != 0},
		{"ISIG", m.Lflag&unix.ISIG != 0},
		{"ICRNL", m.Iflag&unix.ICRNL != 0},
		{"OPOST", m.Oflag&unix.OPOST != 0},
	} {
		if f.set {
			on = append(on, f.name)
		}
	}
	return "{" + strings.Join(on, " ") + "}"
}
