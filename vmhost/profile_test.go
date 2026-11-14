package vmhost_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// profiledProgram spends most of its time in `hot`, a loop with no call
// in it, some in `inner`, which a lambda run by `Iter.map` from `outer`
// calls, and next to none in `cold`.
const profiledProgram = `import std::io

fn hot(n: Int, acc: Int) -> Int
    if n == 0 then acc else hot(n - 1, (acc + n * 7) % 1000003)
end

fn inner(i: Int) -> Int
    Iter.reduce(0..2000, i, fn acc, k -> (acc + k) % 1000003)
end

fn outer(n: Int) -> Int
    0..n |> Iter.map(fn i -> inner(i)) |> Iter.sum()
end

fn cold() -> Int
    1
end

fn main()
    io.print(hot(4000000, 0) + outer(300) + cold())
end
`

// profileRow is one row of the table's functions: its name and its
// seconds of self and total time.
type profileRow struct {
	name        string
	self, total float64
}

// profileRows reads the function rows of a profile table.
func profileRows(t *testing.T, table string) map[string]profileRow {
	t.Helper()
	rows := map[string]profileRow{}
	inFunctions := false
	for _, line := range strings.Split(table, "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) >= 3 && fields[0] == "self" && fields[1] == "total" && fields[2] == "function":
			inFunctions = true
		case inFunctions && len(fields) == 0:
			return rows
		case inFunctions && len(fields) >= 5:
			self, err1 := strconv.ParseFloat(strings.TrimSuffix(fields[0], "s"), 64)
			total, err2 := strconv.ParseFloat(strings.TrimSuffix(fields[2], "s"), 64)
			if err1 != nil || err2 != nil {
				t.Fatalf("unreadable profile row %q", line)
			}
			if _, seen := rows[fields[4]]; !seen {
				rows[fields[4]] = profileRow{name: fields[4], self: self, total: total}
			}
		}
	}
	return rows
}

// TestProfile_AHotFunctionLeadsTheProfile runs a program under
// NOMI_PROFILE (internal/vm's profile.go) and checks what it reports: the
// function doing the work leads the table, a lambda run from Go is charged
// to the function that called the iteration, the program's output is
// untouched, and the pprof file is a gzipped profile naming its functions.
func TestProfile_AHotFunctionLeadsTheProfile(t *testing.T) {
	dir := t.TempDir()
	pprofFile := filepath.Join(dir, "run.pb.gz")
	t.Setenv("NOMI_PROFILE", pprofFile)

	p, err := vmhost.LoadSource(filepath.Join(dir, "main"), profiledProgram)
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	var out, errOut bytes.Buffer
	p.SetErrorOutput(&errOut)
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, errOut.String())
	}
	if strings.Contains(out.String(), "nomi profile") {
		t.Errorf("the profile went to the program's output:\n%s", out.String())
	}

	table := errOut.String()
	if !strings.Contains(table, "nomi profile:") {
		t.Fatalf("no profile on the error output:\n%s", table)
	}
	rows := profileRows(t, table)
	var first string
	for _, line := range strings.Split(table, "\n") {
		if f := strings.Fields(line); len(f) >= 5 && strings.HasSuffix(f[0], "s") && f[0] != "self" {
			first = f[4]
			break
		}
	}
	if first != "hot" {
		t.Errorf("the table leads with %q, want hot:\n%s", first, table)
	}
	inner, outer := rows["inner"], rows["outer"]
	if inner.total == 0 {
		t.Fatalf("no time for inner, which a lambda calls from Iter.map:\n%s", table)
	}
	if outer.total < inner.total {
		t.Errorf("outer's total %.2fs is under inner's %.2fs: the lambda Iter.map runs is not linked to outer:\n%s",
			outer.total, inner.total, table)
	}
	if c, ok := rows["cold"]; ok && c.self > 0.05 {
		t.Errorf("cold has %.2fs of its own:\n%s", c.self, table)
	}

	f, err := os.Open(pprofFile)
	if err != nil {
		t.Fatalf("no pprof file: %v", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("the pprof file is not gzipped: %v", err)
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("the pprof file does not decompress: %v", err)
	}
	for _, name := range []string{"hot", "inner", "outer", "wall", "nanoseconds"} {
		if !bytes.Contains(data, []byte(name)) {
			t.Errorf("the pprof file's strings lack %q", name)
		}
	}
}

// TestProfile_ARunWithoutNOMI_PROFILEReportsNothing checks that a plain run
// prints no profile.
func TestProfile_ARunWithoutNOMI_PROFILEReportsNothing(t *testing.T) {
	t.Setenv("NOMI_PROFILE", "")
	dir := t.TempDir()
	p, err := vmhost.LoadSource(filepath.Join(dir, "main"), `import std::io

fn main()
    io.print(1)
end
`)
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	var out, errOut bytes.Buffer
	p.SetErrorOutput(&errOut)
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if errOut.Len() != 0 {
		t.Errorf("a plain run wrote to the error output:\n%s", errOut.String())
	}
}
