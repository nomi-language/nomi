package vmhost_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

const filesNotes = `import {
    std/fs
    std/io
}

fn save(text: String) -> Result<Unit, io.Error> {
    fs.write_text("notes.txt", text)
}

fn main() -> Result<Unit, io.Error> {
    text = fs.read_text("notes.txt")?
    save(text + "!")?
    io.print("saved")
    Ok(Unit)
}
`

// A build for a target lacking a class the program reaches is refused with
// each call in the program's own code that reaches it, at the call, the
// std function it calls and the crossing that function makes, the
// functions that reach the call, and how to build anyway. No real target
// lacks `files`, so the test builds for one that does.
func TestCheckTarget_NamesTheProgramsOwnCallSites(t *testing.T) {
	defer vmhost.LackingTargetForTest("nomitest", "nofiles", vmhost.ClassFiles)()
	path := filepath.Join(t.TempDir(), "notes.nomi")
	if err := os.WriteFile(path, []byte(filesNotes), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Uses(vmhost.ClassFiles, false) {
		t.Fatal("the program reaches std/fs, and Uses says it does not")
	}
	if refused := p.CheckTarget("nomi build --target js/wasm", "js", "wasm", false, nil); refused != nil {
		t.Fatalf("js/wasm has files, and the build is refused:\n%s", refused.Error())
	}
	if refused := p.CheckTarget("nomi build --target nomitest/nofiles", "nomitest", "nofiles", false, []string{vmhost.ClassFiles}); refused != nil {
		t.Fatalf("--allow-unsupported files still refuses:\n%s", refused.Error())
	}
	refused := p.CheckTarget("nomi build --target nomitest/nofiles", "nomitest", "nofiles", false, nil)
	if refused == nil {
		t.Fatal("a target lacking files does not refuse a program that reads files")
	}
	var report strings.Builder
	refused.Write(&report)
	got := report.String()
	want := "nomi build --target nomitest/nofiles: this program reads or writes files, which a nomitest/nofiles module cannot do:\n" +
		"  notes.nomi:7:18: `fs.write_text` calls `fs_host.write` (reached from fn main through save)\n" +
		"  notes.nomi:11:24: `fs.read_text` calls `fs_host.read_text` (reached from fn main)\n" +
		"hint: build for your platform without --target, or pass --allow-unsupported files\n" +
		"      to build anyway; the calls then answer Err(.Unsupported{...})\n"
	if got != want {
		t.Errorf("report:\n%s\nwant:\n%s", got, want)
	}
	for _, u := range refused.Uses {
		if strings.HasPrefix(u.Site, "std/") {
			t.Errorf("a use is reported inside std: %+v", u)
		}
	}
	if p.Refuse("nomi build", "x/y", nil, false) != nil {
		t.Error("a target that lacks nothing refuses")
	}
}

// A crossing two std functions deep (`fs.with_temp_dir` defers a private
// `remove_quietly`, which removes the directory) is reported at the
// program's call of the std function it entered std through.
func TestCheckTarget_ACrossingDeepInStdIsReportedAtTheProgramsCall(t *testing.T) {
	defer vmhost.LackingTargetForTest("nomitest", "nofiles", vmhost.ClassFiles)()
	src := `import {
    std/fs
    std/io
}

fn scratch() -> Result<Int, io.Error> {
    fs.with_temp_dir(fn _ -> Ok(1))
}

fn main() {
    io.print(scratch())
}
`
	path := filepath.Join(t.TempDir(), "scratch.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	refused := p.CheckTarget("nomi build --target nomitest/nofiles", "nomitest", "nofiles", false, nil)
	if refused == nil {
		t.Fatal("a target lacking files does not refuse fs.with_temp_dir")
	}
	crossings := map[string]bool{}
	for _, u := range refused.Uses {
		if u.Site != "scratch.nomi:7:21" || !strings.HasPrefix(u.Callee, "fs.with_temp_dir") ||
			strings.Join(u.Chain, " ") != "main scratch" {
			t.Errorf("use %+v, want it at scratch.nomi:7:21, the call of fs.with_temp_dir in scratch", u)
		}
		crossings[u.Crossing] = true
	}
	if !crossings["fs_host.make_temp_dir"] || !crossings["fs_host.remove_all"] {
		t.Errorf("the crossings are %v, want make_temp_dir and remove_all", crossings)
	}
}

// A WebAssembly module cannot start a process: a build for js/wasm or
// wasip1 refuses a program that reaches std/process, and builds one with
// --allow-unsupported processes. A native target builds it.
func TestCheckTarget_WebAssemblyRefusesProcesses(t *testing.T) {
	src := `import {
    std/io
    std/process.{self, Command}
}

fn main() -> Result<Unit, io.Error> {
    _ = process.run(Command{program: "make"})?
    Ok(Unit)
}
`
	path := filepath.Join(t.TempDir(), "build.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if refused := p.CheckTarget("nomi build --target linux/amd64", "linux", "amd64", false, nil); refused != nil {
		t.Fatalf("linux/amd64 can start processes, and the build is refused:\n%s", refused.Error())
	}
	for _, target := range [][2]string{{"js", "wasm"}, {"wasip1", "wasm"}} {
		command := "nomi build --target " + target[0] + "/" + target[1]
		if p.CheckTarget(command, target[0], target[1], false, []string{vmhost.ClassProcesses}) != nil {
			t.Errorf("%s: --allow-unsupported processes still refuses", command)
		}
		refused := p.CheckTarget(command, target[0], target[1], false, nil)
		if refused == nil {
			t.Fatalf("%s builds a program that starts a process", command)
		}
		var report strings.Builder
		refused.Write(&report)
		want := command + ": this program starts programs, which a " + target[0] + "/" + target[1] + " module cannot do:\n" +
			"  build.nomi:7:20: `process.run` calls `process_host.start` (reached from fn main)\n" +
			"hint: build for your platform without --target, or pass --allow-unsupported processes\n" +
			"      to build anyway; the calls then answer Err(.Unsupported{...})\n"
		if report.String() != want {
			t.Errorf("report:\n%s\nwant:\n%s", report.String(), want)
		}
	}
}

// A WebAssembly module cannot serve: a build for js/wasm or wasip1 refuses
// a program that listens, while a js/wasm build of a program that only sends
// requests is allowed, since the browser's fetch answers them.
func TestCheckTarget_WebAssemblyRefusesAServerAndKeepsTheClient(t *testing.T) {
	load := func(src string) *vmhost.Program {
		t.Helper()
		path := filepath.Join(t.TempDir(), "build.nomi")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		p, err := vmhost.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	server := load(`import std/http

fn main() -> Result<Unit, http.Error> {
    listener = http.listen()?
    dbg listener.server.address
    Ok(Unit)
}
`)
	client := load(`import {
    std/http
    std/url.Url
}

fn main() -> Result<Unit, http.Error> {
    _ = http.get(Url.parse("https://example.com")!)?
    Ok(Unit)
}
`)
	if refused := client.CheckTarget("nomi build --target js/wasm", "js", "wasm", false, nil); refused != nil {
		t.Fatalf("js/wasm refuses a program that only sends requests:\n%s", refused.Error())
	}
	for _, target := range [][2]string{{"js", "wasm"}, {"wasip1", "wasm"}} {
		command := "nomi build --target " + target[0] + "/" + target[1]
		refused := server.CheckTarget(command, target[0], target[1], false, nil)
		if refused == nil {
			t.Fatalf("%s builds a program that listens", command)
		}
		var report strings.Builder
		refused.Write(&report)
		if !strings.Contains(report.String(), "this program serves network requests") ||
			!strings.Contains(report.String(), "`http.listen` calls `http_host.listen`") {
			t.Errorf("%s: report:\n%s", command, report.String())
		}
	}
	if refused := server.CheckTarget("nomi build --target linux/amd64", "linux", "amd64", false, nil); refused != nil {
		t.Fatalf("linux/amd64 can serve, and the build is refused:\n%s", refused.Error())
	}
}
