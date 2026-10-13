package main

// `nomi build --web <entry.nomi>`: a folder any static file host can serve
// that runs the program's `main` in a browser.
//
//	app.wasm    the js/wasm runner with the program in its nomi.image
//	            custom section, its memory capped at 1 GiB
//	nomi.js     one ES module: Go's JS support file wasm_exec.js, from the
//	            toolchain that built the runner (cached beside it:
//	            ffirun.WasmExec), unchanged inside a function, then
//	            web/nomi.js, the loader with its run and load API, which
//	            also starts itself as the module worker each run executes in
//	            (webGlue)
//	nomi.d.ts   TypeScript declarations for nomi.js, with a typed `call`
//	            for each of the program's exported functions
//	            (vmhost.Program.WebDeclarations)
//	index.html  a page that mounts a terminal on its data-nomi-program
//	            element, with no inline script
//
// The author's static files are copied in beside them, subfolders included:
// the files of the program's web/ folder, or of the folder --static names
// (webStaticDir). A static index.html replaces the generated page; a static
// file with one of webGenerated's names is refused. Files and folders whose
// names start with a dot are left out.
//
// The runner is the one `--target js/wasm` writes, so it carries the tour's
// policy: file access refused (internal/stdio/file_browser.go) and call depth
// capped at 1,000 (rt/calldepth_js.go). A project with Go bindings is refused
// before anything is built.
//
// A build over a folder an earlier build wrote removes the files earlier
// builds generated and this one does not (webRetired), but only those whose
// contents show a build wrote them; any other file of that name is kept.

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/internal/wasmmem"
	"github.com/nomi-language/nomi/vmhost"
	"github.com/nomi-language/nomi/vmrunner"
)

var (
	//go:embed web/nomi.js
	webLoaderJS []byte
	//go:embed web/index.html
	webIndexHTML string
)

// webFiles are the names `--web` generates. With the static files it copies,
// they are what a build replaces. Anything else in the folder is left alone,
// so a host's _headers file can live there, and a static file deleted since
// the last build stays until someone removes it.
var webFiles = []string{"app.wasm", "nomi.js", "nomi.d.ts", "index.html"}

// webGenerated are the generated names a static file may not take. A static
// index.html is allowed, and replaces the generated page.
var webGenerated = []string{"app.wasm", "nomi.js", "nomi.d.ts"}

// webGlueHead and webGlueTail wrap Go's wasm_exec.js in nomi.js. It is a
// classic script that sets globalThis.Go (and globalThis.fs, process and path
// when they are missing) as soon as it runs. As the body of installGo it runs
// unchanged in module scope, and only where a program runs, so a page that
// runs its programs in workers keeps its own globals.
const (
	webGlueHead = "// nomi.js, written by `nomi build --web`: Go's wasm_exec.js from the toolchain\n" +
		"// that built app.wasm, unchanged, then the loader. See the comment after it.\n" +
		"function installGo() {\n"
	webGlueTail = "\n}\n\n"
)

// webGlue is nomi.js: wasmExec as installGo's body, then the loader.
func webGlue(wasmExec []byte) []byte {
	glue := make([]byte, 0, len(webGlueHead)+len(wasmExec)+len(webGlueTail)+len(webLoaderJS))
	glue = append(glue, webGlueHead...)
	glue = append(glue, wasmExec...)
	glue = append(glue, webGlueTail...)
	return append(glue, webLoaderJS...)
}

// webRetired are the names earlier builds generated and this one does not,
// each with the test that a file of that name is one a build wrote: Go's
// wasm_exec.js, whose text depends on the toolchain, by its first line and
// the class it defines; the one nomi-web.js and the one nomi-worker.js any
// build wrote, by their SHA-256 (their text is in testdata/web/retired).
var webRetired = map[string]func([]byte) bool{
	"wasm_exec.js": func(data []byte) bool {
		return bytes.HasPrefix(data, []byte("// Copyright 2018 The Go Authors. All rights reserved.\n")) &&
			bytes.Contains(data, []byte("globalThis.Go = class"))
	},
	"nomi-web.js":    webDigestIs("33724787ba11dedd024fe88b4e238f98dd9389d4edc3bee8d1b3aa1b10e5818a"),
	"nomi-worker.js": webDigestIs("bb9fa0b905f6e0cc1bd4081f30fcf96ff98839b717322cc11707346c6f0050d5"),
}

func webDigestIs(want string) func([]byte) bool {
	return func(data []byte) bool {
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:]) == want
	}
}

