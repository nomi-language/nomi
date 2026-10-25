package main

// `nomi run --web`: the server, its rebuilds and their events, and its
// refusals. See run_web.go.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmhost"
)

func TestParseRunWebArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		want runWebArgs
		err  string
	}{
		{args: []string{"--web", "a.nomi"}, want: runWebArgs{entry: "a.nomi", port: -1}},
		{args: []string{"--web", "--port", "9000", "--open", "a.nomi"}, want: runWebArgs{entry: "a.nomi", port: 9000, open: true}},
		{args: []string{"--web", "a.nomi", "--port=0"}, want: runWebArgs{entry: "a.nomi", port: 0}},
		{args: []string{"--web", "--static", "site", "a.nomi"}, want: runWebArgs{entry: "a.nomi", port: -1, static: "site"}},
		{args: []string{"--web", "a.nomi", "--static=site"}, want: runWebArgs{entry: "a.nomi", port: -1, static: "site"}},
		{args: []string{"--web", "a.nomi", "--static"}, err: "--static needs a value"},
		{args: []string{"--web", "a.nomi", "--static="}, err: "--static takes a folder"},
		{args: []string{"--static", "site", "a.nomi"}, err: "nomi run: --port, --open and --static need --web"},
		{args: []string{"--port", "9000", "a.nomi"}, err: "nomi run: --port, --open and --static need --web"},
		{args: []string{"--web", "--port", "x", "a.nomi"}, err: `--port takes a port number; got "x"`},
		{args: []string{"--web", "--port"}, err: "--port needs a value"},
		{args: []string{"--web", "a.nomi", "b"}, err: `unexpected argument "b"; a program in the browser takes no arguments`},
		{args: []string{"--web"}, err: "usage: nomi run"},
		{args: []string{"--verbose", "a.nomi"}, err: `nomi run: unknown flag "--verbose"`},
	} {
		got, err := parseRunWebArgs(c.args)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: error %v, want %q", c.args, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q: %+v, %v; want %+v", c.args, got, err, c.want)
		}
	}
}

// webServer is one `nomi run --web` process under test.
type webServer struct {
	t      *testing.T
	cmd    *exec.Cmd
	url    string
	stderr *bytes.Buffer
	events chan webState
	// last is the last event next answered, nil before the first.
	last *webState

	mu     sync.Mutex
	stdout strings.Builder
}

// output answers what the server has printed to stdout so far.
func (s *webServer) output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stdout.String()
}

// startWebServer runs `nomi run --web --port 0 [flags] entry` with env and
// waits for the URL it prints, then subscribes to its events.
func startWebServer(t *testing.T, env []string, entry string, flags ...string) *webServer {
	t.Helper()
	cmd := exec.Command(nomiBin, append(append([]string{"run", "--web", "--port", "0"}, flags...), entry)...)
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	s := &webServer{t: t, cmd: cmd, stderr: &bytes.Buffer{}, events: make(chan webState, 16)}
	cmd.Stderr = s.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	urls := make(chan string, 1)
	go func() {
		serving := regexp.MustCompile(`^serving .* at (http://localhost:\d+/) \(Ctrl-C stops\)$`)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			s.mu.Lock()
			s.stdout.WriteString(scanner.Text() + "\n")
			s.mu.Unlock()
			if m := serving.FindStringSubmatch(scanner.Text()); m != nil {
				urls <- m[1]
			}
		}
		close(urls)
	}()
	select {
	case u, ok := <-urls:
		if !ok {
			t.Fatalf("nomi run --web stopped before serving:\n%s", s.stderr)
		}
		s.url = u
	case <-time.After(5 * time.Minute):
		t.Fatalf("nomi run --web printed no URL:\n%s", s.stderr)
	}
	resp, err := http.Get(s.url + "__nomi/events")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("/__nomi/events: Content-Type %q", got)
	}
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(nil, 1<<20)
		for scanner.Scan() {
			data, ok := strings.CutPrefix(scanner.Text(), "data: ")
			if !ok {
				continue
			}
			var state webState
			if err := json.Unmarshal([]byte(data), &state); err != nil {
				t.Errorf("event %q: %v", data, err)
				return
			}
			s.events <- state
		}
	}()
	return s
}

