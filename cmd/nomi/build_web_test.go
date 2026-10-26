package main

// `nomi build --web`: the folder, its refusals, and nomi.js's run()
// driven under Node. See build_web.go.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/wasmmem"
	"github.com/nomi-language/nomi/vmhost"
	"github.com/nomi-language/nomi/vmrunner"
)

var webPrograms = map[string]string{
	// A program with no main whose function a page calls: it builds, and
	// the page's run() runs nothing.
	"exports.nomi": `export fn double(n: Int) -> Int {
  n * 2
}
`,
	// A program a page loads and calls: main runs first, then the calls.
	"game.nomi": `import std/io
import std/json.{ToJson, FromJson}

struct Game {
  word: String
  guesses: List<String>
}

derive ToJson for Game
derive FromJson for Game

enum Outcome {
  Won
  Playing {left: Int}
}

derive ToJson for Outcome
derive FromJson for Outcome

fn main() {
  io.print("ready")
}

export fn new_game(word: String) -> Game {
  #Game{word, guesses: []}
}

export fn guess(game: Game, letter: String) -> Game {
  io.print("guessed ${letter}")
  #{..game, guesses: [letter, ..game.guesses]}
}

export fn outcome(game: Game) -> Outcome {
  if List.head(game.guesses) == Some("w") { Outcome.Won } else { #Outcome.Playing{left: 6 - Iter.count(game.guesses)} }
}

export fn describe(o: Outcome) -> String {
  case o {
    Outcome.Won -> "won"
    #Outcome.Playing{left} -> "${left} left"
  }
}

export fn divide(a: Int, b: Int) -> Int {
  a / b
}

export fn nothing() {
}
`,
	"failing.nomi": `fn main() -> Result<Unit, String> {
  Err("no setup")
}

export fn one() -> Int {
  1
}
`,
	"hello.nomi": `import std/io

fn main() {
  io.write("What is your name? ")
  case io.read_line() {
    Ok(Some(name)) -> io.print("Hello, ${name}! héllo ✓")
    _ -> io.print("no name")
  }
}
`,
	"startup.nomi": `import std/io

struct App {
  context: Context
}

fn boot(startup: Startup) -> Result<App, String> {
  io.print(startup.args)
  io.print(Map.get(startup.env, "NAME"))
  Ok(#App{context: Context.root()})
}

fn main() {
  io.print("main ran")
}
`,
	"trap.nomi": `import std/io

fn main() {
  zero = String.length("")
  io.print("dividing")
  io.print("${100 / zero}")
}
`,
	"spin.nomi": `fn main() {
  _ = Iter.loop(fn n = 0 -> n + 1)
}
`,
	"oom.nomi": `import std/io

fn main() {
  big = String.repeat("x", 1_500_000_000)
  io.print(String.length(big))
}
`,
	"file.nomi": `import {
  std/fs
  std/io
}

fn main() -> Result<Unit, io.Error> {
  text = try fs.read_text("data.txt")
  try fs.write_text("out/shout.txt", String.to_upper(text))
  io.print(text)
  Ok(Unit)
}
`,
}

