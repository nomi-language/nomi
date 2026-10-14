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

// A script that matches passes: indented `> ` lines feed the reads, output
// sits at the script's left edge, a prompt written without a newline is an
// output line of its own, output starting with `>` at the left edge needs
// no escape, a prompt left waiting at end of input is output like any
// other, and the `"""` literal's missing final newline does not count. A
// script with no input follows the same rule.
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
// an output line `>` at the left edge and each input line follows it,
// indented; an indented `>` alone is an empty line, typed text that itself
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
	if want := "[> \n    > north\nok\n> |> ok\n> ]"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// Every line that is not input is output as written. Padded output, `>>`
// at the left edge and indented, and `>` with no space after it are output.
// An indented output line that starts with `> ` is written with a
// backslash before its `>`, which the script and the transcript both carry;
// without the backslash the line is input, which the program never reads.
// A backslash anywhere else is the character itself.
func TestReplay_OutputThatReadsAsInputIsEscaped(t *testing.T) {
	out := runReplayTests(t, `import std/io

fn quoted() {
    io.print("    > quoted")
    io.print("  >")
    io.print("    >> double")
    io.print(">> at the edge")
    io.print("    >x")
    io.print("\\> kept")
    io.print("    \\x")
    io.write("ask: ")
    _ = io.read_line()
}

test "escaped" {
    assert io.replay(
        """
            \> quoted
          \>
            >> double
        >> at the edge
            >x
        \> kept
            \x
        ask:
            > typed
        """,
        quoted,
    )
}

test "not escaped" {
    refute io.replay(
        """
            > quoted
        """,
        || io.print("    > quoted"),
    )
}

test "a backslash in the output" {
    assert io.replay(
        """
            \\> slashed
        """,
        || io.print("    \\> slashed"),
    )
}
`)
	if !strings.HasSuffix(out, "test result: ok. 3 passed, 0 failed\n") {
		t.Fatalf("want every replay to pass:\n%s", out)
	}
}

// A script the program does not follow fails with a line diff of the script
// (`-`) against the transcript (`+`), each line right of the `│` as written,
// input lines at their indentation, and an input line no read reached is
// named in the reason. The echo of the assertion writes the script as the
// source lines the diff numbers, `""" lines 36-44 """`, rather than
// repeating it.
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
    assert io.replay(""" lines 36-44 """, main)
    io.replay: the transcript (-) and what main printed (+) differ

        36   │ What is the starting balance?
        37   │     > 500
        38 - │ Your starting balance is 500
           + │ Your starting balance: 500
        39   │
        40   │ Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
        41   │ amount:
             │   ... 3 lines match ...
