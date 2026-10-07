package lsp

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// fuzzing reports whether this process runs with -fuzz: the seeds are then
// the fuzzer's starting corpus.
func fuzzing() bool {
	f := flag.Lookup("test.fuzz")
	return f != nil && f.Value.String() != ""
}

// fuzzSeedLimit bounds the size of a seed the fuzzer starts from: a large
// file makes every input slow.
const fuzzSeedLimit = 12 << 10

// fuzzEditor is the one server a fuzz process sends every input to, as an
// editor's long-lived server sees many documents.
var fuzzEditor struct {
	ss     *typingSession
	counts typingCounts
	n      int
}

// FuzzLSPSurvivesEdits opens a mutated seed in the language server and
// edits it: the battery at the end and at two other places, keystroke
// requests at four more, then a stretch deleted from the middle, a block
// pasted, and a save. It fails on any panic, hang or internal-error
// diagnostic no known gap (knownTypingGaps) explains. Run it with
//
//	go test ./internal/lsp -run '^$' -fuzz '^FuzzLSPSurvivesEdits$' -fuzztime 5m -fuzzminimizetime 5s -parallel 4
//
// Each input takes a fraction of a second, so the default minute spent
// minimizing every new interesting input stalls the run; hence the short
// -fuzzminimizetime. NOMI_LSP_SLOW_LOG=<file> appends every request over
// typingSlow on a small input to file. NOMI_LSP_TYPING=full replays every
// seed under plain `go test`.
//
// A failing input is written under internal/lsp/testdata/fuzz/; plain `go
// test` replays everything there, so triage an input before committing
// it.
func FuzzLSPSurvivesEdits(f *testing.F) {
	recordPanics(f)
	if fuzzing() || os.Getenv("NOMI_LSP_TYPING") == "full" {
		for _, s := range append(trackedSeeds(f), tourTypingSeeds(f)...) {
			if len(s.text()) <= fuzzSeedLimit {
				f.Add(s.text())
			}
		}
	} else {
		// Plain `go test` runs a few seeds, so the target itself stays
		// working.
		tour := tourTypingSeeds(f)
		for _, i := range []int{0, len(tour) / 2, len(tour) - 1} {
			f.Add(tour[i].text())
		}
	}
	fuzzEditor.ss = newTypingSession(f, "fuzz", map[string]string{"nomi.toml": "[module]\nname = \"app\"\n"}, "seed0.nomi", &fuzzEditor.counts)
	f.Cleanup(fuzzEditor.ss.close)
	f.Fuzz(func(t *testing.T, src string) {
		if !utf8.ValidString(src) || len(src) > 2*fuzzSeedLimit {
			// An editor sends UTF-8; JSON would replace the invalid bytes.
			t.Skip()
		}
		ss := fuzzEditor.ss
		fuzzEditor.n++
		ss.reopen(t, src, fmt.Sprintf("seed%d.nomi", fuzzEditor.n))
		editAll(ss, src)
		ss.collect()
		logSlow(ss)
		for _, fd := range ss.findings {
			if knownTypingGapFor(fd) == nil {
				t.Fatalf("%s", fd)
			}
		}
	})
}

// editAll is one fuzz input's edits of src.
func editAll(ss *typingSession, src string) {
	ss.setText(src)
	ss.battery(len(src))
	if len(src) == 0 {
		return
	}
	at := func() int {
		off := ss.rng.Intn(len(src) + 1)
		for off > 0 && off < len(src) && !utf8.RuneStart(src[off]) {
			off--
		}
		return off
	}
	ss.battery(at())
	for range 4 {
		ss.keystroke(at())
	}
	// Delete a stretch, as a selection cut, then type one character there.
	from, to := at(), at()
	if from > to {
		from, to = to, from
	}
	ss.setText(src[:from] + src[to:])
	ss.keystroke(from)
	ss.setText(src[:from] + "." + src[to:])
	ss.keystroke(from + 1)
	ss.battery(from + 1)
	// Paste a block of lines at a line start.
	lines := strings.SplitAfter(src, "\n")
	first := ss.rng.Intn(len(lines))
	block := strings.Join(lines[first:min(first+1+ss.rng.Intn(8), len(lines))], "")
	p := lineStart(src, at())
	ss.setText(src[:p] + block + src[p:])
	ss.battery(p + len(block))
	ss.setText(src)
	ss.save()
	ss.battery(at())
}

// logSlow appends each slow request of a small input, with the input, to
// the file NOMI_LSP_SLOW_LOG names. A fuzz worker's log never reaches the
// terminal, so this is how a run reports them.
func logSlow(ss *typingSession) {
	path := os.Getenv("NOMI_LSP_SLOW_LOG")
	if path == "" || len(ss.slow) == 0 {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	for _, r := range ss.slow {
		if r.lines <= smallFileLines {
			fmt.Fprintf(f, "%s took %v on %d lines; input %q\n", r.method, r.took, r.lines, ss.seed)
		}
	}
}
