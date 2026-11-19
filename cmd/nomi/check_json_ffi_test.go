package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ffiCheckRecord is what these tests read of a `nomi check --format json`
// record.
type ffiCheckRecord struct {
	Type        string   `json:"type"`
	File        string   `json:"file"`
	Line        int      `json:"line"`
	Col         int      `json:"col"`
	EndLine     int      `json:"end_line"`
	StartByte   *int     `json:"start_byte"`
	Severity    string   `json:"severity"`
	Code        string   `json:"code"`
	Args        []string `json:"args"`
	Message     string   `json:"message"`
	Hints       []string `json:"hints"`
	Status      string   `json:"status"`
	Diagnostics *int     `json:"diagnostics"`
}

// checkJSONRecords runs `nomi check --format json entry` with env and
// answers its exit status and records, failing t on a line that is not
// one.
func checkJSONRecords(t *testing.T, env []string, entry string) (int, []ffiCheckRecord) {
	t.Helper()
	r := runProcess(t, env, t.TempDir(), "", nomiBin, "check", "--format", "json", entry)
	var recs []ffiCheckRecord
	for _, line := range strings.Split(strings.TrimSpace(r.stdout), "\n") {
		var rec ffiCheckRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %q is not a record: %v\n%s", line, err, r.transcript())
		}
		recs = append(recs, rec)
	}
	if r.stderr != "" {
		t.Errorf("nomi check --format json wrote to stderr:\n%s", r.transcript())
	}
	return r.exit, recs
}

// diagnosticRecords are the diagnostic records of recs.
func diagnosticRecords(recs []ffiCheckRecord) []ffiCheckRecord {
	var out []ffiCheckRecord
	for _, r := range recs {
		if r.Type == "diagnostic" {
			out = append(out, r)
		}
	}
	return out
}

// A program with Go bindings, which its FFI wrapper checks, gives the
// records any other program gives: each diagnostic with its code, args,
// span and hints, a capability list's refusal of a binding among them, its
// file record and the summary. One project serves each case, so its
// wrapper is built once.
func TestCheckJSON_AProgramWithGoBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds an FFI wrapper; -short")
	}
	dir := t.TempDir()
	if goModAbove(dir) {
		t.Skip("a go.mod above the temp directory")
	}
	env := buildEnv(t.TempDir())
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.27.0\n")
	const module = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.self]\nstd = [\"std::io\"]\n"
	const binds = "gopkg \"strings\" as strings\n\nfn upper(s: String) -> String go strings.ToUpper\n"
	mustWrite(t, entry, "import std::io\n\n"+binds+"\nfn main()\n    io.print(upper(\"hi\"))\nend\n")

	t.Run("clean", func(t *testing.T) {
		mustWrite(t, filepath.Join(dir, "nomi.toml"), module+"go = true\n")
		exit, recs := checkJSONRecords(t, env, entry)
		if exit != 0 || len(diagnosticRecords(recs)) != 0 || len(recs) != 2 || recs[0].Status != "ok" ||
			recs[1].Type != "summary" || recs[1].Diagnostics == nil || *recs[1].Diagnostics != 0 {
			t.Errorf("exit %d, records %+v; want the file ok and no diagnostic", exit, recs)
		}
	})

	t.Run("a capability list refuses the binding", func(t *testing.T) {
		mustWrite(t, filepath.Join(dir, "nomi.toml"), module)
		exit, recs := checkJSONRecords(t, env, entry)
		ds := diagnosticRecords(recs)
		if exit != 1 || len(ds) != 1 {
			t.Fatalf("exit %d, records %+v; want one diagnostic", exit, recs)
		}
		d := ds[0]
		if d.Code != "capability-not-allowed" || !slices.Equal(d.Args, []string{"go:strings", "self", "example.com/app"}) ||
			d.File != entry || d.Line != 5 || d.Col != 4 || d.StartByte == nil || d.Severity != "error" ||
			d.Message != "this module's own code binds Go through go:strings, and [capabilities.self] does not let it bind Go" || len(d.Hints) != 2 {
			t.Errorf("the refusal's record is %+v", d)
		}
		if last := recs[len(recs)-1]; last.Diagnostics == nil || *last.Diagnostics != 1 || recs[len(recs)-2].Status != "failed" {
			t.Errorf("records %+v; want the file failed and one diagnostic counted", recs)
		}
	})

	t.Run("a type error", func(t *testing.T) {
		mustWrite(t, filepath.Join(dir, "nomi.toml"), module+"go = true\n")
		mustWrite(t, entry, "import\n    std::io\n    std::fs\nend\n\n"+binds+"\nfn main()\n    x: Int = upper(\"hi\")\n    io.print(\"${x}\")\nend\n")
		exit, recs := checkJSONRecords(t, env, entry)
		ds := diagnosticRecords(recs)
		if exit != 1 || len(ds) != 2 {
			t.Fatalf("exit %d, records %+v; want two diagnostics", exit, recs)
		}
		if ds[0].Code != "unused-import" || ds[0].Line != 3 || ds[0].Col != 10 || ds[0].StartByte == nil {
			t.Errorf("the unused import's record is %+v", ds[0])
		}
		if ds[1].Message != "type mismatch: expected Int, got String" || ds[1].Line != 11 || ds[1].EndLine != 11 || ds[1].StartByte == nil {
			t.Errorf("the type error's record is %+v", ds[1])
		}
		if last := recs[len(recs)-1]; last.Diagnostics == nil || *last.Diagnostics != 2 {
			t.Errorf("the summary is %+v; want two diagnostics counted", last)
		}
	})
}

// Go a program binds that does not compile is no Nomi diagnostic: its
// record is coded go-build-failed, with the Go compiler's report as its
// message, which `nomi check` prints as it is.
func TestCheckJSON_AGoBuildFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds an FFI wrapper; -short")
	}
	dir := t.TempDir()
	if goModAbove(dir) {
		t.Skip("a go.mod above the temp directory")
	}
	env := buildEnv(t.TempDir())
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.27.0\n")
	mustWrite(t, filepath.Join(dir, "shout", "shout.go"), "package shout\n\nfunc Shout(s string) string {\n\treturn s + 1\n}\n")
	mustWrite(t, entry, "import std::io\n\ngopkg \"example.com/app/shout\" as shout\n\nfn loud(s: String) -> String go shout.Shout\n\nfn main()\n    io.print(loud(\"hi\"))\nend\n")

	exit, recs := checkJSONRecords(t, env, entry)
	ds := diagnosticRecords(recs)
	if exit != 1 || len(ds) != 1 {
		t.Fatalf("exit %d, records %+v; want one diagnostic", exit, recs)
	}
	d := ds[0]
	if d.Code != "go-build-failed" || d.File != entry || d.Severity != "error" ||
		!strings.Contains(d.Message, "shout.go:4:9: invalid operation: s + 1") {
		t.Errorf("the build failure's record is %+v", d)
	}
	text := runProcess(t, append(env, "NOMI_DIAGNOSTICS="), t.TempDir(), "", nomiBin, "check", entry)
	if text.exit != 1 || text.stderr != d.Message+"\n" {
		t.Errorf("nomi check: exit %d, stderr %q; want the record's message %q", text.exit, text.stderr, d.Message)
	}
}
