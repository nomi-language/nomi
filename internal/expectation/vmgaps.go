package expectation

import (
	"fmt"
	"strings"
)

// A VM GAP is a run where the VM could not RUN the program, as opposed to
// running it to a different answer: `nomi run` stopped with a body the
// compiler cannot lower, `nomi test` reported a case BLOCKED, the VM stopped
// at a machine limit, or its lowering panicked. A gap's transcript is not an
// answer, so it is never recorded: checking or regenerating a golden file, a
// gap fails and names the VM's first reason. JudgeVM tells the two failures
// apart so the message says which one it is.

// isUnloweredDiagnostic reports whether line is the first line of the
// diagnostic vmhost gives for a body the compiler cannot lower
// (vmhost.Program.Unsupported): `error: <construct> is not supported yet, so
// <declaration> cannot run`, or the same message in the short form.
func isUnloweredDiagnostic(line string) bool {
	i := strings.Index(line, " is not supported yet, so ")
	return i >= 0 && strings.HasSuffix(line, " cannot run")
}

// VMGap reports whether a VM transcript says the VM could not run the
// program, and answers the first line that says so.
func VMGap(transcript string) (string, bool) {
	for _, line := range strings.Split(transcript, "\n") {
		switch {
		case strings.HasPrefix(line, "BLOCKED "):
			return strings.TrimPrefix(line, "BLOCKED "), true
		case isUnloweredDiagnostic(line):
			return strings.TrimPrefix(line, "error: "), true
		case strings.Contains(line, "irbuild test harness: panic"):
			return line, true
		case strings.Contains(line, "the VM cannot run this program"):
			return line, true
		}
	}
	return "", false
}

// Verdict is how a VM run stands against a record.
type Verdict int

const (
	// Match: the VM reproduced the record.
	Match Verdict = iota
	// Gap: the VM could not run the program, and every line it printed for
	// the cases it did run is in the record, in order.
	Gap
	// Wrong: the VM ran the program to a different answer.
	Wrong
)

// JudgeVM judges a VM run of a record's program. For a Gap it answers the VM's
// first reason; for Wrong, what differs.
func JudgeVM(want Case, text string, exit int) (Verdict, string) {
	if text == want.Transcript && exit == want.Exit {
		return Match, ""
	}
	reason, gap := VMGap(text)
	if !gap {
		if text != want.Transcript {
			return Wrong, fmt.Sprintf("output differs (exit %d, recorded %d)\n%s",
				exit, want.Exit, LineDiff(want.Transcript, text))
		}
		return Wrong, fmt.Sprintf("exit status %d, recorded %d", exit, want.Exit)
	}
	// What ran must agree with the record: every line but the VM's own
	// BLOCKED lines and the indented lines under them, its summary and its
	// closing line. A run stops at its first unlowered body's diagnostic,
	// before its first effect, so the diagnostics end the transcript.
	var ran []string
	underBlocked := false
	for _, line := range strings.Split(text, "\n") {
		if isUnloweredDiagnostic(line) {
			break
		}
		if underBlocked && strings.HasPrefix(line, "  ") {
			continue
		}
		underBlocked = strings.HasPrefix(line, "BLOCKED ")
		switch {
		case line == "",
			strings.HasPrefix(line, "BLOCKED "),
			strings.HasPrefix(line, "test result: "),
			line+"\n" == StderrLabel,
			strings.Contains(line, "the VM cannot run this program"),
			strings.Contains(line, "irbuild test harness: panic"):
			continue
		}
		ran = append(ran, line)
	}
	record := strings.Split(want.Transcript, "\n")
	at := 0
	for _, l := range ran {
		for at < len(record) && record[at] != l {
			at++
		}
		if at == len(record) {
			return Wrong, fmt.Sprintf("the VM could not run all of it (%s), and what it did run "+
				"printed %q, which the record does not hold at that point:\n%s",
				reason, l, LineDiff(want.Transcript, text))
		}
		at++
	}
	return Gap, reason
}
