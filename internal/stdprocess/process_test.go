package stdprocess_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmhost"
)

// helperArg makes the test binary a helper program the tests start: it
// does what its next argument says and exits, rather than running tests.
const helperArg = "nomi-process-helper"

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == helperArg {
		os.Exit(helper(os.Args[2], os.Args[3:]))
	}
	os.Exit(m.Run())
}

// helper is the helper program: `echo` prints each argument on a line,
// `upper` copies its input to its output in capitals, `fail <code>`
// writes `bad` to its error and exits with code, `env <name>` prints a
// variable, `pwd` prints its directory's base name, and `sleep` waits an
// hour.
func helper(cmd string, args []string) int {
	switch cmd {
	case "echo":
		for _, a := range args {
			fmt.Println(a)
		}
	case "upper":
		in := bufio.NewReader(os.Stdin)
		data, _ := io.ReadAll(in)
		os.Stdout.WriteString(strings.ToUpper(string(data)))
	case "fail":
		code, _ := strconv.Atoi(args[0])
		fmt.Fprintln(os.Stderr, "bad")
		return code
	case "env":
		fmt.Println(os.Getenv(args[0]))
	case "pwd":
		wd, _ := os.Getwd()
		fmt.Println(filepath.Base(wd))
	case "sleep":
		time.Sleep(time.Hour)
	}
	return 0
}

// run loads src and runs its main, answering what it printed.
func run(t *testing.T, src string) string {
	t.Helper()
	var errs bytes.Buffer
	p, err := vmhost.LoadSource("main.nomi", src, vmhost.WithErrorOutput(&errs))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := p.Run(ctx, &out, nil, false); err != nil {
		t.Fatalf("run: %v\nstderr: %s\noutput: %s", err, errs.String(), out.String())
	}
	return out.String()
}

func helperPath(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(exe)
}

// conformanceBody exercises every call std/process makes. The operating
// system runs it against the helper, and process_testing's fake against
// answers that say what the helper would; both print the same.
const conformanceBody = `
fn body(helper: String)
    echoed = process.output(Command{program: helper, args: ["nomi-process-helper", "echo", "a b", "*"]})?
    io.print("echo: ${Bytes.to_string(echoed.stdout)!} ${echoed.status}")

    failed = process.output(Command{program: helper, args: ["nomi-process-helper", "fail", "3"]})?
    io.print("fail: ${Bytes.to_string(failed.stderr)!} ${failed.status} ${Status.is_success(failed.status)}")

    ran = process.run(Command{program: helper, args: ["nomi-process-helper", "echo", "inherited"]})?
    io.print("run: ${ran}")

    upper = process.output(Command{program: helper, args: ["nomi-process-helper", "upper", "1"], stdin: Stdin.Text("hi")})?
    io.print("upper: ${Bytes.to_string(upper.stdout)!}")

    env = process.output(Command{program: helper, args: ["nomi-process-helper", "env", "NOMI_X"], env: {"NOMI_X" => "set"}})?
    io.print("env: ${Bytes.to_string(env.stdout)!}")

    child = process.spawn(Command{program: helper, args: ["nomi-process-helper", "upper", "2"]})?
    defer Child.kill(child)

    _ = Writer.write_text(Child.stdin(child), "abc")
    status = Child.wait(child)?
    text = Reader.read_all_text(Child.stdout(child))?
    io.print("spawned: ${text} ${status}")

    case process.output(Command{program: "nomi-no-such-program"})
        Ok(_) -> io.print("missing: started")
        Err(e) -> io.print("missing: ${e}")
    end

    Ok(Unit)
end
`

const conformanceImports = `import
    std::io.{self, Error, Reader, Writer}
    std::process.{self, Child, Command, Status, Stdin}
`

const conformanceWant = `echo: a b
*
 exited with code 0
fail: bad
 exited with code 3 False
inherited
run: exited with code 0
upper: HI
env: set

spawned: ABC exited with code 0
missing: nomi-no-such-program: not found
`