// next answers the next event the server sends. Its failure says which
// event came last and what the server printed, so a change the server never
// noticed (no "changed" line) reads apart from a slow build.
func (s *webServer) next() webState {
	s.t.Helper()
	select {
	case state := <-s.events:
		s.last = &state
		return state
	case <-time.After(2 * time.Minute):
		after := "subscribing"
		if s.last != nil {
			after = fmt.Sprintf("the event %+v", *s.last)
		}
		s.t.Fatalf("no event from the server in 2 minutes after %s\nstdout:\n%s\nstderr:\n%s", after, s.output(), s.stderr)
		return webState{}
	}
}

// get fetches path and answers its body and content type.
func (s *webServer) get(path string) ([]byte, string) {
	s.t.Helper()
	resp, err := http.Get(s.url + path)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("%s: HTTP %d\n%s", path, resp.StatusCode, body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		s.t.Fatalf("%s: Cache-Control %q", path, got)
	}
	return body, resp.Header.Get("Content-Type")
}

// status answers the HTTP status of a GET of path.
func (s *webServer) status(path string) int {
	s.t.Helper()
	resp, err := http.Get(s.url + path)
	if err != nil {
		s.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// stop sends the server SIGINT, as Ctrl-C does, and waits for it.
func (s *webServer) stop() {
	s.t.Helper()
	if err := s.cmd.Process.Signal(syscall.SIGINT); err != nil {
		s.t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			s.t.Fatalf("nomi run --web after Ctrl-C: %v\n%s", err, s.stderr)
		}
	case <-time.After(30 * time.Second):
		s.t.Fatal("nomi run --web did not stop on Ctrl-C")
	}
}

// TestRunWeb serves a program, rebuilds it on each edit, keeps the last
// good build through a failed one, and stops on Ctrl-C leaving no temporary
// directory.
func TestRunWeb(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a js/wasm runner; -short")
	}
	tmp := t.TempDir()
	env := append(buildEnv(t.TempDir()), "TMPDIR="+tmp)
	src := t.TempDir()
	entry := filepath.Join(src, "hello.nomi")
	program := func(text string) string {
		return "import {\n  std/io\n  words\n}\n\nfn main() {\n  io.print(\"" + text + "\")\n  io.print(words.word())\n}\n"
	}
	helper := filepath.Join(src, "words.nomi")
	word := func(text string) string { return "pub fn word() -> String {\n  \"" + text + "\"\n}\n" }
	mustWrite(t, helper, word("a"))
	mustWrite(t, entry, program("one"))

	s := startWebServer(t, env, entry)
	if state := s.next(); state != (webState{Build: 1}) {
		t.Fatalf("first state %+v, want build 1 and no error", state)
	}
	index, typ := s.get("")
	if typ != "text/html; charset=utf-8" || !bytes.Contains(index, []byte(`data-nomi-program="app.wasm"`)) ||
		!bytes.Contains(index, []byte(`<script type="module" src="/__nomi/dev.js?build=1&amp;static=0"></script>`+"\n  </head>")) {
		t.Fatalf("/ (%s):\n%s", typ, index)
	}
	first, typ := s.get("app.wasm")
	if typ != "application/wasm" || len(first) < 1<<20 {
		t.Fatalf("/app.wasm: %d bytes of %s", len(first), typ)
	}
	if glue, typ := s.get("nomi.js"); typ != "text/javascript; charset=utf-8" ||
		!bytes.HasPrefix(glue, []byte(webGlueHead)) || !bytes.HasSuffix(glue, webLoaderJS) {
		t.Fatalf("/nomi.js: %s, wraps wasm_exec.js: %v, ends with the embedded loader: %v",
			typ, bytes.HasPrefix(glue, []byte(webGlueHead)), bytes.HasSuffix(glue, webLoaderJS))
	}
	if dev, _ := s.get("__nomi/dev.js"); !bytes.Equal(dev, webDevJS) {
		t.Fatal("/__nomi/dev.js is not web/nomi-dev.js")
	}

	// An edit rebuilds, and the page is told to reload.
	mustWrite(t, entry, program("two, longer"))
	if state := s.next(); state != (webState{Build: 2}) {
		t.Fatalf("after an edit: %+v, want build 2", state)
	}
	second, _ := s.get("app.wasm")
	if bytes.Equal(first, second) {
		t.Fatal("the rebuild serves the same app.wasm")
	}
	if index, _ := s.get("index.html"); !bytes.Contains(index, []byte("dev.js?build=2")) {
		t.Fatalf("index.html after the rebuild:\n%s", index)
	}

	// A failed build is reported, and the last good one is still served.
	mustWrite(t, entry, strings.Replace(program("three"), "io.print", "io.prnt", 1))
	state := s.next()
	if state.Build != 2 || !strings.Contains(state.Error, "file 'io' has no member 'prnt'") {
		t.Fatalf("after a broken edit: %+v", state)
	}
	if strings.Contains(state.Error, "\x1b[") {
		t.Fatalf("the event's error carries terminal colour: %q", state.Error)
	}
	if still, _ := s.get("app.wasm"); !bytes.Equal(still, second) {
		t.Fatal("a failed build changed the app.wasm served")
	}
	if !strings.Contains(s.stderr.String(), "has no member 'prnt'") {
		t.Fatalf("the terminal shows no build error:\n%s", s.stderr)
	}

	// Fixing it builds again and clears the error.
	mustWrite(t, entry, program("four, fixed"))
	if state := s.next(); state != (webState{Build: 3}) {
		t.Fatalf("after the fix: %+v, want build 3", state)
	}

	// A file the program imports is watched too.
	mustWrite(t, helper, word("a longer word"))
	if state := s.next(); state != (webState{Build: 4}) {
		t.Fatalf("after editing an imported file: %+v, want build 4", state)
	}

	s.stop()
	if leftovers, _ := filepath.Glob(filepath.Join(tmp, "nomi-web-*")); len(leftovers) != 0 {
		t.Fatalf("Ctrl-C left %v", leftovers)
	}
}

