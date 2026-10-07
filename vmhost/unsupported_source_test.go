package vmhost_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A body that uses break or continue, called directly from a callback, is a
// gap the docs name, so it declines stably for these tests.
const declinedSource = `import std/io

fn skip_odd(n: Int): Int {
  if n % 2 == 1 {
    continue
  }
  n
}

fn evens(xs: List<Int>): List<Int> {
  Iter.map(xs, |x| skip_odd(x)) |> Iter.to_list()
}

fn main() {
  io.print("before")
  io.print(evens([1, 2]))
}

test "evens" {
  assert evens([2]) == [2]
}
`

// blockedReport runs p and answers its blocked report and Error() text.
func blockedReport(t *testing.T, p *vmhost.Program) (string, string) {
	t.Helper()
	var out bytes.Buffer
	err := p.Run(context.Background(), &out, nil, false)
	blocked, ok := vmhost.IsBlocked(err)
	if !ok {
		t.Fatalf("want a *Blocked, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("a blocked program printed %q", out.String())
	}
	var report bytes.Buffer
	blocked.Write(&report, "main.nomi")
	return report.String(), blocked.Error()
}

// An in-memory program (the tour's, LoadSource's) reports a declined body as
// a program on disk does: the same diagnostic at the same expression, with
// its line quoted, from the run, from Unsupported and from a blocked test
// case. Only the path differs.
func TestLoadSource_ADeclineReadsAsOnDisk(t *testing.T) {
	path := writeProgram(t, declinedSource)
	onDisk, err := vmhost.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	inMemory, err := vmhost.LoadSource("main", declinedSource)
	if err != nil {
		t.Fatal(err)
	}
	diskReport, diskErr := blockedReport(t, onDisk)
	memReport, memErr := blockedReport(t, inMemory)
	if strings.ReplaceAll(diskReport, path, "main.nomi") != memReport {
		t.Errorf("in-memory report:\n%s\nwant the on-disk one:\n%s", memReport, diskReport)
	}
	if !strings.Contains(memReport, " --> main.nomi:11:20\n") || !strings.Contains(memReport, "11 |   Iter.map(xs, |x| skip_odd(x))") {
		t.Errorf("the report does not locate and quote the call:\n%s", memReport)
	}
	if strings.ReplaceAll(diskErr, path, "main.nomi") != memErr {
		t.Errorf("in-memory Error():\n%s\nwant:\n%s", memErr, diskErr)
	}
	if u := inMemory.Unsupported(); u == nil || u.Error() != memErr {
		t.Errorf("Unsupported() = %v; want the run's diagnostic %q", u, memErr)
	}
	var out bytes.Buffer
	cases := inMemory.Cases(&out, vmhost.TestOptions{})
	if len(cases) != 1 || len(cases[0].Blocked) != 1 || cases[0].Blocked[0] != memErr {
		t.Errorf("cases %+v; want one blocked with %q", cases, memErr)
	}
}

// A decline in an in-memory sibling file is located in that file and quotes
// its line from the virtual source, as the entry's does.
func TestLoadSource_ADeclineInAVirtualSiblingQuotesIt(t *testing.T) {
	entry := `import {
  std/io
  lib
}

fn main() {
  io.print(lib.evens([1, 2]))
}
`
	lib := `fn skip_odd(n: Int): Int {
  if n % 2 == 1 {
    continue
  }
  n
}

pub fn evens(xs: List<Int>): List<Int> {
  Iter.map(xs, |x| skip_odd(x)) |> Iter.to_list()
}
`
	p, err := vmhost.LoadSource("main", entry, vmhost.WithVirtualFiles(map[string]string{"lib": lib}))
	if err != nil {
		t.Fatal(err)
	}
	report, _ := blockedReport(t, p)
	want := "error: this call to `skip_odd` is not supported yet, so `fn evens` cannot run\n" +
		" --> lib.nomi:9:20\n  |\n9 |   Iter.map(xs, |x| skip_odd(x)) |> Iter.to_list()\n  |                    ^^^^^^^^\n"
	if !strings.HasPrefix(report, want) {
		t.Errorf("report:\n%s\nwant it to start:\n%s", report, want)
	}
}