// TestBuildWeb builds a --web folder for each of webPrograms with one
// runner cache, checks the folder, the refusals, and, with node on PATH,
// runs the programs through nomi.js.
func TestBuildWeb(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a js/wasm runner; -short")
	}
	env := buildEnv(t.TempDir())
	src := t.TempDir()
	for name, text := range webPrograms {
		mustWrite(t, filepath.Join(src, name), text)
	}
	out := t.TempDir()
	folders := map[string]string{}
	for name := range webPrograms {
		folder := filepath.Join(out, strings.TrimSuffix(name, ".nomi")+"-web")
		args := []string{"build", "--web", filepath.Join(src, name), "-o", folder}
		// A program that reaches std/fs builds, with a note that it sees
		// only the files run's `files` option gives it.
		wantStderr := ""
		if name == "file.nomi" {
			wantStderr = vmhost.WebFilesNote + "\n"
		}
		if b := runProcess(t, env, src, "", nomiBin, args...); b.exit != 0 || b.stderr != wantStderr {
			t.Fatalf("nomi build --web %s: exit %d\n%s", name, b.exit, b.transcript())
		}
		folders[name] = folder
	}

	t.Run("folder", func(t *testing.T) {
		folder := folders["hello.nomi"]
		for _, name := range webFiles {
			if info, err := os.Stat(filepath.Join(folder, name)); err != nil || info.Size() == 0 {
				t.Fatalf("%s: %v", name, err)
			}
		}
		module, err := os.ReadFile(filepath.Join(folder, "app.wasm"))
		if err != nil {
			t.Fatal(err)
		}
		sections, err := wasmSections(module)
		if err != nil {
			t.Fatalf("app.wasm is not a valid module: %v", err)
		}
		if last := sections[len(sections)-1]; last.id != 0 || last.name != vmrunner.WasmSectionName {
			t.Fatalf("the last section is id %d %q, want %q", last.id, last.name, vmrunner.WasmSectionName)
		}
		if image, err := vmrunner.ReadWasmImage(bytes.NewReader(module), int64(len(module))); err != nil || len(image) == 0 {
			t.Fatalf("app.wasm's image: %d bytes, %v", len(image), err)
		}
		if max := memoryMax(t, module, sections); max != wasmmem.MaxBytes/(64<<10) {
			t.Fatalf("app.wasm's memory maximum is %d pages, want %d", max, wasmmem.MaxBytes/(64<<10))
		}
		// A checkout build's runner comes from this checkout's Go, so the
		// wasm_exec.js inside nomi.js is that Go's, byte for byte, and the
		// loader follows it.
		goroot, err := exec.Command("go", "env", "GOROOT").Output()
		if err != nil {
			t.Fatal(err)
		}
		wasmExec, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(goroot)), "lib", "wasm", "wasm_exec.js"))
		if err != nil {
			t.Fatal(err)
		}
		glue, _ := os.ReadFile(filepath.Join(folder, "nomi.js"))
		head := append([]byte(webGlueHead), wasmExec...)
		if !bytes.HasPrefix(glue, head) {
			t.Fatal("nomi.js does not start with installGo wrapping GOROOT's wasm_exec.js")
		}
		if rest := glue[len(head):]; !bytes.Equal(rest, append([]byte(webGlueTail), webLoaderJS...)) {
			t.Fatal("nomi.js's loader differs from the embedded web/nomi.js")
		}
		for _, name := range []string{"wasm_exec.js", "nomi-web.js", "nomi-worker.js"} {
			if _, err := os.Stat(filepath.Join(folder, name)); err == nil {
				t.Fatalf("the build wrote %s", name)
			}
		}
		index, _ := os.ReadFile(filepath.Join(folder, "index.html"))
		if !strings.Contains(string(index), `data-nomi-program="app.wasm"`) || !strings.Contains(string(index), "<title>hello</title>") {
			t.Fatalf("index.html:\n%s", index)
		}
		for _, tag := range regexp.MustCompile(`<script[^>]*>`).FindAllString(string(index), -1) {
			if !strings.Contains(tag, "src=") {
				t.Fatalf("index.html has an inline script: %s", tag)
			}
		}
		// `nomi run --web`'s reload hook is added when it serves the page,
		// never written.
		entries, _ := os.ReadDir(folder)
		if len(entries) != len(webFiles) {
			t.Fatalf("the folder holds %d entries, want the %d of webFiles", len(entries), len(webFiles))
		}
		for _, name := range webFiles {
			if got, _ := os.ReadFile(filepath.Join(folder, name)); bytes.Contains(got, []byte("__nomi")) || bytes.Contains(got, []byte("nomi-dev")) {
				t.Fatalf("%s carries nomi run --web's reload hook", name)
			}
		}
	})

	t.Run("declarations", func(t *testing.T) {
		decls, err := os.ReadFile(filepath.Join(folders["game.nomi"], "nomi.d.ts"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"export interface Game { word: string; guesses: string[] }",
			`export type Outcome = "Won" | { Playing: { left: number } };`,
			`  call(name: "new_game", word: string): Promise<Game>;`,
			`  call(name: "guess", game: Game, letter: string): Promise<Game>;`,
			`  call(name: "divide", a: number, b: number): Promise<number>;`,
			`  call(name: "nothing"): Promise<void>;`,
			"export function load(url: string | URL, options?: NomiLoadOptions): Promise<NomiProgram>;",
		} {
			if !strings.Contains(string(decls), want) {
				t.Errorf("nomi.d.ts lacks %q:\n%s", want, decls)
			}
		}
		// A program with no exported functions still gets the loader's types.
		decls, _ = os.ReadFile(filepath.Join(folders["hello.nomi"], "nomi.d.ts"))
		if !strings.Contains(string(decls), "  call(name: string, ...args: unknown[]): Promise<unknown>;") {
			t.Errorf("hello's nomi.d.ts:\n%s", decls)
		}
	})

	t.Run("rebuild removes the files earlier builds wrote", func(t *testing.T) {
		folder := t.TempDir()
		goroot, err := exec.Command("go", "env", "GOROOT").Output()
		if err != nil {
			t.Fatal(err)
		}
		wasmExec, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(goroot)), "lib", "wasm", "wasm_exec.js"))
		if err != nil {
			t.Fatal(err)
		}
		// What an earlier build wrote: Go's wasm_exec.js, and the loader and
		// worker as they were (testdata/web/retired).
		mustWrite(t, filepath.Join(folder, "wasm_exec.js"), string(wasmExec))
		for _, name := range []string{"nomi-web.js", "nomi-worker.js"} {
			old, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "nomi", "testdata", "web", "retired", name))
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(folder, name), string(old))
		}
		if b := runProcess(t, env, src, "", nomiBin, "build", "--web", filepath.Join(src, "hello.nomi"), "-o", folder); b.exit != 0 {
			t.Fatalf("exit %d\n%s", b.exit, b.transcript())
		}
		for _, name := range []string{"wasm_exec.js", "nomi-web.js", "nomi-worker.js"} {
			if _, err := os.Stat(filepath.Join(folder, name)); err == nil {
				t.Fatalf("the rebuild left the %s an earlier build wrote", name)
			}
		}
		// A file of one of those names the build did not write stays.
		for _, name := range []string{"wasm_exec.js", "nomi-web.js", "nomi-worker.js"} {
			mustWrite(t, filepath.Join(folder, name), "// mine\n")
		}
		if b := runProcess(t, env, src, "", nomiBin, "build", "--web", filepath.Join(src, "hello.nomi"), "-o", folder); b.exit != 0 {
			t.Fatalf("exit %d\n%s", b.exit, b.transcript())
		}
		for _, name := range []string{"wasm_exec.js", "nomi-web.js", "nomi-worker.js"} {
			if got, _ := os.ReadFile(filepath.Join(folder, name)); string(got) != "// mine\n" {
				t.Fatalf("the rebuild changed someone else's %s: %q", name, got)
			}
		}
	})

	t.Run("rebuild keeps other files", func(t *testing.T) {
		folder := t.TempDir()
		mustWrite(t, filepath.Join(folder, "_headers"), "/*\n  X-Frame-Options: DENY\n")
		mustWrite(t, filepath.Join(folder, "app.wasm"), "stale")
		if b := runProcess(t, env, src, "", nomiBin, "build", "--web", filepath.Join(src, "hello.nomi"), "-o", folder); b.exit != 0 {
			t.Fatalf("exit %d\n%s", b.exit, b.transcript())
		}
		if got, _ := os.ReadFile(filepath.Join(folder, "_headers")); !strings.Contains(string(got), "X-Frame-Options") {
			t.Fatal("the rebuild removed _headers")
		}
		if info, _ := os.Stat(filepath.Join(folder, "app.wasm")); info.Size() < 1<<20 {
			t.Fatal("the rebuild left the stale app.wasm")
		}
	})

	t.Run("static files", func(t *testing.T) {
		dir := t.TempDir()
		entry := filepath.Join(dir, "hello.nomi")
		mustWrite(t, entry, webPrograms["hello.nomi"])
		web := filepath.Join(dir, "web")
		mustWrite(t, filepath.Join(web, "index.html"), "<html><head></head><body>my page</body></html>\n")
		mustWrite(t, filepath.Join(web, "custom.html"), "<p>custom</p>\n")
		// wasm_exec.js is not a generated name, so a static one is an
		// ordinary file.
		mustWrite(t, filepath.Join(web, "wasm_exec.js"), "// mine\n")
		mustWrite(t, filepath.Join(web, "img", "logo.png"), "PNG")
		mustWrite(t, filepath.Join(web, ".env"), "SECRET=1")
		mustWrite(t, filepath.Join(web, ".cache", "x"), "x")
		folder := filepath.Join(t.TempDir(), "site")
		mustWrite(t, filepath.Join(folder, "_headers"), "/*\n  X-Frame-Options: DENY\n")
		mustWrite(t, filepath.Join(folder, "old.html"), "from an earlier build")
		if b := runProcess(t, env, dir, "", nomiBin, "build", "--web", entry, "-o", folder); b.exit != 0 || b.stderr != "" {
			t.Fatalf("exit %d\n%s", b.exit, b.transcript())
		}
		for name, want := range map[string]string{
			"index.html":   "<html><head></head><body>my page</body></html>\n",
			"custom.html":  "<p>custom</p>\n",
			"wasm_exec.js": "// mine\n",
			"img/logo.png": "PNG",
			"_headers":     "/*\n  X-Frame-Options: DENY\n",
			"old.html":     "from an earlier build",
		} {
			if got, err := os.ReadFile(filepath.Join(folder, filepath.FromSlash(name))); err != nil || string(got) != want {
				t.Fatalf("%s: %q, %v; want %q", name, got, err, want)
			}
		}
		for _, name := range []string{".env", ".cache"} {
			if _, err := os.Stat(filepath.Join(folder, name)); err == nil {
				t.Fatalf("the build copied %s", name)
			}
		}
		for _, name := range webGenerated {
			if info, err := os.Stat(filepath.Join(folder, name)); err != nil || info.Size() == 0 {
				t.Fatalf("%s: %v", name, err)
			}
		}

		// --static copies another folder in place of web/.
		other := filepath.Join(dir, "other")
		mustWrite(t, filepath.Join(other, "about.html"), "about")
		folder2 := filepath.Join(t.TempDir(), "site")
		if b := runProcess(t, env, dir, "", nomiBin, "build", "--web", "--static", "other", entry, "-o", folder2); b.exit != 0 || b.stderr != "" {
			t.Fatalf("--static: exit %d\n%s", b.exit, b.transcript())
		}
		if got, _ := os.ReadFile(filepath.Join(folder2, "about.html")); string(got) != "about" {
			t.Fatalf("about.html: %q", got)
		}
		if got, _ := os.ReadFile(filepath.Join(folder2, "index.html")); !strings.Contains(string(got), `data-nomi-program="app.wasm"`) {
			t.Fatalf("with --static and no index.html, the page is not the generated one:\n%s", got)
		}
		if _, err := os.Stat(filepath.Join(folder2, "custom.html")); err == nil {
			t.Fatal("--static still copied web/")
		}
	})

	t.Run("static refusals", func(t *testing.T) {
		dir := t.TempDir()
		entry := filepath.Join(dir, "hello.nomi")
		mustWrite(t, entry, webPrograms["hello.nomi"])
		mustWrite(t, filepath.Join(dir, "web", "nomi.js"), "mine")
		mustWrite(t, filepath.Join(dir, "plain", "index.html"), "plain")
		mustWrite(t, filepath.Join(dir, "afile"), "")
		for _, c := range []struct {
			args []string
			want string
		}{
			{[]string{"build", "--web", "hello.nomi", "-o", "w"}, "nomi build --web: web/nomi.js: the build writes nomi.js, so a static file cannot have that name"},
			{[]string{"build", "--web", "--static", "missing", "hello.nomi"}, "nomi build --web: --static missing: no such folder"},
			{[]string{"build", "--web", "--static", "afile", "hello.nomi"}, "nomi build --web: --static afile is a file; it takes a folder"},
			{[]string{"build", "--web", "--static", "plain", "hello.nomi", "-o", "plain/out"}, "nomi build --web: the output folder plain/out is inside the static folder plain; name another with -o"},
			{[]string{"build", "--static", "plain", "hello.nomi"}, "nomi build: --static needs --web"},
		} {
			got := runProcess(t, env, dir, "", nomiBin, c.args...)
			if got.exit == 0 || !strings.Contains(got.stderr, c.want) {
				t.Fatalf("nomi %v: exit %d, want %q:\n%s", c.args, got.exit, c.want, got.transcript())
			}
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 4 {
			t.Fatalf("a refused build wrote into %s: %d entries", dir, len(entries))
		}
	})

	t.Run("default folder", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "hello.nomi"), webPrograms["hello.nomi"])
		if b := runProcess(t, env, dir, "", nomiBin, "build", "--web", "hello.nomi"); b.exit != 0 {
			t.Fatalf("exit %d\n%s", b.exit, b.transcript())
		}
		if _, err := os.Stat(filepath.Join(dir, "hello-web", "index.html")); err != nil {
			t.Fatalf("no hello-web/index.html: %v", err)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		hello := filepath.Join(src, "hello.nomi")
		file := filepath.Join(t.TempDir(), "taken")
		mustWrite(t, file, "")
		for _, c := range []struct {
			args []string
			want string
		}{
			{[]string{"build", "--web", "--target", "linux/amd64", hello}, "--web builds for js/wasm, so it cannot take --target linux/amd64"},
			{[]string{"build", "--web", hello, "-o", file}, "--web writes a folder, and " + file + " is a file"},
		} {
			got := runProcess(t, env, src, "", nomiBin, c.args...)
			if got.exit == 0 || !strings.Contains(got.stderr, c.want) {
				t.Fatalf("nomi %v: exit %d, want %q:\n%s", c.args, got.exit, c.want, got.transcript())
			}
		}
		if b := runProcess(t, env, src, "", nomiBin, "build", "--web", "--target", "js/wasm", hello, "-o", filepath.Join(t.TempDir(), "w")); b.exit != 0 {
			t.Fatalf("--web --target js/wasm: exit %d\n%s", b.exit, b.transcript())
		}
	})

	t.Run("node", func(t *testing.T) {
		node, err := exec.LookPath("node")
		if err != nil {
			t.Skip("no node on PATH")
		}
		if major := nodeMajor(t, node); major < 22 {
			t.Skipf("node %d; the harness needs 22 or newer", major)
		}
		type webRun struct {
			Folder       string            `json:"folder"`
			ContentType  string            `json:"contentType,omitempty"`
			Stdin        *string           `json:"stdin,omitempty"`
			StdinLines   []string          `json:"stdinLines,omitempty"`
			Args         []string          `json:"args,omitempty"`
			Env          map[string]string `json:"env,omitempty"`
			TimeoutMs    int               `json:"timeoutMs,omitempty"`
			AbortAfterMs int               `json:"abortAfterMs,omitempty"`
			Worker       *bool             `json:"worker,omitempty"`
			Files        map[string]string `json:"files,omitempty"`
			Setup        string            `json:"setup,omitempty"`
		}
		type webResult struct {
			Stdout, Stderr, Fatal string
			ExitCode              int
			Events                []string
			Fetches               int
			Files                 map[string]string
		}
		str := func(s string) *string { return &s }
		no := false
		runs := []webRun{
			0:  {Folder: folders["hello.nomi"], Stdin: str("Ada\n")},
			1:  {Folder: folders["hello.nomi"], StdinLines: []string{"Bo"}},
			2:  {Folder: folders["hello.nomi"]},
			3:  {Folder: folders["hello.nomi"], Stdin: str("Cy\n"), Worker: &no, ContentType: "application/octet-stream"},
			4:  {Folder: folders["startup.nomi"], Args: []string{"first", "two words"}, Env: map[string]string{"NAME": "x"}},
			5:  {Folder: folders["trap.nomi"]},
			6:  {Folder: folders["spin.nomi"], TimeoutMs: 300},
			7:  {Folder: folders["spin.nomi"], AbortAfterMs: 300},
			8:  {Folder: folders["file.nomi"], Files: map[string]string{"data.txt": "seeded\n", "out/keep.txt": "kept"}},
			9:  {Folder: folders["oom.nomi"]},
			10: {Folder: folders["exports.nomi"]},
			11: {Folder: folders["file.nomi"]},
			12: {Folder: folders["hello.nomi"], Stdin: str("Di\n"), Setup: "./setup-ok.mjs"},
			13: {Folder: folders["hello.nomi"], Setup: "./setup-fail.mjs"},
			14: {Folder: folders["hello.nomi"], Setup: "./setup-fail.mjs", Worker: &no},
		}
		// A setup module beside nomi.js, which a run imports where the
		// program runs and awaits before it starts.
		mustWrite(t, filepath.Join(folders["hello.nomi"], "setup-ok.mjs"),
			"export default async () => {\n  await new Promise((resolve) => setTimeout(resolve, 5));\n};\n")
		mustWrite(t, filepath.Join(folders["hello.nomi"], "setup-fail.mjs"),
			"export default async () => {\n  throw new Error(\"no database\");\n};\n")
		spec, _ := json.Marshal(map[string]any{"runs": runs})
		harness := filepath.Join(repoRoot(t), "cmd", "nomi", "testdata", "web", "harness.mjs")
		got := runNode(t, env, src, "", node, harness, string(spec))
		if got.exit != 0 {
			t.Fatalf("the harness: exit %d\n%s", got.exit, got.transcript())
		}
		var results []webResult
		if err := json.Unmarshal([]byte(got.stdout), &results); err != nil || len(results) != len(runs) {
			t.Fatalf("the harness printed %q (%v)", got.stdout, err)
		}
		// Each run with input from stdin or none matches `nomi run` of the
		// same source.
		same := func(i int, entry, stdin string, args ...string) {
			t.Helper()
			runEnv := env
			if runs[i].Env != nil {
				runEnv = append(append([]string(nil), env...), "NAME="+runs[i].Env["NAME"])
			}
			want := runProcess(t, runEnv, src, stdin, nomiBin, append([]string{"run", filepath.Join(src, entry)}, args...)...)
			r := results[i]
			if r.Stdout != want.stdout || r.Stderr != want.stderr || r.ExitCode != want.exit {
				t.Fatalf("run %d (%s) under nomi.js differs from nomi run\n--- nomi run (exit %d) ---\n%s\n--- web (exit %d, fatal %q) ---\nstdout: %q\nstderr: %q",
					i, entry, want.exit, want.transcript(), r.ExitCode, r.Fatal, r.Stdout, r.Stderr)
			}
		}
		same(0, "hello.nomi", "Ada\n")
		same(1, "hello.nomi", "Bo\n")
		same(2, "hello.nomi", "")
		same(3, "hello.nomi", "Cy\n")
		same(4, "startup.nomi", "", "first", "two words")
		same(5, "trap.nomi", "")
		if r := results[10]; r.ExitCode != 0 || r.Stdout != "" || r.Stderr != "" || r.Fatal != "" {
			t.Fatalf("a program with no main ran under nomi.js as %+v, want nothing", r)
		}
		if results[0].Stdout != "What is your name? Hello, Ada! héllo ✓\n" || results[5].ExitCode != 1 ||
			results[4].Stdout != "[first, two words]\nSome(x)\nmain ran\n" {
			t.Fatalf("the programs ran as %+v; this test compares the wrong thing", results)
		}
		// A prompt written without a newline reaches the page before the
		// program asks for the line.
		if ev := results[1].Events; len(ev) < 2 || ev[0] != "stdout:What is your name? " || ev[1] != "read" {
			t.Fatalf("events %q, want the prompt and then the read", ev)
		}
		// The module is fetched and compiled once for every run of it.
		if results[3].Fetches != 1 {
			t.Fatalf("app.wasm was fetched %d times over four runs", results[3].Fetches)
		}
		if r := results[6]; r.ExitCode != 124 || !strings.Contains(r.Fatal, "ran for 300 ms and was stopped") {
			t.Fatalf("timeout: %+v", r)
		}
		if r := results[7]; r.ExitCode != 130 || r.Fatal != "the run was aborted" {
			t.Fatalf("abort: %+v", r)
		}
		// A page's files are the program's: it reads one run's `files`
		// gave, and run answers every file when it ends.
		if r := results[8]; r.ExitCode != 0 || r.Stdout != "seeded\n\n" ||
			!maps.Equal(r.Files, map[string]string{"data.txt": "seeded\n", "out/keep.txt": "kept", "out/shout.txt": "SEEDED\n"}) {
			t.Fatalf("files: %+v, want the seeded file read and the written one returned", r)
		}
		// A run waits for its setup module, and one whose setup fails never
		// starts, in a worker or on the page.
		same(12, "hello.nomi", "Di\n")
		for _, i := range []int{13, 14} {
			if r := results[i]; r.ExitCode != 2 || r.Stdout != "" || !strings.HasPrefix(r.Fatal, "the setup module ") ||
				!strings.HasSuffix(r.Fatal, "setup-fail.mjs failed: no database") {
				t.Fatalf("run %d with a failing setup: %+v", i, r)
			}
		}
		// Without them the file is not there.
		if r := results[11]; r.ExitCode != 1 || r.Stderr != "error: data.txt: not found\n" || len(r.Files) != 0 {
			t.Fatalf("no files: %+v, want NotFound", r)
		}
		// An allocation past the 1 GiB memory cap stops Go, and run reports
		// Go's reason.
		if r := results[9]; r.ExitCode != 2 || !strings.Contains(r.Fatal, "fatal error: out of memory") {
			t.Fatalf("out of memory: exit %d, fatal %q", r.ExitCode, r.Fatal)
		}
		// load() starts a program once and serves calls to its exported
		// functions; values cross as JSON.
		game := map[string]any{"word": "pipeline", "guesses": []string{"e"}}
		loads, _ := json.Marshal(map[string]any{"loads": []any{
			map[string]any{"folder": folders["game.nomi"], "closeThenCall": true, "calls": []any{
				[]any{"new_game", "pipeline"},
				[]any{"guess", map[string]any{"word": "pipeline", "guesses": []string{}}, "e"},
				[]any{"outcome", game},
				[]any{"describe", map[string]any{"Playing": map[string]any{"left": 3}}},
				[]any{"describe", "Won"},
				[]any{"divide", 1, 0},
				[]any{"divide", 7, 2},
				[]any{"describe", "Lost"},
				[]any{"nope"},
				[]any{"nothing"},
			}},
			map[string]any{"folder": folders["exports.nomi"], "calls": []any{[]any{"double", 21}}},
			map[string]any{"folder": folders["failing.nomi"], "calls": []any{}},
		}})
		harness = filepath.Join(repoRoot(t), "cmd", "nomi", "testdata", "web", "load-harness.mjs")
		got = runNode(t, env, src, "", node, harness, string(loads))
		if got.exit != 0 {
			t.Fatalf("the load harness: exit %d\n%s", got.exit, got.transcript())
		}
		type callResult struct {
			Value     any    `json:"value"`
			Error     string `json:"error"`
			Undefined bool   `json:"undefined"`
		}
		type loadResult struct {
			Stdout, Stderr, Error, AfterClose string
			Results                           []callResult
			Exports                           []struct {
				Name, Result, Signature string
				Params                  []struct{ Name, Type string }
			}
		}
		var loaded []loadResult
		if err := json.Unmarshal([]byte(got.stdout), &loaded); err != nil || len(loaded) != 3 {
			t.Fatalf("the load harness printed %q (%v)", got.stdout, err)
		}
		g := loaded[0]
		if g.Error != "" || g.Stdout != "ready\nguessed e\n" || g.Stderr != "" {
			t.Fatalf("load(game): error %q, stdout %q, stderr %q", g.Error, g.Stdout, g.Stderr)
		}
		value := func(i int) string {
			t.Helper()
			if g.Results[i].Error != "" {
				t.Fatalf("call %d rejected: %s", i, g.Results[i].Error)
			}
			b, _ := json.Marshal(g.Results[i].Value)
			return string(b)
		}
		for i, want := range map[int]string{
			0: `{"guesses":[],"word":"pipeline"}`,
			1: `{"guesses":["e"],"word":"pipeline"}`,
			2: `{"Playing":{"left":5}}`,
			3: `"3 left"`,
			4: `"won"`,
			6: `3`,
		} {
			if got := value(i); got != want {
				t.Errorf("call %d resolved %s, want %s", i, got, want)
			}
		}
		for i, want := range map[int]string{
			5: "line 45: division by zero",
			7: "argument `o` of `describe(o: Outcome) -> String`: expected Outcome, got \"Lost\"",
			8: "no exported function `nope`; the exported functions are describe, divide, guess, new_game, nothing, outcome",
		} {
			if got := g.Results[i].Error; got != want {
				t.Errorf("call %d rejected with %q, want %q", i, got, want)
			}
		}
		if !g.Results[9].Undefined {
			t.Errorf("a Unit result resolved %+v, want undefined", g.Results[9])
		}
		if g.AfterClose != "the program was closed" {
			t.Errorf("a call after close: %q", g.AfterClose)
		}
		var sigs []string
		for _, x := range g.Exports {
			sigs = append(sigs, x.Signature)
		}
		if strings.Join(sigs, "; ") != "new_game(word: String) -> Game; guess(game: Game, letter: String) -> Game; outcome(game: Game) -> Outcome; "+
			"describe(o: Outcome) -> String; divide(a: Int, b: Int) -> Int; nothing()" ||
			g.Exports[1].Params[1].Name != "letter" || g.Exports[1].Params[1].Type != "String" || g.Exports[5].Result != "" {
			t.Errorf("exports() resolved %+v", g.Exports)
		}
		if r := loaded[1]; r.Error != "" || len(r.Results) != 1 || r.Results[0].Value != float64(42) {
			t.Errorf("load(exports) with no main: %+v", r)
		}
		if r := loaded[2]; r.Error != "error: no setup" {
			t.Errorf("load of a program whose main fails: %+v, want it to reject with main's error", r)
		}

		// Node validates the module the browser would compile.
		check := runNode(t, env, src, "", node, "-e",
			`const fs=require("fs");process.exit(WebAssembly.validate(fs.readFileSync(process.argv[1]))?0:3)`,
			filepath.Join(folders["hello.nomi"], "app.wasm"))
		if check.exit != 0 {
			t.Fatalf("WebAssembly.validate rejects app.wasm: exit %d\n%s", check.exit, check.transcript())
		}
	})
}