// TestRunWeb_BrokenAtStart serves a page that shows the first build's error
// and moves to build 1 once the program is fixed.
func TestRunWeb_BrokenAtStart(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a js/wasm runner; -short")
	}
	env := buildEnv(t.TempDir())
	entry := filepath.Join(t.TempDir(), "broken.nomi")
	mustWrite(t, entry, "fn main() {\n  undefined_name()\n}\n")
	s := startWebServer(t, env, entry)
	if state := s.next(); state.Build != 0 || !strings.Contains(state.Error, "undefined_name") {
		t.Fatalf("first state %+v, want build 0 and the error", state)
	}
	if index, _ := s.get(""); !bytes.Contains(index, []byte("dev.js?build=0")) || !bytes.Contains(index, []byte("<title>broken</title>")) {
		t.Fatalf("the placeholder page:\n%s", index)
	}
	resp, err := http.Get(s.url + "app.wasm")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/app.wasm before any build: HTTP %d", resp.StatusCode)
	}
	mustWrite(t, entry, "fn main() {\n  _ = 1\n}\n")
	if state := s.next(); state != (webState{Build: 1}) {
		t.Fatalf("after the fix: %+v, want build 1", state)
	}
	s.stop()
}

// TestRunWeb_Refusals checks what `nomi run --web` refuses before it
// serves anything.
func TestRunWeb_Refusals(t *testing.T) {
	env := buildEnv(t.TempDir())
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a_test.nomi"), "test \"x\" {\n}\n")
	mustWrite(t, filepath.Join(dir, "site", "a.nomi"), "fn main() {\n}\n")
	mustWrite(t, filepath.Join(dir, "site", "web", "app.wasm"), "mine")
	project := filepath.Join(repoRoot(t), "cmd", "nomi", "testdata", "go_bindings")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "--web", filepath.Join(project, "main.nomi")}, "nomi run --web: this project has Go bindings, and the browser build cannot link them"},
		{[]string{"run", "--web", filepath.Join(dir, "missing.nomi")}, "nomi run: " + filepath.Join(dir, "missing.nomi") + ": no such file or directory"},
		{[]string{"run", "--web", filepath.Join(dir, "a_test.nomi")}, "is a test file; use `nomi test"},
		{[]string{"run", "--open", filepath.Join(dir, "a.nomi")}, "--port, --open and --static need --web"},
		{[]string{"run", "--web", "--static", filepath.Join(dir, "nowhere"), filepath.Join(dir, "site", "a.nomi")},
			"nomi run --web: --static " + filepath.Join(dir, "nowhere") + ": no such folder"},
		{[]string{"run", "--web", "site/a.nomi"},
			"nomi run --web: site/web/app.wasm: the build writes app.wasm, so a static file cannot have that name"},
	} {
		got := runProcess(t, env, dir, "", nomiBin, c.args...)
		if got.exit != 1 || !strings.Contains(got.stderr, c.want) {
			t.Fatalf("nomi %v: exit %d, want %q:\n%s", c.args, got.exit, c.want, got.transcript())
		}
	}
	// Without --web, a flag after the file is still the program's.
	mustWrite(t, filepath.Join(dir, "args.nomi"), "import std/io\n\nstruct App {\n  context: Context\n}\n\nfn boot(startup: Startup) -> App {\n  io.print(startup.args)\n  #App{context: Context.root()}\n}\n\nfn main() {\n}\n")
	if got := runProcess(t, env, dir, "", nomiBin, "run", "args.nomi", "--web"); got.exit != 0 || got.stdout != "[--web]\n" {
		t.Fatalf("nomi run args.nomi --web: exit %d\n%s", got.exit, got.transcript())
	}
	if _, err := os.Stat(filepath.Join(dir, "args-web")); err == nil {
		t.Fatal("nomi run wrote a folder")
	}
}

