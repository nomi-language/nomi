package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// bankProgram is the interactive program the replay tests drive: it prompts on
// a line of its own, then on a partial line, and reads until end of input.
const bankProgram = `import std/io

fn main(): Result<Unit, String> {
    io.print("What is the starting balance?")
    balance = case io.read_line() {
        Ok(line) -> String.to_int(line) |> Maybe.with_default(0)
        Err(_) -> 0
    }
    io.print("Your starting balance: ${balance}")
    io.print("")
    io.print("Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)")
    loop(balance)
    Ok(Unit)
}

fn loop(balance: Int): Unit {
    io.write("amount: ")
    case io.read_line() {
        Ok(line) -> case String.split(line, " ") {
            ["deposit", n] -> {
                amount = String.to_int(n) |> Maybe.with_default(0)
                io.print("${amount} deposited, new balance is ${balance + amount}")
                loop(balance + amount)
            }
            _ -> {
                io.print("> unknown command")
                loop(balance)
            }
        }
        Err(_) -> io.print("bye")
    }
}
`

// runReplayTests loads src as a test file and answers the reporter's text.
func runReplayTests(t *testing.T, src string) string {
	t.Helper()
	t.Setenv("NOMI_COLOR", "never")
	const name = "bank_test.nomi"
	p, err := vmhost.LoadSource(name, src)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	return buf.String()
}

// A script that matches passes: `> ` lines feed the reads, every output line
// sits behind a two-space gutter, a prompt written without a newline is an
// output line of its own, output starting with `>` needs no escape, a prompt
// left waiting at end of input is output like any other, and the `"""`
// literal's missing final newline does not count.
func TestReplay_PassesOnAMatchingScript(t *testing.T) {
	out := runReplayTests(t, bankProgram+`
test "deposit money" {
    assert io.replay("""
          What is the starting balance?
        > 500
          Your starting balance: 500

          Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
          amount:
        > deposit 25
          25 deposited, new balance is 525
          amount:
        > withdraw
          > unknown command
          amount: bye
        """, main)
}

test "no input at all" {
    assert io.replay("""
          What is the starting balance?
          Your starting balance: 0

          Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
          amount: bye
        """, main)
}
`)
	if !strings.HasSuffix(out, "test result: ok. 2 passed, 0 failed\n") {
		t.Fatalf("want both replays to pass:\n%s", out)
	}
}

// The session from the io.write prompt example reads as the terminal shows
// it: the prompt is output on its own line and the typed text is below it.
func TestReplay_APartialLinePromptIsOutput(t *testing.T) {
	out := runReplayTests(t, `import std/io

fn main() {
    io.print("What is your name?")
    name = io.read_line() |> Result.with_default("")
    io.write("Hello, ${name}. Your age: ")
    age = io.read_line() |> Result.with_default("")
    io.print("${name} is ${age}")
}

test "asks name and age" {
    assert io.replay(
        """
          What is your name?
        > Ada
          Hello, Ada. Your age:
        > 36
          Ada is 36
        """,
        main,
    )
}
`)
	if !strings.HasSuffix(out, "test result: ok. 1 passed, 0 failed\n") {
		t.Fatalf("want the replay to pass:\n%s", out)
	}
}

