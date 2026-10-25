package main

// `nomi run --web <entry.nomi>`: the dev loop for the browser target, as
// `trunk serve` and `flutter run -d chrome` are. It builds the folder `nomi
// build --web` writes into a temporary directory and serves it on
// http://localhost:<port>/, then watches the program's source files (the
// entry, every file it loads and nomi.toml) by polling their modification
// times (watch.go), and rebuilds when one changes.
//
// The author's static files (build_web.go's webStaticDir: the program's web/
// folder, or the one --static names) are served from where they are, beside
// the generated files and in front of them: a static index.html is the page.
// app.wasm, nomi.js and nomi.d.ts are always the build's. The static folder is polled
// too, and a change there reloads the page without a rebuild.
//
// Every .html page served gets one more script, /__nomi/dev.js
// (web/nomi-dev.js), which listens to /__nomi/events: a server-sent event
// stream of the server's state, {"build": n, "static": m, "error": "..."}.
// n counts the builds that succeeded and m the changes to the static files;
// the page reloads when either differs from its own, and shows the error
// over the page. A failed build keeps the last good one served. Nothing of
// this is written into the folder, so `nomi build --web`'s output never
// carries it.
//
// Ctrl-C stops the server and removes the temporary directory.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/vmhost"
)

//go:embed web/nomi-dev.js
var webDevJS []byte

const runUsage = "usage: nomi run <file> [args]\n       nomi run --watch <file> [args]\n       nomi run --web [--port N] [--open] [--static <dir>] <file>"

const (
	// webFirstPort is where `nomi run --web` starts looking for a free port.
	webFirstPort = 8000
	webLastPort  = 8099
)

type runWebArgs struct {
	entry string
	// port is the port to serve on; -1 is the first free one from
	// webFirstPort, and 0 any free one the system picks.
	port int
	open bool
	// static is --static: the folder of static files to serve in place of
	// the program's web/ folder.
	static string
}

// parseRunWebArgs reads the arguments of a `nomi run` whose first word is a
// flag: the browser dev loop's flags and one program file, in any order.
func parseRunWebArgs(args []string) (runWebArgs, error) {
	r := runWebArgs{port: -1}
	web := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--web":
			web = true
		case arg == "--open":
			r.open = true
		case arg == "--watch":
			return r, errWatchWithWeb
		case arg == "--port" || strings.HasPrefix(arg, "--port="):
			value, inline := strings.CutPrefix(arg, "--port=")
			if !inline {
				if i+1 >= len(args) {
					return r, fmt.Errorf("nomi run: --port needs a value\n%s", runUsage)
				}
				i++
				value = args[i]
			}
			port, err := strconv.Atoi(value)
			if err != nil || port < 0 || port > 65535 {
				return r, fmt.Errorf("nomi run: --port takes a port number; got %q", value)
			}
			r.port = port
		case arg == "--static" || strings.HasPrefix(arg, "--static="):
			value, inline := strings.CutPrefix(arg, "--static=")
			if !inline {
				if i+1 >= len(args) {
					return r, fmt.Errorf("nomi run: --static needs a value\n%s", runUsage)
				}
				i++
				value = args[i]
			}
			if value == "" {
				return r, fmt.Errorf("nomi run: --static takes a folder\n%s", runUsage)
			}
			r.static = value
		case isFlag(arg):
			return r, unknownFlag("nomi run", arg, runUsage)
		case r.entry == "":
			r.entry = arg
		default:
			return r, fmt.Errorf("nomi run --web: unexpected argument %q; a program in the browser takes no arguments\n%s", arg, runUsage)
		}
	}
	if !web {
		return r, fmt.Errorf("nomi run: --port, --open and --static need --web\n%s", runUsage)
	}
	if r.entry == "" {
		return r, errors.New(runUsage)
	}
	return r, nil
}

// runWeb is `nomi run --web`. It returns when ctx is done or the server
// fails, and writes the URL and each build's outcome to stdout and build
// errors to stderr.
func runWeb(ctx context.Context, r runWebArgs, stdout, stderr io.Writer) error {
	absPath, err := programEntry("run", r.entry)
	if err != nil {
		return err
	}
	static, err := webStaticDir("run", absPath, r.static)
	if err != nil {
		return err
	}
	if _, err := readStatic(static); err != nil {
		return fmt.Errorf("nomi run --web: %w", err)
	}
	if res, err := ffirun.Prepare(absPath); hasGoBindings(res, err) {
		return errors.New("nomi run --web: " + webGoBindings)
	}
	root, err := os.MkdirTemp("", "nomi-web-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	listeners, port, err := listenLocal(r.port)
	if err != nil {
		return fmt.Errorf("nomi run --web: %w", err)
	}
	dev := &webDev{
		entry:  absPath,
		title:  strings.TrimSuffix(filepath.Base(absPath), ".nomi"),
		root:   root,
		static: static,
		subs:   map[chan webState]struct{}{},
		stdout: stdout,
		stderr: stderr,
	}
	name := vmhost.DisplayPath(absPath)
	// The stamps are read before the first build, so an edit made while it
	// runs, or as soon as the URL is printed, rebuilds.
	w := dev.watcher()
	fmt.Fprintf(stdout, "building %s\n", name)
	dev.rebuild()
	srv := &http.Server{Handler: dev, ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, len(listeners))
	for _, l := range listeners {
		go func() { served <- srv.Serve(l) }()
	}
	url := fmt.Sprintf("http://localhost:%d/", port)
	fmt.Fprintf(stdout, "serving %s at %s (Ctrl-C stops)\n", name, url)
	if r.open {
		if err := openBrowser(url); err != nil {
			fmt.Fprintf(stderr, "nomi run --web: opening a browser: %v\n", err)
		}
	}
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		w.run(ctx)
	}()
	select {
	case <-ctx.Done():
		err = nil
	case err = <-served:
		err = fmt.Errorf("nomi run --web: %w", err)
	}
	_ = srv.Close()
	<-watchDone
	return err
}

