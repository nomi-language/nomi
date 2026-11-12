package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// embedProgram embeds a folder of text and a binary file, and prints what
// it holds: the files' paths, a text file, and the binary file's bytes.
const embedProgram = `import
  std::embed.Files
  std::io
end

embed site ` + "`static logo.bin`" + `

fn main()
  io.print(Files.paths(site))
  io.print(Files.read_text(site, "static/index.html")!)
  io.print(Files.read(site, "logo.bin")! |> Bytes.to_list() |> Iter.map(Byte.to_int) |> Iter.to_list())
  io.print(Files.metadata(site, "static/css/site.css")!.size)
end
`

const embedProgramOutput = "[logo.bin, static/css/site.css, static/index.html]\n<h1>hi</h1>\n\n[0, 137, 80, 255]\n8\n"

// writeEmbedProject writes embedProgram and the files it embeds under a
// new directory, and answers the entry's path.
func writeEmbedProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"site\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(dir, "main.nomi"), embedProgram)
	mustWrite(t, filepath.Join(dir, "static", "index.html"), "<h1>hi</h1>\n")
	mustWrite(t, filepath.Join(dir, "static", "css", "site.css"), "body {}\n")
	mustWrite(t, filepath.Join(dir, "logo.bin"), "\x00\x89P\xff")
	return filepath.Join(dir, "main.nomi")
}

// A built executable carries the files its program embeds: built, then run
// with its sources and the embedded files deleted and from another
// directory, it prints what `nomi run` printed. So does a compiled
// `.nomic`, run by `nomi run`.
func TestBuildCommand_EmbeddedFilesTravelInTheBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a runner; -short")
	}
	env := buildEnv(t.TempDir())
	entry := writeEmbedProject(t)
	dir := filepath.Dir(entry)
	want := runProcess(t, env, dir, "", nomiBin, "run", entry)
	if want.exit != 0 || want.stdout != embedProgramOutput {
		t.Fatalf("nomi run: exit %d\n%s", want.exit, want.transcript())
	}
	out := t.TempDir()
	bin := exe(filepath.Join(out, "site"))
	if b := runProcess(t, env, dir, "", nomiBin, "build", entry, "-o", bin); b.exit != 0 {
		t.Fatalf("nomi build: exit %d\n%s", b.exit, b.transcript())
	}
	compiled := filepath.Join(out, "site.nomic")
	if b := runProcess(t, env, dir, "", nomiBin, "build", "--no-vm", entry, "-o", compiled); b.exit != 0 {
		t.Fatalf("nomi build --no-vm: exit %d\n%s", b.exit, b.transcript())
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if got := runProcess(t, env, elsewhere, "", bin); got != want {
		t.Fatalf("the built binary, its sources deleted, differs from nomi run\n--- nomi run ---\n%s\n--- binary (exit %d) ---\n%s",
			want.transcript(), got.exit, got.transcript())
	}
	if got := runProcess(t, env, elsewhere, "", nomiBin, "run", compiled); got != want {
		t.Fatalf("the .nomic, its sources deleted, differs from nomi run\n--- nomi run ---\n%s\n--- .nomic (exit %d) ---\n%s",
			want.transcript(), got.exit, got.transcript())
	}
}

// The WebAssembly targets carry the files too: a js/wasm module and a
// wasip1 one, run under Node with their sources deleted, print what `nomi
// run` printed.
func TestBuildCommand_EmbeddedFilesTravelInWasmModules(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds two runners; -short")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on PATH to run the modules")
	}
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("go env GOROOT: %v", err)
	}
	env := buildEnv(t.TempDir())
	entry := writeEmbedProject(t)
	dir := filepath.Dir(entry)
	out := t.TempDir()
	jsMod := filepath.Join(out, "site-js.wasm")
	wasiMod := filepath.Join(out, "site-wasi.wasm")
	for target, mod := range map[string]string{"js/wasm": jsMod, "wasip1/wasm": wasiMod} {
		if b := runProcess(t, env, dir, "", nomiBin, "build", "--target", target, entry, "-o", mod); b.exit != 0 {
			t.Fatalf("nomi build --target %s: exit %d\n%s", target, b.exit, b.transcript())
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	got := runNode(t, env, elsewhere, "", node, filepath.Join(strings.TrimSpace(string(goroot)), "lib", "wasm", "wasm_exec_node.js"), jsMod)
	if got.exit != 0 || got.stdout != embedProgramOutput {
		t.Fatalf("the js/wasm module: exit %d\n%s", got.exit, got.transcript())
	}
	if nodeMajor(t, node) < 20 {
		t.Log("node has no node:wasi preview1; not running the wasip1 module")
		return
	}
	script := filepath.Join(t.TempDir(), "wasi.mjs")
	mustWrite(t, script, wasiNodeScript)
	got = runNode(t, env, elsewhere, "", node, "--no-warnings", script, wasiMod)
	if got.exit != 0 || got.stdout != embedProgramOutput {
		t.Fatalf("the wasip1 module: exit %d\n%s", got.exit, got.transcript())
	}
}

// A file in a subdirectory embeds a folder at the module root: its
// patterns name files from the root, as import paths do, so web/pages.nomi's
// `assets` is the root's assets, never web/assets. The kept program tracks
// what the patterns can match: an edit to an embedded file is read on the
// next run, and an edit outside the patterns' folders keeps the program.
func TestRunCommand_ANestedEmbedReadsFromTheModuleRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi; -short")
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"site\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(dir, "main.nomi"), `import
  std::embed.Files
  std::io
  web::pages
end

fn main()
  io.print(Files.paths(pages.site))
  io.print(Files.read_text(pages.site, "assets/x.css")!)
end
`)
	mustWrite(t, filepath.Join(dir, "web", "pages.nomi"), "pub embed site `assets`\n")
	mustWrite(t, filepath.Join(dir, "assets", "x.css"), "body {}\n")
	mustWrite(t, filepath.Join(dir, "web", "assets", "y.css"), "p {}\n")
	mustWrite(t, filepath.Join(dir, "notes.txt"), "one\n")
	cache := newCacheDir(t)
	want := "[assets/x.css]\nbody {}\n\n"
	if got := runNomiIn(t, dir, cache, "run", "main.nomi"); got.exit != 0 || got.stdout != want {
		t.Fatalf("nomi run:\n%s\nwant stdout %q", got, want)
	}
	// A file younger than the cache's racy window is not kept yet; the
	// second run, its files a moment older, stores the program.
	time.Sleep(2100 * time.Millisecond)
	runNomiIn(t, dir, cache, "run", "main.nomi")
	mustWrite(t, filepath.Join(dir, "notes.txt"), "two\n")
	if got := runNomiIn(t, dir, cache, "run", "main.nomi"); !got.read() || got.stdout != want {
		t.Fatalf("after an edit outside the embedded folder, nomi run read %v and printed\n%s", got.report, got)
	}
	mustWrite(t, filepath.Join(dir, "assets", "x.css"), "body { margin: 0 }\n")
	want = "[assets/x.css]\nbody { margin: 0 }\n\n"
	if got := runNomiIn(t, dir, cache, "run", "main.nomi"); got.exit != 0 || got.stdout != want {
		t.Fatalf("after an edit to the embedded file, nomi run:\n%s\nwant stdout %q", got, want)
	}
}
