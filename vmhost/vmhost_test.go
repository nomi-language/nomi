package vmhost_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A literal of a struct whose field is a Map of function values is a body
// the IR builder declines, so a caller that reaches it is the fixture for
// a blocked program. If the builder learns that
// shape these tests fail on their positive controls and need another
// unretained construct, not a looser assertion.
const blockedProgram = `import std/io

fn count(n: Int, acc: Int): Int {
  if n == 0 {
    acc
  } else {
    weigh(Node{f: Map.empty()}) + 3
  }
}

fn main() {
  io.print("before")
  io.print(Int.to_string(count(3, 0)))
}

test "counts" {
  assert count(3, 0) == 3
}

test "adds" {
  assert 1 + 2 == 3
}

test "fails" {
  assert 1 + 2 == 4
}

struct Node {
  f: Map<String, (Int) -> Int>
}

fn weigh(_d: Node): Int {
  0
}
`

func writeProgram(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRun_ARetainedProgramRunsOnTheVM(t *testing.T) {
	path := writeProgram(t, `import std/io

fn double(n: Int): Int {
  n * 2
}

fn main() {
  io.print(Int.to_string(double(21)))
}
`)
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.String() != "42\n" {
		t.Fatalf("output %q, want %q", out.String(), "42\n")
	}
}

// The run is refused before its first effect: `main` prints "before" first,
// and nothing is printed.
func TestRun_ABlockedProgramNamesItsFunctionAndRunsNothing(t *testing.T) {
	path := writeProgram(t, blockedProgram)
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = p.Run(context.Background(), &out, nil, false)
	blocked, ok := vmhost.IsBlocked(err)
	if !ok {
		t.Fatalf("want a *Blocked, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("a blocked program printed %q; it must be refused before its first effect", out.String())
	}
	want := "[count] not retained: a struct literal: Node"
	if len(blocked.Reasons) != 1 || blocked.Reasons[0] != want {
		t.Fatalf("reasons %q, want [%q]", blocked.Reasons, want)
	}
	// The report is the diagnostic `nomi check` gives, at the struct literal
	// the lowering stopped at, and never the builder's reason.
	diag := "this struct literal is not supported yet, so `fn count` cannot run"
	var report bytes.Buffer
	blocked.Write(&report, "main.nomi")
	wantReport := "error: " + diag + "\n --> " + path + ":7:11\n  |\n7 |     weigh(Node{f: Map.empty()}) + 3\n  |           ^^^^\n  = help: " + gapHint + "\n"
	if report.String() != wantReport {
		t.Fatalf("report:\n%s\nwant:\n%s", report.String(), wantReport)
	}
	if got := blocked.Error(); got != path+":7:11: "+diag+"\n"+path+":7:11: help: "+gapHint {
		t.Fatalf("Error() = %q", got)
	}
	checked := vmhost.Check(path)
	if checked == nil || checked.Error() != blocked.Error() {
		t.Fatalf("Check answered %v; want the run's diagnostic %q", checked, blocked.Error())
	}
}

// A blocker that is not a declined body (here a host function nothing
// binds) keeps its BLOCKED line and closing line.
func TestBlocked_WithoutADiagnosticKeepsItsReason(t *testing.T) {
	b := &vmhost.Blocked{Reasons: []string{"[vm] machine limit: x"}}
	var report bytes.Buffer
	b.Write(&report, "main.nomi")
	if want := "BLOCKED main.nomi [vm] machine limit: x\nthe VM cannot run this program\n"; report.String() != want {
		t.Fatalf("report %q, want %q", report.String(), want)
	}
}

// Every case is reported, in source order: the
// retained ones run to their verdict and the blocked one names its reason.
func TestTest_ReportsRunnableAndBlockedCasesInOrder(t *testing.T) {
	path := writeProgram(t, blockedProgram)
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cases := p.Cases(&bytes.Buffer{}, vmhost.TestOptions{})
	if len(cases) != 3 {
		t.Fatalf("%d cases, want 3: %+v", len(cases), cases)
	}
	diag := path + ":7:11: this struct literal is not supported yet, so `fn count` cannot run\n" +
		path + ":7:11: help: " + gapHint
	if cases[0].Name != "counts" || len(cases[0].Blocked) != 1 || cases[0].Blocked[0] != diag ||
		len(cases[0].Reasons) != 1 || cases[0].Reasons[0] != "[count] not retained: a struct literal: Node" ||
		cases[0].BlockedPath != path || cases[0].BlockedLine != 7 {
		t.Fatalf("case 0 = %+v, want counts blocked on count", cases[0])
	}
	if cases[1].Name != "adds" || cases[1].Blocked != nil || cases[1].Err != nil {
		t.Fatalf("case 1 = %+v, want adds passing", cases[1])
	}
	if cases[2].Name != "fails" || cases[2].Blocked != nil || cases[2].Err == nil {
		t.Fatalf("case 2 = %+v, want fails failing", cases[2])
	}

	var report bytes.Buffer
	rep := vmhost.NewTestReport(&report)
	p.Test(&report, rep, "/abs/f.nomi", vmhost.TestOptions{}, func(n string) string { return "f :: " + n })
	rep.Summary()
	text := report.String()
	for _, want := range []string{
		"BLOCKED f :: counts " + strings.Replace(diag, "\n", "\n  ", 1) + "\n",
		"f :: adds\n",
		"f :: fails\n",
		"1 passed, ",
		" failed, ",
		"1 blocked\n",
	} {
		if !strings.Contains(stripANSI(text), want) {
			t.Fatalf("report lacks %q:\n%s", want, text)
		}
	}
}

// A blocked JSON record carries the diagnostic as its message and the line of
// the code the compiler could not lower as its error_line.
func TestTest_JSONBlockedRecordLocatesItsBlocker(t *testing.T) {
	path := writeProgram(t, blockedProgram)
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	rep := vmhost.NewTestReportFormat(&report, vmhost.TestFormatJSON)
	p.Test(&bytes.Buffer{}, rep, path, vmhost.TestOptions{}, func(n string) string { return n })
	first, _, _ := strings.Cut(report.String(), "\n")
	var rec map[string]any
	if err := json.Unmarshal([]byte(first), &rec); err != nil {
		t.Fatalf("%v: %s", err, first)
	}
	want := path + ":7:11: this struct literal is not supported yet, so `fn count` cannot run\n" +
		path + ":7:11: help: " + gapHint
	if rec["status"] != "blocked" || rec["message"] != want || rec["error_line"] != float64(7) {
		t.Fatalf("blocked record %s", first)
	}
}

// --line selects the one case whose lines contain it.
func TestTest_LineSelectsOneCase(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, blockedProgram))
	if err != nil {
		t.Fatal(err)
	}
	cases := p.Cases(&bytes.Buffer{}, vmhost.TestOptions{Line: 20, LineSet: true})
	if len(cases) != 1 || cases[0].Name != "adds" || cases[0].Err != nil || cases[0].Blocked != nil {
		t.Fatalf("--line 20 answered %+v, want only adds, passing", cases)
	}
}

func TestLoadSource_RunsAVirtualProgram(t *testing.T) {
	p, err := vmhost.LoadSource("main", `import std/io

fn main() {
  io.print("hi")
}
`)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hi\n" {
		t.Fatalf("output %q", out.String())
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