// devHook is the script `nomi run --web` adds to a page served at a state.
func devHook(build, static int) string {
	return fmt.Sprintf(`<script type="module" src="/__nomi/dev.js?build=%d&amp;static=%d"></script>`, build, static)
}

// waitOutput waits for the server to print text on stdout.
func (s *webServer) waitOutput(text string) {
	s.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if strings.Contains(s.output(), text) {
			return
		}
	}
	s.t.Fatalf("the server never printed %q:\n%s", text, s.output())
}

// TestRunWeb_Static serves the web/ folder beside the program: its pages,
// each with the reload hook and in front of the generated page, its
// subfolders, and none of its dotfiles. A change there reloads the pages
// without a rebuild, and --static serves another folder in its place.
func TestRunWeb_Static(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a js/wasm runner; -short")
	}
	env := buildEnv(t.TempDir())
	src := t.TempDir()
	entry := filepath.Join(src, "page.nomi")
	program := func(text string) string {
		return "import std/io\n\nfn main() {\n  io.print(\"" + text + "\")\n}\n"
	}
	mustWrite(t, entry, program("one"))
	web := filepath.Join(src, "web")
	mustWrite(t, filepath.Join(web, "index.html"), "<!doctype html>\n<html>\n<HEAD>\n<title>mine</title>\n</HEAD>\n<body>my page</body>\n</html>\n")
	mustWrite(t, filepath.Join(web, "custom.html"), "<!doctype html>\n<p>custom one</p>\n")
	mustWrite(t, filepath.Join(web, "img", "logo.png"), "PNG")
	mustWrite(t, filepath.Join(web, ".secret"), "secret")
	mustWrite(t, filepath.Join(web, ".git", "config"), "config")

	s := startWebServer(t, env, entry)
	if state := s.next(); state != (webState{Build: 1}) {
		t.Fatalf("first state %+v, want build 1", state)
	}
	// The static index.html is the page; the hook goes before its </HEAD>.
	if index, typ := s.get(""); typ != "text/html; charset=utf-8" ||
		!strings.Contains(string(index), "<title>mine</title>\n  "+devHook(1, 0)+"\n  </HEAD>") ||
		strings.Contains(string(index), "data-nomi-program") {
		t.Fatalf("/ (%s):\n%s", typ, index)
	}
	// A page without a </head> gets the hook at its end.
	if custom, _ := s.get("custom.html"); string(custom) != "<!doctype html>\n<p>custom one</p>\n\n"+devHook(1, 0)+"\n" {
		t.Fatalf("/custom.html:\n%s", custom)
	}
	if logo, typ := s.get("img/logo.png"); string(logo) != "PNG" || typ != "image/png" {
		t.Fatalf("/img/logo.png: %q (%s)", logo, typ)
	}
	for _, p := range []string{".secret", ".git/config", "img/../.secret", "missing.html", "img/"} {
		if code := s.status(p); code != http.StatusNotFound {
			t.Fatalf("/%s: HTTP %d, want 404", p, code)
		}
	}
	wasm, typ := s.get("app.wasm")
	if typ != "application/wasm" || len(wasm) < 1<<20 {
		t.Fatalf("/app.wasm: %d bytes of %s", len(wasm), typ)
	}

	// Editing a static page reloads without a rebuild.
	mustWrite(t, filepath.Join(web, "custom.html"), "<!doctype html>\n<p>custom two, edited</p>\n")
	if state := s.next(); state != (webState{Build: 1, Static: 1}) {
		t.Fatalf("after a static edit: %+v, want build 1, static 1", state)
	}
	if custom, _ := s.get("custom.html"); !strings.Contains(string(custom), "custom two") || !strings.Contains(string(custom), devHook(1, 1)) {
		t.Fatalf("/custom.html after the edit:\n%s", custom)
	}
	s.waitOutput(vmhost.DisplayPath(filepath.Join(web, "custom.html")) + " changed; reloading\n")
	if strings.Contains(s.output(), "rebuilding") {
		t.Fatalf("a static edit rebuilt the program:\n%s", s.output())
	}

	// A static file taking a generated name is reported and not served.
	mustWrite(t, filepath.Join(web, "app.wasm"), "mine")
	if state := s.next(); state.Build != 1 || state.Static != 2 || !strings.Contains(state.Error, "the build writes app.wasm, so a static file cannot have that name") {
		t.Fatalf("after adding web/app.wasm: %+v", state)
	}
	if got, _ := s.get("app.wasm"); !bytes.Equal(got, wasm) {
		t.Fatal("a static app.wasm replaced the generated one")
	}
	if err := os.Remove(filepath.Join(web, "app.wasm")); err != nil {
		t.Fatal(err)
	}
	if state := s.next(); state != (webState{Build: 1, Static: 3}) {
		t.Fatalf("after removing web/app.wasm: %+v, want build 1, static 3, no error", state)
	}

	// A source edit still rebuilds.
	mustWrite(t, entry, program("two, longer"))
	if state := s.next(); state != (webState{Build: 2, Static: 3}) {
		t.Fatalf("after a source edit: %+v, want build 2", state)
	}
	s.stop()

	// --static serves another folder in place of web/.
	other := filepath.Join(src, "other")
	mustWrite(t, filepath.Join(other, "index.html"), "<html><head></head><body>other page</body></html>")
	s = startWebServer(t, env, entry, "--static", other)
	if state := s.next(); state != (webState{Build: 1}) {
		t.Fatalf("with --static: %+v, want build 1", state)
	}
	if index, _ := s.get("index.html"); !strings.Contains(string(index), "other page") || !strings.Contains(string(index), devHook(1, 0)) {
		t.Fatalf("/index.html with --static:\n%s", index)
	}
	if code := s.status("custom.html"); code != http.StatusNotFound {
		t.Fatalf("/custom.html with --static: HTTP %d, want 404", code)
	}
	s.stop()
}