// memoryMax answers the maximum, in pages, of the module's one memory.
func memoryMax(t *testing.T, module []byte, sections []wasmSection) uint64 {
	t.Helper()
	for _, s := range sections {
		if s.id != 5 {
			continue
		}
		body := module[s.start:s.end]
		count, n := binary.Uvarint(body)
		if count != 1 {
			t.Fatalf("%d memories", count)
		}
		body = body[n:]
		if body[0] != 1 {
			t.Fatal("the memory has no maximum")
		}
		_, n = binary.Uvarint(body[1:])
		max, _ := binary.Uvarint(body[1+n:])
		return max
	}
	t.Fatal("no memory section")
	return 0
}

// TestWebStaticDir checks where a browser build looks for static files: the
// web/ folder beside a lone file, the project root's for any entry in a
// project with a nomi.toml, and --static's folder over either.
func TestWebStaticDir(t *testing.T) {
	dir := t.TempDir()
	lone := filepath.Join(dir, "lone", "app.nomi")
	mustWrite(t, lone, "fn main() {\n}\n")
	project := filepath.Join(dir, "project")
	mustWrite(t, filepath.Join(project, "nomi.toml"), "")
	nested := filepath.Join(project, "cmd", "tool.nomi")
	mustWrite(t, nested, "fn main() {\n}\n")
	for _, c := range []struct{ entry, flag, want string }{
		{lone, "", filepath.Join(dir, "lone", "web")},
		{nested, "", filepath.Join(project, "web")},
		{nested, filepath.Join(project, "cmd"), filepath.Join(project, "cmd")},
	} {
		if got, err := webStaticDir("build", c.entry, c.flag); err != nil || got != c.want {
			t.Errorf("webStaticDir(%s, %q) = %s, %v; want %s", c.entry, c.flag, got, err, c.want)
		}
	}
}

// TestListStatic lists a static folder's files, subfolders included, and
// leaves out dotfiles and dot folders.
func TestListStatic(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"index.html", "img/logo.png", "img/.DS_Store", ".env", ".git/HEAD", "a/b/c.txt"} {
		mustWrite(t, filepath.Join(dir, filepath.FromSlash(name)), name)
	}
	if got, want := listStatic(dir), []string{"a/b/c.txt", "img/logo.png", "index.html"}; !slices.Equal(got, want) {
		t.Fatalf("listStatic: %q, want %q", got, want)
	}
	if got := listStatic(filepath.Join(dir, "missing")); len(got) != 0 {
		t.Fatalf("a missing folder lists %q", got)
	}
}

// Every generated name but index.html is refused as a static file.
func TestReadStatic_RefusesGeneratedNames(t *testing.T) {
	for _, name := range []string{"app.wasm", "nomi.js", "nomi.d.ts"} {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, name), "mine")
		if _, err := readStatic(dir); err == nil || !strings.Contains(err.Error(), "the build writes "+name+", so a static file cannot have that name") {
			t.Errorf("a static %s: %v", name, err)
		}
	}
}
