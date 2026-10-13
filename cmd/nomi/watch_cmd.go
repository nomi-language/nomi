package main

// `nomi run --watch <file> [args]` and `nomi test --watch [path]`: run the
// program or the tests, and again whenever a file they are built from
// changes (watch.go). A run still going when a file changes is stopped
// through its context (vmcmd.Run, vmcmd.Tester.Stop), not by exiting, and
// what it writes after that is dropped, so the next run's output follows the
// status line alone. A build error is printed as the command prints it, and
// the watch waits for the next change. Ctrl-C stops the run and exits with
// the status a signal gives (130 for Ctrl-C).
//
// Standard input is read by one goroutine for the whole watch (watchInput).
// Each run reads it through its own reader, which answers end of input once
// the run is stopped, so a read pending when a file changes ends and a line
// typed while the next run starts is that run's.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/nomi-language/nomi/internal/vmcmd"
)

// watchSeparator is the status line between two runs.
func watchSeparator(w io.Writer, names string) {
	fmt.Fprintf(w, "%s changed; running again\n", names)
}

// parseRunWatchArgs reads a `nomi run` whose first word is a flag, when its
// flags before the file include --watch: the file, and every word after it
// as the program's arguments. watch is false for any other flag set, which
// is the browser dev loop's (run_web.go).
func parseRunWatchArgs(args []string) (watch bool, entry string, progArgs []string, err error) {
	i := 0
	web := false
	var unknown string
	for ; i < len(args) && isFlag(args[i]); i++ {
		arg := args[i]
		switch {
		case arg == "--watch":
			watch = true
		case arg == "--web" || arg == "--open" || arg == "--port" || arg == "--static" ||
			strings.HasPrefix(arg, "--port=") || strings.HasPrefix(arg, "--static="):
			web = true
		case unknown == "":
			unknown = arg
		}
	}
	if !watch {
		return false, "", nil, nil
	}
	if web {
		return false, "", nil, errWatchWithWeb
	}
	if unknown != "" {
		return false, "", nil, unknownFlag("nomi run", unknown, runUsage)
	}
	if i >= len(args) {
		return false, "", nil, errors.New(runUsage)
	}
	return true, args[i], args[i+1:], nil
}

var errWatchWithWeb = errors.New("nomi run: --watch and --web cannot be combined; --web rebuilds and reloads on every change already")

// runWatch is `nomi run --watch`. It returns when ctx is done, or at once
// with an error when path cannot be watched.
func runWatch(ctx context.Context, path string, args []string, stdout, stderr io.Writer, stdin io.Reader) error {
	if isImageFilePath(path) {
		return fmt.Errorf("nomi run --watch: %s is a compiled program, with no source to watch; watch the .nomi file it was built from", path)
	}
	absPath, err := runEntry(path)
	if err != nil {
		return err
	}
	files := programFiles(absPath)
	watchRuns(ctx, stdout, stderr, stdin,
		func() []string { return files },
		func() { files = programFiles(absPath) },
		func(ctx context.Context, stdout, stderr io.Writer, stdin io.Reader) {
			vmcmd.Run(ctx, absPath, stdout, stderr, stdin, args, false)
		})
	return nil
}

// testWatch is `nomi test --watch`: the tests under parsed.root, run again
// when a .nomi file under it or a file its tests load changes. It returns
// when ctx is done.
func testWatch(ctx context.Context, parsed testArgs, stdout, stderr io.Writer, stdin io.Reader) error {
	if _, err := os.Stat(parsed.root); err != nil {
		return fmt.Errorf("nomi test: %w", pathError(parsed.root, err))
	}
	loaded := testedFiles(parsed.root)
	watchRuns(ctx, stdout, stderr, stdin,
		func() []string { return append(nomiFilesUnder(parsed.root), loaded...) },
		func() { loaded = testedFiles(parsed.root) },
		func(ctx context.Context, stdout, stderr io.Writer, stdin io.Reader) {
			err := testVM(ctx, parsed.root, parsed.opts, parsed.format, stdout, stderr, stdin)
			if err != nil && !errors.Is(err, errTestsFailed) {
				fmt.Fprintln(stderr, err)
			}
		})
	return nil
}

// testedFiles is every file the tests under root load, and the nomi.toml
// of each one's project.
func testedFiles(root string) []string {
	tests, _ := discoverTestFiles(root)
	var files []string
	for _, f := range tests {
		files = append(files, programFiles(f)...)
	}
	return files
}

