package vmhost_test

import (
	"strings"
	"testing"
)

// A reader over chunks the program makes: from_fn's function and
// from_chunks' sequence run when a read needs them, on the reading task,
// inside the capture the read is in, and a trap in either is the
// program's trap at the line that trapped.
func TestReaderSources_RunWhenReadAndInsideTheReadsCapture(t *testing.T) {
	src := `import std/io

fn main() {
    made = io.Reader.from_chunks(["a", "", "b\nc"] |> Iter.map(fn s -> {
        io.print("making ${s}")
        String.to_bytes(s)
    }))
    io.print("before")
    run = io.capture("", fn -> io.Reader.read_line(made))
    io.print("captured [${run.output}] ${run.value}")
    io.print(io.Reader.read_line(made))
    io.print(io.Reader.read_line(made))

    inner = io.Reader.from_text("x\ny\n")
    pulled = io.Reader.from_fn(fn -> io.Reader.read(inner, max: 1))
    io.print(io.Reader.read_all_text(pulled))
}
`
	got, err := runCaptureProgram(t, src, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "before\n" +
		"captured [making a\nmaking \nmaking b\nc\n] Ok(Some(ab))\n" +
		"Ok(Some(c))\n" +
		"Ok(None)\n" +
		"Ok(x\ny\n)\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

func TestReaderSources_ATrapInTheSourceIsTheProgramsTrap(t *testing.T) {
	for name, src := range map[string]string{
		"from_fn": `import std/io

fn main() {
    r = io.Reader.from_fn(fn -> {
        n = 1 / 0
        Ok(Some(String.to_bytes("${n}")))
    })
    io.print(io.Reader.read_line(r))
}
`,
		"from_chunks": `import std/io

fn main() {
    r = io.Reader.from_chunks(Iter.from(0) |> Iter.map(fn n -> String.to_bytes("${1 / n}")))
    io.print(io.Reader.read_line(r))
}
`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runCaptureProgram(t, src, "")
			if err == nil || !strings.Contains(err.Error(), "division by zero") {
				t.Fatalf("run answered %v, want the division-by-zero trap", err)
			}
		})
	}
}
