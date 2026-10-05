package expectation

import (
	"strings"
	"testing"
)

// TestJudgeVM_SeparatesAGapFromAWrongAnswer is the positive control for the
// gap classifier: a run that could not finish is a Gap only while every line it did
// print is in the record, in order; a line the record lacks, or a changed
// answer with nothing blocked, is Wrong.
func TestJudgeVM_SeparatesAGapFromAWrongAnswer(t *testing.T) {
	rec := NewCase("f", 1, 3, "ok a\nFAIL b\n  line 4: assertion failed\nok c\ntest result: FAILED. 2 passed, 1 failed\n")

	if v, _ := JudgeVM(rec, rec.Transcript, 1); v != Match {
		t.Fatalf("the record itself judged %v", v)
	}
	gap := "ok a\nBLOCKED f :: b [test body] not retained: x\nok c\ntest result: BLOCKED. 2 passed, 0 failed, 1 blocked\n"
	if v, reason := JudgeVM(rec, gap, 1); v != Gap || !strings.Contains(reason, "not retained: x") {
		t.Fatalf("a blocked case judged %v (%q)", v, reason)
	}
	located := "ok a\nBLOCKED f :: b f:9:20: this call to `g` is not supported yet, so `fn h` cannot run\n" +
		"  f:9:20: help: pass it\nok c\ntest result: BLOCKED. 2 passed, 0 failed, 1 blocked\n"
	if v, reason := JudgeVM(rec, located, 1); v != Gap || !strings.Contains(reason, "this call to `g`") {
		t.Fatalf("a blocked case with its hint judged %v (%q)", v, reason)
	}
	wrongLine := "ok a\nBLOCKED f :: b [test body] not retained: x\nFAIL c\ntest result: FAILED. 1 passed, 1 failed, 1 blocked\n"
	if v, _ := JudgeVM(rec, wrongLine, 1); v != Wrong {
		t.Fatalf("a blocked run whose other case failed differently judged %v", v)
	}
	reordered := "ok c\nBLOCKED f :: b [test body] not retained: x\nok a\ntest result: BLOCKED. 2 passed, 0 failed, 1 blocked\n"
	if v, _ := JudgeVM(rec, reordered, 1); v != Wrong {
		t.Fatalf("a reordering judged %v", v)
	}
	if v, _ := JudgeVM(rec, strings.Replace(rec.Transcript, "line 4", "line 5", 1), 1); v != Wrong {
		t.Fatalf("a changed answer with nothing blocked judged %v", v)
	}
	if v, _ := JudgeVM(rec, rec.Transcript, 0); v != Wrong {
		t.Fatalf("a changed exit status judged %v", v)
	}
	program := StderrLabel + "BLOCKED f [main] not retained: y\nthe VM cannot run this program\n"
	if v, _ := JudgeVM(NewCase("p", 0, 0, "hello\n"), program, 1); v != Gap {
		t.Fatalf("a blocked program judged %v", v)
	}
	unlowered := StderrLabel + "error: this call to `g` is not supported yet, so `fn main` cannot run\n" +
		"  --> main.nomi:3:3\n   |\n 3 |   g()\n   |   ^\n"
	if v, reason := JudgeVM(NewCase("p", 0, 0, "hello\n"), unlowered, 1); v != Gap || !strings.HasPrefix(reason, "this call to `g`") {
		t.Fatalf("a run stopped at an unlowered body judged %v (%q)", v, reason)
	}
}