// nomiFilesUnder is root when it is a file, and otherwise every .nomi file
// under it, skipping the folders `nomi test` skips.
func nomiFilesUnder(root string) []string {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		abs, _ := filepath.Abs(root)
		return []string{abs}
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", ".astro":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".nomi") {
			if abs, err := filepath.Abs(path); err == nil {
				files = append(files, abs)
			}
		}
		return nil
	})
	return files
}

// watchRuns runs run, and whenever one of files() changes stops it if it is
// still running, prints the status line and runs it again, until ctx is
// done. refresh, called as each run starts, updates what files answers.
func watchRuns(ctx context.Context, stdout, stderr io.Writer, stdin io.Reader,
	files func() []string, refresh func(),
	run func(ctx context.Context, stdout, stderr io.Writer, stdin io.Reader)) {
	in := &watchInput{src: stdin}
	start := func() (stop func()) {
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			run(runCtx, &gatedWriter{ctx: runCtx, w: stdout}, &gatedWriter{ctx: runCtx, w: stderr}, in.reader(runCtx))
		}()
		return func() {
			cancel()
			<-done
		}
	}
	stop := start()
	watchFiles(ctx, watchSet{
		files: files,
		changed: func(names string) {
			stop()
			watchSeparator(stderr, names)
			stop = start()
			refresh()
		},
	})
	stop()
}

// gatedWriter writes to w until ctx ends and drops every write after, so a
// stopped run's last words do not follow the next run's status line.
type gatedWriter struct {
	ctx context.Context
	mu  sync.Mutex
	w   io.Writer
}

func (g *gatedWriter) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ctx.Err() != nil {
		return len(p), nil
	}
	return g.w.Write(p)
}

// watchInput is the standard input the runs of one watch share. One
// goroutine, started by the first read, reads src; each run reads through
// reader. Bytes no run has taken stay for the next run.
type watchInput struct {
	src    io.Reader
	once   sync.Once
	chunks chan []byte // closed at the end of src

	mu   sync.Mutex
	rest []byte // read from src and not yet given to a run
	eof  bool
}

func (in *watchInput) pump() {
	in.chunks = make(chan []byte)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := in.src.Read(buf)
			if n > 0 {
				in.chunks <- bytes.Clone(buf[:n])
			}
			if err != nil {
				close(in.chunks)
				return
			}
		}
	}()
}

// reader is one run's view of the input: it answers end of input once ctx
// ends, a pending read included.
func (in *watchInput) reader(ctx context.Context) io.Reader {
	return &watchReader{in: in, ctx: ctx}
}

type watchReader struct {
	in  *watchInput
	ctx context.Context
}

// Read answers at most one line, so a run's buffered reader never holds a
// line it has not read when the run is stopped.
func (r *watchReader) Read(p []byte) (int, error) {
	in := r.in
	if in.src == nil {
		return 0, io.EOF
	}
	in.once.Do(in.pump)
	in.mu.Lock()
	defer in.mu.Unlock()
	for len(in.rest) == 0 && !in.eof {
		in.mu.Unlock()
		var chunk []byte
		ok := true
		select {
		case <-r.ctx.Done():
			in.mu.Lock()
			return 0, io.EOF
		case chunk, ok = <-in.chunks:
		}
		in.mu.Lock()
		if !ok {
			in.eof = true
		}
		in.rest = append(in.rest, chunk...)
	}
	// A chunk that arrived as the run was stopped stays for the next run.
	if len(in.rest) == 0 || r.ctx.Err() != nil {
		return 0, io.EOF
	}
	n := len(in.rest)
	if i := bytes.IndexByte(in.rest, '\n'); i >= 0 {
		n = i + 1
	}
	n = copy(p, in.rest[:n])
	in.rest = in.rest[n:]
	return n, nil
}

// watchCommand runs body until Ctrl-C or SIGTERM and answers the exit status:
// 128 plus the signal's number, or 1 when body fails at once. A second
// signal exits at once.
func watchCommand(body func(ctx context.Context) error) int {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	code := make(chan int, 1)
	go func() {
		s := <-signals
		c := 130
		if sig, ok := s.(syscall.Signal); ok {
			c = 128 + int(sig)
		}
		code <- c
		cancel()
		<-signals
		os.Exit(c)
	}()
	if err := body(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return <-code
}

// runWatchCommand is `nomi run --watch` on the process's streams.
func runWatchCommand(entry string, args []string) int {
	return watchCommand(func(ctx context.Context) error {
		return runWatch(ctx, entry, args, os.Stdout, os.Stderr, os.Stdin)
	})
}

// testWatchCommand is `nomi test --watch` on the process's streams.
func testWatchCommand(parsed testArgs) int {
	return watchCommand(func(ctx context.Context) error {
		return testWatch(ctx, parsed, os.Stdout, os.Stderr, os.Stdin)
	})
}