FAIL bank_test.nomi :: input left over
  line 49: 2 input lines of the script were never read
    assert io.replay(""" lines 50-57 """, || {
`
	if !strings.HasPrefix(out, want) {
		t.Fatalf("got:\n%s\nwant it to start with:\n%s", out, want)
	}
	tail := `    io.replay: the transcript (-) and what the function printed (+) differ

             │   ... 3 lines match ...
        53   │
        54   │ Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
        55   │ amount: bye
        56 - │     > 7
        57 - │     > 8
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
    assert io.replay(""" lines 42-44 """, main)
    io.replay: the transcript (-) and what main printed (+) differ

        42   │ What is the starting balance?
        43   │     > 500
        44 - │ Your starting balance is 500
           + │ Your starting balance: 500
           + │ (empty line)
           + │ Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
           + │ amount: bye
FAIL bank_test.nomi :: compared
  line 54: assertion failed
    assert io.capture("500\n", g).output == """ lines 55-56 """
    values:
      g
        = <func: main>
    expected """ ... """ (-) and actual io.capture("500\n", g).output (+) differ

        55   │ What is the starting balance?
        56 - │ Your starting balance is 500
             │ \ no newline at end
           + │ Your starting balance: 500
           + │ (empty line)
           + │ Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
           + │ amount: bye
test result: FAILED. 0 passed, 2 failed
`
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

// A script that is not a `"""` literal at the call has no line numbers: the
// diff keeps its marker and text columns, and the header still names the
// function.
func TestReplay_AScriptNotWrittenAtTheCallIsNotNumbered(t *testing.T) {
	out := runReplayTests(t, bankProgram+`
test "bound script" {
    script = """
        What is the starting balance?
            > 500
        Your starting balance is 500
        """
    assert io.replay(script, main)
}
`)
	want := `    io.replay: the transcript (-) and what main printed (+) differ

          │ What is the starting balance?
          │     > 500
        - │ Your starting balance is 500
        + │ Your starting balance: 500
        + │ (empty line)
        + │ Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
        + │ amount: bye
test result: FAILED. 0 passed, 1 failed
`
	if !strings.HasSuffix(out, want) {
		t.Fatalf("got:\n%s\nwant it to end with:\n%s", out, want)
	}
}

// Captured.transcript and Replayed's fields read as documented: the output
// as written with each read written in as four spaces, `> ` and the text,
// a partial-line prompt on a line of its own, output starting with `>` at
// the left edge as it is, indented output that reads as input escaped, and
// an inner capture's reads only in its own transcript (the outer one escapes
// them when the inner transcript is printed). A replay writes each
// read at the indentation its script line has.
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
        io.print("  > padded")
        _ = io.read_line()
        _ = io.read_line()
    })
    io.write("transcript [${run.transcript}]\n")
    io.write("output [${run.output}]\n")
    r = io.replay("q?\n  > a  \n\t> b\n", || {
        io.print("q?  ")
        _ = io.read_line()
    })
    io.write("replayed [${r.output}|${r.transcript}|${r.expected}|${r.unread}]\n")
}
`, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "transcript [name: \n    > Ada\ngot inner\n    \\> x\n\n> quoted\n  \\> padded\n    > Grace\n]\n" +
		"output [name: got inner\n    > x\n\n> quoted\n  > padded\n]\n" +
		"replayed [q?  \n|q?\n  > a\n|q?\n  > a\n\t> b\n|1]\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A replay called with named arguments, in either order and with a lambda,
// reports as the positional call does: the echo writes the script collapsed
// to the lines it spans, and the diff numbers them by source line.
func TestReplay_NamedArgumentsNumberAndCollapse(t *testing.T) {
	out := runReplayTests(t, bankProgram+`
test "script first" {
    assert io.replay(script: """
        What is the starting balance?
            > 500
        Your starting balance is 500
        """, run: main)
}

test "run first" {
    assert io.replay(run: || io.print("hullo"), script: """
        hello
        """)
}
`)
	want := `FAIL bank_test.nomi :: script first
  line 35: the transcript differs from the script
    assert io.replay(script: """ lines 36-38 """, run: main)
    io.replay: the transcript (-) and what main printed (+) differ

        36   │ What is the starting balance?
        37   │     > 500
        38 - │ Your starting balance is 500
           + │ Your starting balance: 500
`
	if !strings.HasPrefix(out, want) {
		t.Fatalf("got:\n%s\nwant it to start with:\n%s", out, want)
	}
	tail := `FAIL bank_test.nomi :: run first
  line 43: the transcript differs from the script
    assert io.replay(run: || io.print("hullo"), script: """ line 44 """)
    io.replay: the transcript (-) and what the function printed (+) differ

        44 - │ hello
           + │ hullo
`
	if !strings.Contains(out, tail) {
		t.Fatalf("got:\n%s\nwant it to hold:\n%s", out, tail)
	}
}

// A script with no input is the output as written. Its lines sit at the
// closing quotes' column, and indentation past that column is output: a
// line that starts with a space keeps it, and so does output whose every
// line is indented, as padded columns are.
func TestReplay_AScriptWithNoInputIsTheOutputAsWritten(t *testing.T) {
	out := runReplayTests(t, `import std/io

fn main() {
    io.print("-10.0 C |   14.0 F")
    io.print(" -5.0 C |   23.0 F")
}

fn padded() {
    io.print(String.pad_start("1", 6))
    io.print("")
    io.print(String.pad_start("22", 6))
}

test "e2e" {
    assert io.replay(
        """
        -10.0 C |   14.0 F
         -5.0 C |   23.0 F
        """,
        main,
    )
}

test "every line indented" {
    assert io.replay(
        """
             1

            22
        """,
        padded,
    )
}

test "the indentation counts" {
    refute io.replay(
        """
            -10.0 C |   14.0 F
             -5.0 C |   23.0 F
        """,
        main,
    )
}
`)
	if !strings.HasSuffix(out, "test result: ok. 3 passed, 0 failed\n") {
		t.Fatalf("want every replay to pass:\n%s", out)
	}
}

// A script with no input that the output does not follow fails with the
// same line diff, its lines numbered by source line and shown as written,
// indentation included.
func TestReplay_AScriptWithNoInputFailsWithALineDiff(t *testing.T) {
	out := runReplayTests(t, `import std/io

fn main() {
    io.print("-10.0 C |   14.0 F")
    io.print(" -5.0 C |   23.0 F")
}

test "a wrong line" {
    assert io.replay(
        """
        -10.0 C |   14.0 F
         -5.0 C |   24.0 F
        """,
        main,
    )
}

test "indented" {
    assert io.replay(
        """
            -10.0 C |   14.0 F
             -5.0 C |   23.0 F
        """,
        main,
    )
}
`)
	want := `FAIL bank_test.nomi :: a wrong line
  line 9: the transcript differs from the script
    assert io.replay(""" lines 11-12 """, main)
    io.replay: the transcript (-) and what main printed (+) differ

        11   │ -10.0 C |   14.0 F
        12 - │  -5.0 C |   24.0 F
           + │  -5.0 C |   23.0 F
FAIL bank_test.nomi :: indented
  line 19: the transcript differs from the script
    assert io.replay(""" lines 21-22 """, main)
    io.replay: the transcript (-) and what main printed (+) differ

        21 - │     -10.0 C |   14.0 F
        22 - │      -5.0 C |   23.0 F
           + │ -10.0 C |   14.0 F
           + │  -5.0 C |   23.0 F
test result: FAILED. 0 passed, 2 failed
`
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}
