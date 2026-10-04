// Package expectation is the RECORDED-EXPECTATION instrument: one committed
// artifact per population of runnable Nomi, holding what each case is observed
// to PRINT, so the VM is compared to bytes in the repo rather than to another
// live run.
//
// # Why a committed expectation
//
// A comparison between two live runs cannot see anything the two runs SHARE:
// a bug in `rt` or in the IR builder is reproduced identically on both sides
// and the comparison reports agreement it did not earn. A committed
// expectation does not go blind that way, because it is not produced by the
// code under test. It is GHC's `.stdout` file.
//
// Its instrument is `got == <bytes in git>`. It catches a change in what a
// case prints. It cannot say whether the recorded bytes were right; the
// commit that changes a record has to say why.
//
// # EVERY RECORD CARRIES ITS TRANSCRIPT VERBATIM: THESE ARE GOLDEN FILES
//
// A record holds the normalized text the case printed, not only a hash of it.
// The artifacts are the written-down answers the VM is checked against, and
// they have to stay readable on their own. A hash can say that output moved;
// it cannot say what the right output was.
//
// The failure text is still the scarce part. A population where everything
// passes cannot discriminate a wrong failure message: the commit that found
// `*rt.AssertionFailure.Error()` printing only its header found 45 fixtures
// printing WRONG FAILURE TEXT while the corpus read 614 passed / 0 failed. So
// `Failing` is still derived per case and counted per population, and a
// changed failing record prints both texts in full.
//
// The `H` digest line stays beside the text. Parse re-derives it from the `T`
// lines and rejects a mismatch, so a hand edit to a transcript is an error
// rather than a silently different answer.
//
// # CHANGING A GOLDEN FILE
//
// Regenerate with NOMI_REGENERATE_EXPECTATIONS=1 (see RegenerateRequested),
// review the diff, and name the reason for every moved record in
// the commit message. A golden file changed without a named reason is this
// instrument's failure mode: it defends whatever was last written down.
//
// # What is deliberately NOT in a record
//
//   - Wall time, and nothing in `rt.TestReporter` prints any. If one appeared,
//     it would have to be masked here or every run would report a change.
//   - The absolute worktree root. Two worktrees of one commit must agree, and
//     `rt.DisplayPath` emits an absolute path for anything outside the process
//     working directory. Normalize handles it.
//   - Jittered backoff delays, for the reason
//     `internal/irbuild/differential_test.go` gives for the same mask: the text is
//     the value of a jittered RNG, there is no correct answer for two readings
//     to agree on, and comparing it tests the RNG.
//
// Every mask is a claim that some output is not observable behaviour, and a
// wrong mask hides a real change forever, so the list is short and each entry
// says why.
package expectation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
)

// Case is one case's observed output, as the artifact records it.
//
// Cases is a population-specific denominator and is 0 where the population has
// no sub-case structure — a tour block is one case, a corpus FILE holds several.
// It is recorded because a file whose case count silently fell would otherwise
// keep its exit code and could keep its hash (a dropped case that printed
// nothing moves neither), and "the suite got smaller" is the failure mode a
// digest over output alone cannot see.
type Case struct {
	// ID is the case's stable identity: a population-relative path, plus a
	// block or line discriminator where one file holds several cases. Never
	// absolute, so two worktrees agree.
	ID string
	// Exit is the process exit status the owning command would produce: 1 when
	// anything failed, 0 otherwise.
	Exit int
	// Cases is how many test cases the entry declares, or 0 when the population
	// has no sub-case structure.
	Cases int
	// Digest is the hex sha256 of the normalized transcript. Always set.
	Digest string
	// Transcript is the normalized transcript VERBATIM. Always set, though it
	// is the empty string for a case that printed nothing.
	Transcript string
	// Failing records that the transcript contains a failure report. Derived
	// from Transcript by IsFailing.
	Failing bool
	// Source is the program text of a record whose program exists only inside
	// a test (an inline fixture written to a temp directory), so the record is
	// self-contained and can be re-run. Empty when ID names a repository file.
	Source string
}

// Set is one population's records.
type Set struct {
	// Population is the artifact's name: corpus, stdlib, tour, failure or
	// irbuild.
	Population string
	// What describes, in the artifact itself, exactly what one record covers
	// and how it was produced. It is compared like everything else, so
	// redefining a population without saying so fails the test.
	What string
	// Cases is the records, sorted by ID.
	Cases []Case
}