// TestOSAndFakeAgree runs the same calls against real processes and
// against the fake, and both print what the helper does.
func TestOSAndFakeAgree(t *testing.T) {
	helper := helperPath(t)
	osSrc := conformanceImports + "end\n" + fmt.Sprintf(`
fn main() -> Result<Unit, Error>
    body(%q)
end
`, helper) + strings.Replace(conformanceBody, "fn body(helper: String)", "fn body(helper: String) -> Result<Unit, Error>", 1)

	fakeSrc := conformanceImports + "    std::process_testing.{self, Answer}\nend\n" + fmt.Sprintf(`
fn main() -> Result<Unit, Error>
    helper = %q
    a = fn args: List<String>, stdout: String -> Answer{program: helper, args: Some(["nomi-process-helper", ..args]), stdout:}

    answers = [
        a(["echo", "a b", "*"], "a b\n*\n"),
        Answer{program: helper, args: Some(["nomi-process-helper", "fail", "3"]), code: 3, stderr: "bad\n"},
        a(["echo", "inherited"], "inherited\n"),
        a(["upper", "1"], "HI"),
        a(["env", "NOMI_X"], "set\n"),
        a(["upper", "2"], "ABC"),
    ]

    ran = process_testing.run(answers, fn -> body(helper))
    ran.value
end
`, helper) + strings.Replace(conformanceBody, "fn body(helper: String)", "fn body(helper: String) -> Result<Unit, Error>", 1)

	for name, src := range map[string]string{"os": osSrc, "fake": fakeSrc} {
		t.Run(name, func(t *testing.T) {
			got := run(t, src)
			if strings.ReplaceAll(got, "\r\n", "\n") != conformanceWant {
				t.Errorf("printed:\n%s\nwant:\n%s", got, conformanceWant)
			}
		})
	}
}

// TestDeferredKillKillsWhatSpawnStarted: a function that returns while its
// program still runs does not wait for it; the deferred kill kills it.
func TestDeferredKillKillsWhatSpawnStarted(t *testing.T) {
	src := fmt.Sprintf(`import
    std::io.Error
    std::process.{self, Child, Command}
end

fn main() -> Result<Unit, Error>
    child = process.spawn(Command{program: %q, args: ["nomi-process-helper", "sleep"]})?
    defer Child.kill(child)
    Ok(Unit)
end
`, helperPath(t))
	start := time.Now()
	run(t, src)
	if d := time.Since(start); d > 30*time.Second {
		t.Fatalf("the run took %v: it waited for the program instead of killing it", d)
	}
}

// TestDirRunsTheProgramThere: a command's dir is where the program runs.
func TestDirRunsTheProgramThere(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "here")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`import
    std::io.{self, Error}
    std::process.{self, Command}
end

fn main() -> Result<Unit, Error>
    out = process.output(Command{program: %q, args: ["nomi-process-helper", "pwd"], dir: Some(%q)})?
    io.write(Bytes.to_string(out.stdout)!)
    Ok(Unit)
end
`, helperPath(t), filepath.ToSlash(dir))
	if got := strings.TrimSpace(run(t, src)); got != "here" {
		t.Fatalf("the program ran in %q, want here", got)
	}
}

// TestClearEnvLeavesOnlyTheCommandsVariables: clear_env drops the
// inherited environment.
func TestClearEnvLeavesOnlyTheCommandsVariables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows program needs SYSTEMROOT from its environment to start, so it cannot run with an empty one")
	}
	t.Setenv("NOMI_INHERITED", "yes")
	src := fmt.Sprintf(`import
    std::io.{self, Error}
    std::process.{self, Command}
end

fn main() -> Result<Unit, Error>
    kept = process.output(Command{program: %[1]q, args: ["nomi-process-helper", "env", "NOMI_INHERITED"]})?
    cleared = process.output(Command{program: %[1]q, args: ["nomi-process-helper", "env", "NOMI_INHERITED"], clear_env: True})?
    io.write("${Bytes.to_string(kept.stdout)!}|${Bytes.to_string(cleared.stdout)!}")
    Ok(Unit)
end
`, helperPath(t))
	if got := strings.ReplaceAll(run(t, src), "\r\n", "\n"); got != "yes\n|\n" {
		t.Fatalf("printed %q, want %q", got, "yes\n|\n")
	}
}

// TestDeferredKillKillsWhenTheFunctionTraps: a function that traps while
// its program runs does not leave the program running; the trap is the
// run's failure.
func TestDeferredKillKillsWhenTheFunctionTraps(t *testing.T) {
	src := fmt.Sprintf(`import
    std::io.Error
    std::process.{self, Child, Command}
end

fn main() -> Result<Unit, Error>
    child = process.spawn(Command{program: %q, args: ["nomi-process-helper", "sleep"]})?
    defer Child.kill(child)
    unreachable "stop here"
end
`, helperPath(t))
	p, err := vmhost.LoadSource("main.nomi", src, vmhost.WithErrorOutput(io.Discard))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	start := time.Now()
	err = p.Run(context.Background(), io.Discard, nil, false)
	if err == nil || !strings.Contains(err.Error(), "stop here") {
		t.Fatalf("the run answered %v, want the todo", err)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Fatalf("the trap took %v: it waited for the program instead of killing it", d)
	}
}