// webState is what /__nomi/events sends: the number of the build being
// served (0 before the first succeeds), how many times the static files
// have changed, and the last build's error and the static files', empty
// when there is none.
type webState struct {
	Build  int    `json:"build"`
	Static int    `json:"static"`
	Error  string `json:"error"`
}

// webDev is one `nomi run --web` session: the folder it serves and the pages
// listening for its next build.
type webDev struct {
	entry, title, root string
	// static is the folder of static files, which may not exist.
	static         string
	stdout, stderr io.Writer

	mu    sync.Mutex
	dir   string // the folder of the build being served, "" before one succeeds
	state webState
	seq   int // folders built so far, for their names
	subs  map[chan webState]struct{}
	// buildErr and staticErr are the last build's error and the static
	// files' (one with a generated name), which state.Error joins.
	buildErr, staticErr string
}

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// rebuild builds the folder into a fresh directory and serves it, or keeps
// serving the last good one and reports why the build failed. The pages
// listening hear of it at the next announce.
func (d *webDev) rebuild() {
	d.mu.Lock()
	d.seq++
	out := filepath.Join(d.root, "build-"+strconv.Itoa(d.seq))
	d.mu.Unlock()
	start := time.Now()
	var report bytes.Buffer
	err := build(buildArgs{entry: d.entry, out: out, target: ffirun.Target{GOOS: "js", GOARCH: "wasm"}, web: true, noStatic: true}, &report)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err == nil {
		old := d.dir
		d.dir = out
		d.state.Build++
		d.buildErr = ""
		if old != "" {
			_ = os.RemoveAll(old)
		}
		fmt.Fprintf(d.stdout, "built in %.1fs\n", time.Since(start).Seconds())
	} else {
		_ = os.RemoveAll(out)
		text := report.String()
		if !errors.Is(err, errBuildRefused) {
			text += err.Error() + "\n"
		}
		fmt.Fprint(d.stderr, text)
		if d.dir != "" {
			fmt.Fprintln(d.stdout, "the build failed; serving the last good build")
		}
		d.buildErr = strings.TrimRight(ansiEscape.ReplaceAllString(text, ""), "\n")
	}
	d.setError()
}

// staticChanged counts a change to the static files, which the next
// announce tells the pages to reload for, and reports a static file with a
// generated name, which is not served.
func (d *webDev) staticChanged() {
	_, err := readStatic(d.static)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state.Static++
	d.staticErr = ""
	if err != nil {
		d.staticErr = "nomi run --web: " + err.Error()
		fmt.Fprintln(d.stderr, d.staticErr)
	}
	d.setError()
}

// setError joins the build's error and the static files' into the state's.
// d.mu is held.
func (d *webDev) setError() {
	errs := []string{}
	for _, e := range []string{d.buildErr, d.staticErr} {
		if e != "" {
			errs = append(errs, e)
		}
	}
	d.state.Error = strings.Join(errs, "\n\n")
}

// announce sends the state to every page listening. The watcher calls it
// once it has read the files' stamps again after a rebuild or a static
// change, so a page or a script that edits a file as soon as it hears of
// one has its edit seen, a file the program has just started importing
// included.
func (d *webDev) announce() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for ch := range d.subs {
		// A listener behind keeps only the newest state.
		select {
		case <-ch:
		default:
		}
		ch <- d.state
	}
}

// staticFiles is the static files whose change reloads the page, by path.
func (d *webDev) staticFiles() []string {
	files := listStatic(d.static)
	for i, f := range files {
		files[i] = filepath.Join(d.static, filepath.FromSlash(f))
	}
	return files
}

// watcher watches the static files and the program's source files
// (watch.go): a static change reloads the pages and a source change
// rebuilds.
func (d *webDev) watcher() *watcher {
	sources := programFiles(d.entry)
	return newWatcher(
		watchSet{
			files: d.staticFiles,
			changed: func(names string) {
				fmt.Fprintf(d.stdout, "%s changed; reloading\n", names)
				d.staticChanged()
			},
			then: d.announce,
		},
		watchSet{
			files: func() []string { return sources },
			changed: func(names string) {
				fmt.Fprintf(d.stdout, "%s changed; rebuilding\n", names)
				d.rebuild()
				// The program may load other files now.
				sources = programFiles(d.entry)
			},
			then: d.announce,
		},
	)
}