// FailureMarkers are the substrings that make a transcript a FAILING one.
//
// `FAIL ` is `rt.TestReporter`'s per-case marker (rt/testreport.go) and
// `test result: FAILED` its summary. A non-zero exit alone is NOT the rule: a
// program that exits 1 from a front-end error has no failure REPORT to compare,
// and folding those in would store a hundred analyzer messages verbatim and
// call it failure-text coverage.
var FailureMarkers = []string{"FAIL ", "test result: FAILED"}

// IsFailing reports whether a transcript contains a failure report.
func IsFailing(transcript string) bool {
	for _, m := range FailureMarkers {
		if strings.Contains(transcript, m) {
			return true
		}
	}
	return false
}

// jitteredBackoff is the one RNG-valued mask. Kept spelled identically to
// `internal/irbuild/differential_test.go`'s, because they mask the same text for
// the same reason and two spellings of one rule is how they drift.
var jitteredBackoff = regexp.MustCompile(`retrying in \d+(\.\d+)?[a-z]+`)

// Normalize turns a raw transcript into the comparable form.
//
// root is the directory whose occurrences become `<root>`. Pass the worktree
// root — the parent of the nomi module directory — because that is what leaks
// into output through DisplayPath and through analyzer diagnostics.
func Normalize(transcript, root string) string {
	if root != "" {
		transcript = strings.ReplaceAll(transcript, root+string(filepath.Separator), "<root>/")
		transcript = strings.ReplaceAll(transcript, root, "<root>")
	}
	return jitteredBackoff.ReplaceAllString(transcript, "retrying in <jitter>")
}

// Digest is the hex sha256 of a normalized transcript.
func Digest(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// NewCase builds a record from an already-normalized transcript.
func NewCase(id string, exit, cases int, normalized string) Case {
	return Case{
		ID:         id,
		Exit:       exit,
		Cases:      cases,
		Digest:     Digest(normalized),
		Transcript: normalized,
		Failing:    IsFailing(normalized),
	}
}

// StderrLabel separates the two streams inside a transcript.
const StderrLabel = "--- stderr ---\n"

// Transcript is the one string a record digests: stdout, then stderr under
// StderrLabel when anything reached it.
//
// ONE STRING RATHER THAN TWO FIELDS, because a record is compared as a unit.
// The label is conditional so the common case, a program that printed to
// stdout and nothing to stderr, records exactly what it printed, and a `git
// diff` of a moved record reads like the terminal output it is.
func Transcript(stdout, stderr string) string {
	if stderr == "" {
		return stdout
	}
	return stdout + StderrLabel + stderr
}

// SplitTranscript is Transcript's inverse. A transcript without the label is
// all stdout.
func SplitTranscript(transcript string) (stdout, stderr string) {
	if i := strings.Index(transcript, StderrLabel); i >= 0 {
		return transcript[:i], transcript[i+len(StderrLabel):]
	}
	return transcript, ""
}

// NormalizeDir replaces dir, a directory outside the repository that a test
// wrote its program into, with `<dir>`. A temp directory's name changes on
// every run, so a record of a program that prints its own path would never
// match otherwise. The symlink-resolved spelling is replaced too, longer
// spelling first: on macOS a temp directory is both /var/... and
// /private/var/....
func NormalizeDir(transcript, dir string) string {
	if dir == "" {
		return transcript
	}
	spellings := []string{dir}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
		spellings = append(spellings, resolved)
		sort.Slice(spellings, func(i, j int) bool { return len(spellings[i]) > len(spellings[j]) })
	}
	for _, d := range spellings {
		transcript = strings.ReplaceAll(transcript, d+string(filepath.Separator), "<dir>/")
		transcript = strings.ReplaceAll(transcript, d, "<dir>")
	}
	return transcript
}

// Lookup is the record with the given id.
func (s *Set) Lookup(id string) (Case, bool) {
	for _, c := range s.Cases {
		if c.ID == id {
			return c, true
		}
	}
	return Case{}, false
}

// Add appends a record. Call Sort before Render.
func (s *Set) Add(c Case) { s.Cases = append(s.Cases, c) }

// Sort orders records by ID, which is what makes the artifact a stable file
// rather than a function of map iteration order.
func (s *Set) Sort() {
	sort.Slice(s.Cases, func(i, j int) bool { return s.Cases[i].ID < s.Cases[j].ID })
}