// A program whose prompt is `> ` replays from a script where each prompt is
// an output line `  >` and each input line follows it; typed text that itself
// starts with `>` follows `> `, and the prompt left waiting at end of input
// is output on the last line.
func TestReplay_APromptStartingWithTheMarker(t *testing.T) {
	out := runReplayTests(t, `import std/io

fn main() {
    io.print("Exits: north, east.")
    play()
}

fn play() {
    io.write("> ")
    case io.read_line() {
        Ok("north") -> {
            io.print("You are in an old armory.")
            play()
        }
        Ok(word) -> {
            io.print("I don't know how to '${word}'.")
            play()
        }
        Err(_) -> io.print("Bye.")
    }
}

test "walks north" {
    assert io.replay("""
          Exits: north, east.
          >
        > north
          You are in an old armory.
          >
        >
          I don't know how to ''.
          >
        > > dance
          I don't know how to '> dance'.
          > Bye.
        """, main)
}
`)
	if !strings.HasSuffix(out, "test result: ok. 1 passed, 0 failed\n") {
		t.Fatalf("want the replay to pass:\n%s", out)
	}
	got, err := runCaptureProgram(t, `import std/io

fn main() {
    run = io.capture("north\n", || {
        io.write("> ")
        _ = io.read_line()
        io.print("ok")
        io.write("> ")
        _ = io.read_line()
    })
    io.write("[${run.transcript}|${run.output}]")
}
`, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "[  > \n> north\n  ok\n  > |> ok\n> ]"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// A script line that is neither input (`> `) nor output (two spaces) traps
// before the program runs, naming the line, even with no input lines.
func TestReplay_AScriptLineWithoutTheGutterTraps(t *testing.T) {
	out := runReplayTests(t, `import std/io

test "no gutter" {
    assert io.replay("""
          Your age:
        > 36
        Ada is 36
        """, || {
        io.print("ran")
    })
}

test "output only, no gutter" {
    refute io.replay("""
        > not input
        """, || io.print("> not input"))
}

test "output only, with the gutter" {
    assert io.replay("""
          > not input
        """, || io.print("> not input"))
}
`)
	for _, want := range []string{
		"io.replay: line 3 of the script, \"Ada is 36\", must start with `> ` (input) or two spaces (output)",
		"test result: FAILED. 2 passed, 1 failed\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("got:\n%s\nwant it to contain:\n%s", out, want)
		}
	}
	if strings.Contains(out, "ran") {
		t.Fatalf("the program ran under a malformed script:\n%s", out)
	}
}

// A script the program does not follow fails with a line diff of the script
// (`-`) against the transcript (`+`), both in the two-column form, and a `>`
// line no read reached is named in the reason.
func TestReplay_FailureIsALineDiff(t *testing.T) {
	out := runReplayTests(t, bankProgram+`
test "deposit money" {
    assert io.replay("""
          What is the starting balance?
        > 500
          Your starting balance is 500

          Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
          amount:
        > deposit 25
          25 deposited, new balance is 525
          amount: bye
        """, main)
}

test "input left over" {
    assert io.replay("""
          What is the starting balance?
        > 500
          Your starting balance: 500

          Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
          amount: bye
        > 7
        > 8
        """, || {
        io.print("What is the starting balance?")
        _ = io.read_line()
        io.print("Your starting balance: 500")
        io.print("")
        io.print("Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)")
        io.write("amount: ")
        io.print("bye")
    })
}
`)
	want := `FAIL bank_test.nomi :: deposit money
  line 35: the transcript differs from the script
    assert io.replay(
        """
          What is the starting balance?
        > 500
          Your starting balance is 500

          Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
          amount:
        > deposit 25
          25 deposited, new balance is 525
          amount: bye
        """,
        main,
    )
    diff (- expected, + actual):
          What is the starting balance?
        > 500
      -   Your starting balance is 500
      +   Your starting balance: 500

          Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
          amount:
      ... 3 unchanged lines
FAIL bank_test.nomi :: input left over
  line 49: 2 input lines of the script were never read
`
	if !strings.HasPrefix(out, want) {
		t.Fatalf("got:\n%s\nwant it to start with:\n%s", out, want)
	}
	tail := `    diff (- expected, + actual):
      ... 3 unchanged lines

          Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
          amount: bye
      - > 7
      - > 8
test result: FAILED. 0 passed, 2 failed
`
	if !strings.HasSuffix(out, tail) {
		t.Fatalf("got:\n%s\nwant it to end with:\n%s", out, tail)
	}
}

// A failure that shows a diff prints no `values:` row the diff repeats: not
// the replay script's row, though it interpolates a setup value and is no
// plain literal, not the `==` operands', and not `main`'s, whose value only
// names `main` again. `g`'s row stays: it says which function `g` holds.
func TestReplay_DiffHidesTheRowsItRepeats(t *testing.T) {
	out := runReplayTests(t, bankProgram+`
tests "bank" {
    setup {
        500
    }

    test "replayed", starting_balance {
        assert io.replay(
            """
              What is the starting balance?
            > ${starting_balance}
              Your starting balance is ${starting_balance}
            """,
            main,
        )
    }
}

test "compared" {
    starting_balance = 500
    g = main
    assert io.capture("500\n", g).output == """
        What is the starting balance?
        Your starting balance is ${starting_balance}
        """
}
`)
	want := `FAIL bank_test.nomi :: bank / replayed
  line 40: the transcript differs from the script
    assert io.replay(
        """
          What is the starting balance?
        > ${starting_balance}
          Your starting balance is ${starting_balance}
        """,
        main,
    )
    diff (- expected, + actual):
          What is the starting balance?
        > 500
      -   Your starting balance is 500
      +   Your starting balance: 500
      +
      +   Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
      +   amount: bye
FAIL bank_test.nomi :: compared
  line 54: assertion failed
    assert io.capture("500\n", g).output == """
        What is the starting balance?
        Your starting balance is ${starting_balance}
        """
    values:
      g
        = <func: main>
    diff (- expected """ ... """, + actual io.capture("500\n", g).output):
        What is the starting balance?
      - Your starting balance is 500
      \ no newline at end
      + Your starting balance: 500
      +
      + Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
      + amount: bye
test result: FAILED. 0 passed, 2 failed
`
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

// Captured.transcript and Replayed's fields read as documented: the output
// behind the gutter with each read written in as `> ` and the text, a
// partial-line prompt on a line of its own, output starting with `>` as it
// is, and an inner capture's reads only in its own transcript.
func TestReplay_TranscriptFields(t *testing.T) {
	got, err := runCaptureProgram(t, `import std/io

fn main() {
    run = io.capture("Ada\nGrace\n", || {
        io.write("name: ")
        _ = io.read_line()
        inner = io.capture("x\n", || {
            io.print("inner")
            io.read_line()
        })
        io.print("got ${inner.transcript}")
        io.print("> quoted")
        _ = io.read_line()
        _ = io.read_line()
    })
    io.write("transcript [${run.transcript}]\n")
    io.write("output [${run.output}]\n")
    r = io.replay("  q?\n> a  \n> b\n", || {
        io.print("q?  ")
        _ = io.read_line()
    })
    io.write("replayed [${r.output}|${r.transcript}|${r.expected}|${r.unread}]\n")
}
`, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "transcript [  name: \n> Ada\n  got   inner\n  > x\n\n  > quoted\n> Grace\n]\n" +
		"output [name: got   inner\n> x\n\n> quoted\n]\n" +
		"replayed [q?  \n|  q?\n> a\n|  q?\n> a\n> b\n|1]\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