// removeRetired removes each of webRetired's files in out that a build
// wrote, unless a static file of that name is about to replace it.
func removeRetired(out string, static webStatic) error {
	for name, wrote := range webRetired {
		if slices.Contains(static.files, name) {
			continue
		}
		path := filepath.Join(out, name)
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if data, err := os.ReadFile(path); err != nil || !wrote(data) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

// webStatic is a browser build's static files: the folder they come from and
// their slash-separated paths inside it.
type webStatic struct {
	dir   string
	files []string
}

// webStaticDir is the folder of static files a browser build of entry
// includes. It is flag when --static names one, which must be a folder.
// Otherwise it is the web/ folder at the root of entry's project (the folder
// holding its nomi.toml) or, outside a project, the web/ folder beside
// entry. That one may be missing, which means no static files.
func webStaticDir(cmd, entry, flag string) (string, error) {
	if flag != "" {
		dir, err := filepath.Abs(flag)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(dir)
		if err != nil {
			return "", fmt.Errorf("nomi %s --web: --static %s: no such folder", cmd, flag)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("nomi %s --web: --static %s is a file; it takes a folder", cmd, flag)
		}
		return dir, nil
	}
	root := analysis.ProjectRoot(entry, "")
	if info, err := os.Stat(filepath.Join(root, "nomi.toml")); err == nil && !info.IsDir() {
		return filepath.Join(root, "web"), nil
	}
	return filepath.Join(filepath.Dir(entry), "web"), nil
}

// listStatic answers the files under dir as sorted slash-separated paths
// relative to it, leaving out every file and folder whose name starts with a
// dot. A missing or unreadable dir has none.
func listStatic(dir string) []string {
	var files []string
	_ = filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		switch {
		case err != nil && p == dir:
			return fs.SkipAll
		case err != nil || p == dir:
			return nil
		case strings.HasPrefix(e.Name(), "."):
			if e.IsDir() {
				return fs.SkipDir
			}
			return nil
		case e.IsDir():
			return nil
		}
		// A symlink to a file is followed; one to a folder is not.
		if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() {
			return nil
		}
		if rel, err := filepath.Rel(dir, p); err == nil {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	slices.Sort(files)
	return files
}

// readStatic lists dir's static files, and refuses one that would replace a
// generated file or that `nomi run --web` could not serve.
func readStatic(dir string) (webStatic, error) {
	s := webStatic{dir: dir, files: listStatic(dir)}
	for _, name := range s.files {
		shown := vmhost.DisplayPath(filepath.Join(dir, filepath.FromSlash(name)))
		switch {
		case slices.Contains(webGenerated, name):
			return s, fmt.Errorf("%s: the build writes %s, so a static file cannot have that name", shown, name)
		case strings.HasPrefix(name, "__nomi/"):
			return s, fmt.Errorf("%s: __nomi/ is nomi run --web's own, so a static file cannot be in it", shown)
		}
	}
	return s, nil
}

// webHidden reports whether a slash-separated path has a part that starts
// with a dot, which a browser build neither copies nor serves.
func webHidden(name string) bool {
	for part := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// checkWebOut refuses an output folder that is the static folder or inside
// it, which the next build would copy into itself.
func checkWebOut(out, static string) error {
	abs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(static, abs)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("nomi build --web: the output folder %s is inside the static folder %s; name another with -o",
			vmhost.DisplayPath(abs), vmhost.DisplayPath(static))
	}
	return nil
}

// webLargeModule is the size over which `--web` says how to serve app.wasm.
const webLargeModule = 20 << 20

// webGoBindings is why a browser build refuses a project with Go bindings.
const webGoBindings = "this project has Go bindings, and the browser build cannot link them"

var errWebGoBindings = errors.New("nomi build --web: " + webGoBindings)

var webIndexTemplate = template.Must(template.New("index.html").Parse(webIndexHTML))

// writeWebFolder writes the folder b.out for the js/wasm runner at runner,
// image and the TypeScript declarations decls, and copies the static files
// in.
func writeWebFolder(b buildArgs, runner string, image []byte, decls string, static webStatic, stderr io.Writer) error {
	wasmExecPath, err := ffirun.WasmExec(runner)
	if err != nil {
		return fmt.Errorf("nomi build: %w", err)
	}
	wasmExec, err := os.ReadFile(wasmExecPath)
	if err != nil {
		return fmt.Errorf("nomi build: %w", err)
	}
	module, err := os.ReadFile(runner)
	if err != nil {
		return fmt.Errorf("nomi build: %w", err)
	}
	if !vmrunner.IsWasm(module) {
		return fmt.Errorf("nomi build: the nomi-vm at %s is not a WebAssembly module", runner)
	}
	if module, err = wasmmem.SetMax(module, wasmmem.MaxBytes); err != nil {
		return fmt.Errorf("nomi build: capping the module's memory: %w", err)
	}
	module = append(module, vmrunner.WasmImageSection(image)...)
	var index bytes.Buffer
	title := strings.TrimSuffix(filepath.Base(b.entry), ".nomi")
	if err := webIndexTemplate.Execute(&index, struct{ Title string }{title}); err != nil {
		return err
	}
	contents := map[string][]byte{
		"app.wasm":   module,
		"nomi.js":    webGlue(wasmExec),
		"nomi.d.ts":  []byte(decls),
		"index.html": index.Bytes(),
	}
	if info, err := os.Stat(b.out); err == nil && !info.IsDir() {
		return fmt.Errorf("nomi build: --web writes a folder, and %s is a file", b.out)
	}
	if err := os.MkdirAll(b.out, 0o755); err != nil {
		return fmt.Errorf("nomi build: %w", err)
	}
	if err := removeRetired(b.out, static); err != nil {
		return fmt.Errorf("nomi build: %w", err)
	}
	for _, name := range webFiles {
		if name == "index.html" && slices.Contains(static.files, name) {
			continue
		}
		if err := writeFileReplacing(filepath.Join(b.out, name), contents[name]); err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
	}
	for _, name := range static.files {
		data, err := os.ReadFile(filepath.Join(static.dir, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
		to := filepath.Join(b.out, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
		if err := writeFileReplacing(to, data); err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
	}
	if len(module) > webLargeModule {
		fmt.Fprintf(stderr, "nomi build: app.wasm is %.1f MB; serve it compressed (brotli or gzip) with "+
			"Content-Type: application/wasm\n", float64(len(module))/(1<<20))
	}
	return nil
}

// writeFileReplacing writes data to path through a temporary file beside it,
// so a reader never sees half a file.
func writeFileReplacing(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err == nil {
		err = tmp.Chmod(0o644)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmpPath, path)
	}
	if err != nil {
		_ = os.Remove(tmpPath)
	}
	return err
}