// Failing counts the records whose transcript holds a failure report. This is
// the instrument's POWER reading: a population whose count is 0 can detect that
// output changed and cannot detect a wrong failure message.
func (s *Set) Failing() int {
	n := 0
	for _, c := range s.Cases {
		if c.Failing {
			n++
		}
	}
	return n
}

// TotalCases sums the per-record case counts.
func (s *Set) TotalCases() int {
	n := 0
	for _, c := range s.Cases {
		n += c.Cases
	}
	return n
}

// Fingerprint is one hash over the whole rendered artifact: the single number a
// report can quote. It is derived from Render, so it moves for a changed
// record, a lost record and a new record alike.
func (s *Set) Fingerprint() string { return Digest(string(s.Render())) }

// Render writes the artifact.
//
// LINE-ORIENTED AND ONE FIELD PER LINE, so a `git diff` of a moved expectation
// shows the case that moved and not a re-wrapped blob. Prefixes:
//
//	C <id>       begins a record
//	E <exit>     exit status
//	N <cases>    declared case count
//	H <sha256>   digest of the normalized transcript
//	S <line>     one source line, only for a record that carries its program
//	T <line>     one transcript line, VERBATIM
//
// The `T` lines are the transcript split on "\n", one line each including the
// empty trailing element, so joining them with "\n" reproduces the transcript
// byte for byte. That is why a trailing blank `T` is meaningful and must not be
// tidied away. Every record has at least one `T` line: an empty transcript is
// one bare `T`. `S` lines follow the same rule.
func (s *Set) Render() []byte {
	var b strings.Builder
	b.WriteString("# nomi golden outputs — do not hand-edit\n")
	b.WriteString("#\n")
	b.WriteString("# The VM's output is checked against this file: a written-down answer,\n")
	b.WriteString("# not another engine's.\n")
	b.WriteString("# See internal/expectation/expectation.go for what is masked and why.\n")
	b.WriteString("# Regenerate with NOMI_REGENERATE_EXPECTATIONS=1 and name the reason for\n")
	b.WriteString("# every moved record in the commit message.\n")
	b.WriteString("#\n")
	b.WriteString("# C <id> / E <exit> / N <declared cases> / H <sha256 of transcript>\n")
	b.WriteString("# S <source line>, only for a record that carries its own program\n")
	b.WriteString("# T <transcript line>, verbatim\n")
	fmt.Fprintf(&b, "population %s\n", s.Population)
	for _, line := range strings.Split(s.What, "\n") {
		fmt.Fprintf(&b, "what %s\n", line)
	}
	fmt.Fprintf(&b, "records %d\n", len(s.Cases))
	fmt.Fprintf(&b, "declared-cases %d\n", s.TotalCases())
	fmt.Fprintf(&b, "failing %d\n", s.Failing())
	for _, c := range s.Cases {
		fmt.Fprintf(&b, "C %s\nE %d\nN %d\nH %s\n", c.ID, c.Exit, c.Cases, c.Digest)
		if c.Source != "" {
			writeLines(&b, "S", c.Source)
		}
		writeLines(&b, "T", c.Transcript)
	}
	return []byte(b.String())
}

// writeLines writes text as one prefixed line per "\n"-separated element.
func writeLines(b *strings.Builder, prefix, text string) {
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			b.WriteString(prefix + "\n")
			continue
		}
		b.WriteString(prefix + " " + line + "\n")
	}
}

