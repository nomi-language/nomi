package vmhost_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/vmhost"
)

// checkTourLesson holds a ```nomi-run lesson block, a First Steps task, to
// its purpose: its starter compiles and fails at least one of its tests,
// the ```nomi-solution fence that follows it passes every test, and the two
// hold the same tests, so the reader's checks are the ones the solution
// was proved against. A lesson has no <!-- expect --> block: the page shows
// nothing until the reader presses Check.
//
// A lesson followed by a ```nomi-bug fence is one where the reader writes
// the tests: the fence is the lesson's code with a mistake planted in it,
// and Check runs the reader's tests on the lesson's code and then on the
// bug in its place (tour-client.mjs, lessonOf). It is held by
// checkTestWritingLesson instead.
func checkTourLesson(t *testing.T, b doctest.Block, solution, bug *doctest.Block) {
	t.Helper()
	if bug != nil {
		checkTestWritingLesson(t, b, solution, bug)
		return
	}
	if b.Expected != nil {
		t.Fatalf("a lesson block shows nothing until the reader presses Check, so it has no <!-- expect ... --> block:\n%s", b.Code)
	}
	if strings.Contains(b.Code, "// FILE:") {
		t.Fatalf("a lesson block is one file, the reader's function and its tests:\n%s", b.Code)
	}
	if solution == nil {
		t.Fatalf("a lesson block is followed by a ```nomi-solution fence, its starter solved:\n%s", b.Code)
	}
	if got, want := lessonTests(solution.Code), lessonTests(b.Code); got != want {
		t.Fatalf("a lesson's solution holds the starter's tests unchanged; the starter's tests:\n%s\n\nthe solution's:\n%s", want, got)
	}
	if lessonTests(b.Code) == "" {
		t.Fatalf("a lesson block holds the `test` blocks that check it:\n%s", b.Code)
	}
	failed, total, msg := lessonRun(t, b.Code)
	if msg != "" {
		t.Fatalf("a lesson's starter compiles, so the reader's first Check shows a test failing, not an error:\n%s\n\nerror:\n%s", b.Code, msg)
	}
	if failed == 0 {
		t.Fatalf("a lesson's starter fails at least one of its %d tests, or the lesson is already done:\n%s", total, b.Code)
	}
	failed, total, msg = lessonRun(t, solution.Code)
	if msg != "" {
		t.Fatalf("a lesson's solution compiles:\n%s\n\nerror:\n%s", solution.Code, msg)
	}
	if failed != 0 {
		t.Fatalf("a lesson's solution passes all its tests; %d of %d failed:\n%s", failed, total, solution.Code)
	}
}

// checkTestWritingLesson holds a lesson whose reader writes the tests. The
// starter and the solution share the code before the first `test` line, the
// part the reader leaves alone. The starter's tests pass on that code and on
// the bug alike, so a first Check is not already done; the solution's pass
// on the code and fail on the bug, so Check accepts them.
func checkTestWritingLesson(t *testing.T, b doctest.Block, solution, bug *doctest.Block) {
	t.Helper()
	if b.Expected != nil {
		t.Fatalf("a lesson block shows nothing until the reader presses Check, so it has no <!-- expect ... --> block:\n%s", b.Code)
	}
	if solution == nil {
		t.Fatalf("a lesson block is followed by a ```nomi-solution fence, its starter solved:\n%s", b.Code)
	}
	if lessonTests(bug.Code) != "" {
		t.Fatalf("a ```nomi-bug fence holds the lesson's code with a mistake, and no tests; the reader's take their place:\n%s", bug.Code)
	}
	code := lessonCode(b.Code)
	if got := lessonCode(solution.Code); got != code {
		t.Fatalf("in a lesson whose reader writes the tests, the solution keeps the starter's code before the first test; the starter's:\n%s\n\nthe solution's:\n%s", code, got)
	}
	for _, c := range []struct {
		what, tests string
		failOnBug   bool
	}{
		{"starter", lessonTests(b.Code), false},
		{"solution", lessonTests(solution.Code), true},
	} {
		if c.tests == "" {
			t.Fatalf("the %s holds at least one test, the shape the reader's take:\n%s", c.what, b.Code)
		}
		failed, total, msg := lessonRun(t, code+c.tests)
		if msg != "" || failed != 0 {
			t.Fatalf("the %s's tests pass on the lesson's code; %d of %d failed:\n%s%s\n\n%s", c.what, failed, total, code, c.tests, msg)
		}
		failed, total, msg = lessonRun(t, bug.Code+"\n\n"+c.tests)
		if msg != "" {
			t.Fatalf("the %s's tests compile against the bug:\n%s\n\nerror:\n%s", c.what, bug.Code, msg)
		}
		if c.failOnBug && failed == 0 {
			t.Fatalf("the solution's tests catch the bug, failing at least one of %d on it:\n%s", total, bug.Code)
		}
		if !c.failOnBug && failed != 0 {
			t.Fatalf("the starter's tests pass on the bug, or the lesson is done before the reader writes a test; %d of %d failed on it", failed, total)
		}
	}
}

// lessonCode is the part of a lesson's file before its first `test` block.
func lessonCode(code string) string {
	return strings.TrimSuffix(code, lessonTests(code))
}

// lessonTests is the part of a lesson's file from its first `test` block
// on.
func lessonTests(code string) string {
	lines := strings.Split(code, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "test ") {
			return strings.Join(lines[i:], "\n")
		}
	}
	return ""
}

// lessonRun loads a lesson's file and runs its tests, answering how many
// cases failed and how many ran, or the compile error.
func lessonRun(t *testing.T, code string) (failed, total int, msg string) {
	t.Helper()
	var errOut bytes.Buffer
	p, hasTests, err := vmhost.LoadMarked(code, t.TempDir(), vmhost.WithErrorOutput(&errOut))
	if err != nil {
		return 0, 0, err.Error()
	}
	if !hasTests {
		return 0, 0, "the file declares no tests"
	}
	if err := p.Run(context.Background(), &errOut, nil, false); err != nil {
		return 0, 0, vmhost.FormatFailure(err)
	}
	var out bytes.Buffer
	for _, c := range p.Cases(&out, vmhost.TestOptions{}) {
		if c.Blocked != nil {
			t.Fatalf("a lesson's test %q is blocked on the VM:\n%s", c.Name, strings.Join(c.Blocked, "\n"))
		}
		total += c.Cases
		failed += c.FailedCases
	}
	return failed, total, ""
}

// lessonSolution is the ```nomi-solution fence that follows a lesson block
// in its page: the first one after the block's line.
func lessonSolution(page string, b doctest.Block) *doctest.Block {
	return lessonFence(page, b, "nomi-solution")
}

// lessonBug is the ```nomi-bug fence that follows a lesson block before the
// next lesson block, or nil.
func lessonBug(page string, b doctest.Block) *doctest.Block {
	bug := lessonFence(page, b, "nomi-bug")
	if bug == nil {
		return nil
	}
	for _, other := range doctest.ExtractBlocks(page, "nomi-run") {
		if other.HasInfo(doctest.InfoLesson) && other.Line > b.Line && other.Line < bug.Line {
			return nil
		}
	}
	return bug
}

func lessonFence(page string, b doctest.Block, lang string) *doctest.Block {
	for _, s := range doctest.ExtractBlocks(page, lang) {
		if s.Line > b.Line {
			return &s
		}
	}
	return nil
}