// webPlaceholder is the page served before any build has succeeded: the dev
// script alone, which shows the build's error and reloads when one succeeds.
const webPlaceholder = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="color-scheme" content="light dark">
    <title>%s</title>
  </head>
  <body></body>
</html>
`

func (d *webDev) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	d.mu.Lock()
	dir, state := d.dir, d.state
	d.mu.Unlock()
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	switch {
	case name == "__nomi/events":
		d.serveEvents(w, r)
		return
	case name == "__nomi/dev.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(webDevJS)
		return
	case webHidden(name) || strings.HasPrefix(name, "__nomi/"):
		http.NotFound(w, r)
		return
	case webLocaleFile(name):
		if dir == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		http.ServeFile(w, r, filepath.Join(dir, filepath.FromSlash(name)))
		return
	case slices.Contains(webGenerated, name):
		if dir == "" {
			http.NotFound(w, r)
			return
		}
		switch filepath.Ext(name) {
		case ".wasm":
			w.Header().Set("Content-Type", "application/wasm")
		case ".js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case ".ts":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		http.ServeFile(w, r, filepath.Join(dir, name))
		return
	}
	// A static file, or a static folder's index.html, comes before the
	// generated page.
	file := filepath.Join(d.static, filepath.FromSlash(name))
	if info, err := os.Stat(file); err == nil && info.IsDir() {
		name += "/index.html"
		file = filepath.Join(file, "index.html")
	}
	if info, err := os.Stat(file); err == nil && info.Mode().IsRegular() {
		if filepath.Ext(name) == ".html" {
			page, err := os.ReadFile(file)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(withDevHook(page, state))
			return
		}
		f, err := os.Open(file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()
		http.ServeContent(w, r, name, info.ModTime(), f)
		return
	}
	if name != "index.html" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if dir == "" {
		_, _ = w.Write(withDevHook(fmt.Appendf(nil, webPlaceholder, html.EscapeString(d.title)), state))
		return
	}
	index, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(withDevHook(index, state))
}

// withDevHook answers page with the dev script added, before its </head>
// or, in a page without one, at its end. The script's URL carries the
// state the page was served at.
func withDevHook(page []byte, state webState) []byte {
	hook := fmt.Sprintf(`<script type="module" src="/__nomi/dev.js?build=%d&amp;static=%d"></script>`, state.Build, state.Static)
	at := bytes.Index(bytes.ToLower(page), []byte("</head>"))
	if at < 0 {
		return append(append(slices.Clip(page), '\n'), hook+"\n"...)
	}
	out := make([]byte, 0, len(page)+len(hook)+3)
	out = append(out, page[:at]...)
	out = append(out, "  "+hook+"\n  "...)
	return append(out, page[at:]...)
}

// serveEvents streams the session's state: the current one at once, then
// each build's.
func (d *webDev) serveEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	ch := make(chan webState, 1)
	d.mu.Lock()
	d.subs[ch] = struct{}{}
	ch <- d.state
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.subs, ch)
		d.mu.Unlock()
	}()
	for {
		select {
		case <-r.Context().Done():
			return
		case s := <-ch:
			data, _ := json.Marshal(s)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// listenLocal listens on the loopback addresses localhost resolves to,
// 127.0.0.1 and, where the system has it, ::1, on one port: port, any free
// one when port is 0, or the first free one from webFirstPort when it is
// -1. A browser may try either address for localhost, so a port another
// server holds on one of them is not free.
func listenLocal(port int) ([]net.Listener, int, error) {
	try := func(p int) ([]net.Listener, int, error) {
		v4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err != nil {
			return nil, 0, err
		}
		p = v4.Addr().(*net.TCPAddr).Port
		v6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(p)))
		switch {
		case err == nil:
			return []net.Listener{v4, v6}, p, nil
		case errors.Is(err, syscall.EADDRINUSE):
			_ = v4.Close()
			return nil, 0, err
		}
		// No IPv6 loopback: localhost is 127.0.0.1 alone.
		return []net.Listener{v4}, p, nil
	}
	switch port {
	case -1:
		for p := webFirstPort; p <= webLastPort; p++ {
			if ls, got, err := try(p); err == nil {
				return ls, got, nil
			}
		}
		return nil, 0, fmt.Errorf("no free port from %d to %d; name one with --port", webFirstPort, webLastPort)
	case 0:
		var err error
		for range 10 {
			var ls []net.Listener
			var got int
			if ls, got, err = try(0); err == nil {
				return ls, got, nil
			}
		}
		return nil, 0, err
	}
	ls, got, err := try(port)
	if errors.Is(err, syscall.EADDRINUSE) {
		return nil, 0, fmt.Errorf("port %d is in use", port)
	}
	return ls, got, err
}

// openBrowser opens url in the default browser and does not wait for it.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// runWebCommand is `nomi run` with a flag first: the browser dev loop until
// Ctrl-C.
func runWebCommand(args []string) error {
	r, err := parseRunWebArgs(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWeb(ctx, r, os.Stdout, os.Stderr)
}