// Parse reads an artifact back.
//
// It is strict: an unknown prefix, a field before its record or a malformed
// number is an error rather than a skipped line. A lenient reader would let a
// corrupted artifact read as a smaller population and pass.
func Parse(data []byte) (*Set, error) {
	s := &Set{}
	var what []string
	var cur *Case
	var transcript, source []string
	var flushErr error
	flush := func() {
		if cur == nil {
			return
		}
		if len(transcript) == 0 && flushErr == nil {
			flushErr = fmt.Errorf("record %s has no `T` lines; every record carries its transcript", cur.ID)
		}
		cur.Transcript = strings.Join(transcript, "\n")
		cur.Source = strings.Join(source, "\n")
		cur.Failing = IsFailing(cur.Transcript)
		if got := Digest(cur.Transcript); got != cur.Digest && flushErr == nil {
			flushErr = fmt.Errorf("record %s: the `H` digest is not the digest of its `T` lines "+
				"(recorded %.16s, the text hashes to %.16s), so the text was edited by hand",
				cur.ID, cur.Digest, got)
		}
		s.Cases = append(s.Cases, *cur)
		cur, transcript, source = nil, nil, nil
	}
	for i, raw := range strings.Split(string(data), "\n") {
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		key, rest, _ := strings.Cut(raw, " ")
		switch key {
		case "population":
			s.Population = rest
		case "what":
			what = append(what, rest)
		case "records", "declared-cases", "failing":
			// Derived headers. Re-derived from the records rather than trusted,
			// so a hand-edited count cannot make a truncated artifact look whole.
		case "C":
			flush()
			cur = &Case{ID: rest}
		case "E", "N", "H", "S", "T":
			if cur == nil {
				return nil, fmt.Errorf("line %d: %q before any `C` record", i+1, raw)
			}
			switch key {
			case "E":
				n, err := strconv.Atoi(rest)
				if err != nil {
					return nil, fmt.Errorf("line %d: exit %q: %w", i+1, rest, err)
				}
				cur.Exit = n
			case "N":
				n, err := strconv.Atoi(rest)
				if err != nil {
					return nil, fmt.Errorf("line %d: cases %q: %w", i+1, rest, err)
				}
				cur.Cases = n
			case "H":
				cur.Digest = rest
			case "S":
				source = append(source, rest)
			case "T":
				// `T` alone is an empty transcript line; Cut left rest empty
				// either way, so this is lossless for both spellings.
				transcript = append(transcript, rest)
			}
		default:
			return nil, fmt.Errorf("line %d: unknown prefix %q", i+1, key)
		}
	}
	flush()
	if flushErr != nil {
		return nil, flushErr
	}
	s.What = strings.Join(what, "\n")
	return s, nil
}

// Compare reports every way got differs from s, which is the recorded side. It
// is the PRODUCER's check: the whole population, including a recorded case that
// has disappeared, plus the population's name and its own definition of what a
// record covers.
//
// A PARTIAL RUN MUST USE CompareSubset INSTEAD. See its comment.
//
// EVERY DIFFERENCE, NOT THE FIRST. A reader needs to know whether one case
// moved or two hundred did: the two have different causes and the first
// mismatch cannot tell them apart. Bounded, because a whole-population move
// would otherwise print megabytes.
func (s *Set) Compare(got *Set) []string {
	diffs := newDiffs()
	if s.Population != got.Population {
		diffs.report("population: recorded %q, got %q", s.Population, got.Population)
	}
	if s.What != got.What {
		diffs.report("what: the population's own definition changed:\n  recorded: %s\n  got:      %s",
			s.What, got.What)
	}
	seen := s.compareInto(diffs, got)
	for _, w := range s.Cases {
		if !seen[w.ID] {
			diffs.report("%s: RECORDED case is GONE from the population", w.ID)
		}
	}
	return diffs.done()
}

// CompareSubset is the check for a run that covers only SOME of a population,
// and it is a necessity rather than a convenience.
//
// A VM run that blocks on a tenth of the corpus produces a strict subset of the
// recorded ids. `Compare` would report every unrun case as GONE and drown the
// one record that actually disagrees: a module with a blocked prompt would diff
// on ABSENCE.
//
// So this checks only the ids got carries, and reports a NEW id as a difference
// because a run producing a case the recording does not have is a real
// disagreement in either direction.
//
// IT DOES NOT CHECK Population OR What, and the caller must not read that as
// laxity: a run's Set describes its own run, not the recording's. What
// CompareSubset cannot do is notice that the subset shrank, so a caller MUST
// assert its own coverage count separately. A subset check with no denominator
// is satisfiable by running nothing.
func (s *Set) CompareSubset(got *Set) []string {
	diffs := newDiffs()
	s.compareInto(diffs, got)
	return diffs.done()
}

// compareInto is the per-record comparison both entry points share, and returns
// the recorded ids got covered.
func (s *Set) compareInto(diffs *diffList, got *Set) map[string]bool {
	want := make(map[string]Case, len(s.Cases))
	for _, c := range s.Cases {
		want[c.ID] = c
	}
	seen := make(map[string]bool, len(got.Cases))
	for _, g := range got.Cases {
		seen[g.ID] = true
		w, ok := want[g.ID]
		if !ok {
			diffs.report("%s: NEW case, not in the recorded expectation (exit %d, %d declared, failing=%v)",
				g.ID, g.Exit, g.Cases, g.Failing)
			continue
		}
		if w.Exit != g.Exit {
			diffs.report("%s: exit status %d, recorded %d", g.ID, g.Exit, w.Exit)
		}
		if w.Cases != g.Cases {
			diffs.report("%s: declares %d case(s), recorded %d — the suite changed size",
				g.ID, g.Cases, w.Cases)
		}
		if w.Digest == g.Digest {
			continue
		}
		// THE FAILURE-TEXT BRANCH PRINTS BOTH TEXTS in full: failure text is
		// where a wrong answer is legible.
		if w.Failing || g.Failing {
			diffs.report("%s: FAILURE TEXT CHANGED\n--- recorded ---\n%s\n--- got ---\n%s",
				g.ID, w.Transcript, g.Transcript)
			continue
		}
		diffs.report("%s: output changed\n%s", g.ID, LineDiff(w.Transcript, g.Transcript))
	}
	return seen
}

// LineDiff says where two transcripts first differ: the line number and a few
// lines of each side from there. A whole long transcript twice would bury the
// line that moved.
func LineDiff(recorded, got string) string {
	a, b := strings.Split(recorded, "\n"), strings.Split(got, "\n")
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	const context = 4
	side := func(lines []string) string {
		end := min(len(lines), i+context)
		if i >= end {
			return "    <end of text>"
		}
		var out []string
		for _, l := range lines[i:end] {
			out = append(out, "    "+strconv.Quote(l))
		}
		return strings.Join(out, "\n")
	}
	return fmt.Sprintf("  first difference at line %d (%d lines recorded, %d got)\n"+
		"  --- recorded ---\n%s\n  --- got ---\n%s", i+1, len(a), len(b), side(a), side(b))
}

// diffList is a bounded difference accumulator.
type diffList struct {
	out []string
}

// maxReportedDiffs bounds a report. A producer bug moves every record, and an
// unbounded report would put megabytes into a test log.
const maxReportedDiffs = 40

func newDiffs() *diffList { return &diffList{} }

func (d *diffList) report(format string, args ...any) {
	if len(d.out) < maxReportedDiffs {
		d.out = append(d.out, fmt.Sprintf(format, args...))
	}
}

func (d *diffList) done() []string {
	if len(d.out) == maxReportedDiffs {
		return append(d.out, fmt.Sprintf("... and more; only the first %d are shown", maxReportedDiffs))
	}
	return d.out
}

// Dir is the committed artifacts' directory, resolved from this source file so
// it is the same answer from every test package's working directory.
//
// `testdata` rather than a package directory: the go tool ignores it, so the
// artifacts are never inputs to a build, and `nomi/lsp` walks `.nomi` by
// WalkDir with no build-graph edge — a `.expect` file is invisible to it.
func Dir() (string, error) {
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		return "", fmt.Errorf("expectation: locating this source file")
	}
	// .../internal/expectation/expectation.go
	dir := filepath.Join(filepath.Dir(file), "..", "..", "testdata", "expectations")
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// Root is the repository root, which is also the compiler module root: what
// leaks into transcripts and what Normalize takes out.
func Root() (string, error) {
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		return "", fmt.Errorf("expectation: locating this source file")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

// Path is the artifact path for a population.
func Path(population string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, population+".expect"), nil
}

// Load reads a population's committed artifact.
func Load(population string) (*Set, error) {
	path, err := Path(population)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Store writes a population's artifact. Used by the regeneration path, which
// is opt-in through an environment variable so a run cannot quietly rewrite the
// expectation it was supposed to check.
func Store(s *Set) error {
	path, err := Path(s.Population)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, s.Render(), 0o644)
}

// RegenerateRequested reports whether the caller asked for the artifacts to be
// rewritten rather than checked.
//
// AN ENVIRONMENT VARIABLE AND NOT A FLAG, and the distinction is the point: a
// `-update` flag is one keystroke away from the ordinary command, and the
// failure mode of this whole instrument is somebody regenerating the
// expectation to make a red test green. This spelling makes that an explicit,
// greppable act.
func RegenerateRequested() bool {
	return os.Getenv("NOMI_REGENERATE_EXPECTATIONS") == "1"
}
