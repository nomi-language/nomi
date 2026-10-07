package ffirun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noGoModRoot is a fresh directory with no go.mod in it or above it, or the
// test is skipped.
func noGoModRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, found := findGoModRoot(root); found {
		t.Skip("a go.mod above the temp directory provides a module")
	}
	return root
}

// TestStdPackageDir: a standard library package is found in the toolchain's
// GOROOT, and nothing else is one: a dotted path, a `cmd/` package, and a
// dotless path GOROOT does not hold.
func TestStdPackageDir(t *testing.T) {
	for _, path := range []string{"strings", "math", "net/http", "internal/abi"} {
		dir, ok := StdPackageDir(path)
		if !ok {
			t.Errorf("%s is not a standard library package", path)
			continue
		}
		if want := filepath.Join(stdToolchain().root, "src", filepath.FromSlash(path)); dir != want {
			t.Errorf("%s: dir %s, want %s", path, dir, want)
		}
	}
	for _, path := range []string{"example.com/strings", "cmd/go", "nosuchstdpackage", "strings/nosuch", ""} {
		if dir, ok := StdPackageDir(path); ok {
			t.Errorf("%q is a standard library package at %s", path, dir)
		}
	}
	if v := stdToolchain().version; !strings.HasPrefix(v, "go1.") && !strings.HasPrefix(v, "devel") {
		t.Errorf("toolchain version %q", v)
	}
}

// TestGoImportProblem_StdNeedsNoGoMod: a standard library package is
// provided with no go.mod above the declaring file, unless it is internal to
// the standard library; any other path still needs one.
func TestGoImportProblem_StdNeedsNoGoMod(t *testing.T) {
	nomiPath := filepath.Join(noGoModRoot(t), "main.nomi")
	for _, path := range []string{"strings", "strconv", "math", "net/http"} {
		if why := GoImportProblem(nomiPath, path); why != "" {
			t.Errorf("%s refused: %s", path, why)
		}
	}
	if why, want := GoImportProblem(nomiPath, "internal/abi"), "internal to the Go standard library, which no other module may import"; why != want {
		t.Errorf("internal/abi: got %q, want %q", why, want)
	}
	if why := GoImportProblem(nomiPath, "example.com/x"); !strings.Contains(why, "no go.mod above the declaring file") {
		t.Errorf("example.com/x: got %q", why)
	}
}

// discoverStd discovers the bindings of src written as main.nomi in a
// directory with no go.mod.
func discoverStd(t *testing.T, src string) (string, []DiscoveredPackage) {
	t.Helper()
	root := noGoModRoot(t)
	entry := filepath.Join(root, "main.nomi")
	mustWriteHelper(t, entry, src)
	got, err := discoverForEntry(root, root, entry)
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	return root, got
}

const stdBindingsSource = `gopkg "strings" as strings
gopkg "strconv" as strconv
gopkg "math" as math

fn upper(s: String): String go strings.ToUpper
fn itoa(n: Int): String go strconv.Itoa
fn atoi(s: String): Result<Int, String> go strconv.Atoi
fn sqrt(x: Float): Float go math.Sqrt
`

// TestStdBindings_DiscoveredValidatedAndAdapted: bindings to standard library
// functions are discovered with no go.mod, pass validation against GOROOT's
// source, and get an adapter each.
func TestStdBindings_DiscoveredValidatedAndAdapted(t *testing.T) {
	root, got := discoverStd(t, stdBindingsSource)
	paths := discoveredImportPaths(got)
	if strings.Join(paths, " ") != "math strconv strings" {
		t.Fatalf("discovered %v, want math strconv strings", paths)
	}
	if err := validateDiscoveredGoBindings(root, got); err != nil {
		t.Fatalf("validation: %v", err)
	}
	adapters, err := generateAdapters(root, got)
	if err != nil {
		t.Fatalf("generateAdapters: %v", err)
	}
	for _, r := range adapters.Refused {
		t.Errorf("refused %s: %s", r.Key, r.Reason)
	}
	for _, call := range []string{"strings.ToUpper(", "strconv.Itoa(", "strconv.Atoi(", "math.Sqrt("} {
		if !strings.Contains(adapters.Decls, call) {
			t.Errorf("no adapter calls %s", call)
		}
	}
}

// TestStdBindings_ValidationNamesTheProblem: a function the package does not
// declare, a generic one, and a mismatched signature are each named, and a
// package outside the standard library is refused when there is no go.mod.
func TestStdBindings_ValidationNamesTheProblem(t *testing.T) {
	root, got := discoverStd(t, `gopkg "strings" as strings
gopkg "slices" as slices
gopkg "example.com/elsewhere" as elsewhere

fn shout(s: String): String go strings.Shout
fn biggest(xs: List<Int>): Int go slices.Max
fn upper(n: Int): String go strings.ToUpper
fn f(): Int go elsewhere.F
`)
	err := validateDiscoveredGoBindings(root, got)
	if err == nil {
		t.Fatal("validation passed")
	}
	for _, want := range []string{
		`main.nomi:5:40: Go package "strings" has no top-level function "Shout" for Nomi binding "shout"`,
		`Go function "Max" in package "slices" is generic`,
		`signature mismatch for Go function "ToUpper" in package "strings": parameter 1 "n" projects to String, but Nomi declares Int`,
		`Go package "example.com/elsewhere" is not in the Go standard library, and no go.mod above the declaring file provides it`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}

// TestStdBindings_PrepareWithNoGoMod: Prepare takes the wrapper path for a
// project with no go.mod that binds the standard library, and the wrapper's
// go.mod is its own module, requiring nothing for the standard library.
func TestStdBindings_PrepareWithNoGoMod(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	root := noGoModRoot(t)
	entry := filepath.Join(root, "main.nomi")
	mustWriteHelper(t, entry, stdBindingsSource)
	res, err := Prepare(entry)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.FastPath {
		t.Fatal("Prepare took the fast path")
	}
	goMod, err := os.ReadFile(filepath.Join(res.WrapperDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goMod), "module nomi-build-wrapper") {
		t.Errorf("wrapper go.mod is not its own module:\n%s", goMod)
	}
}

// TestComputeHashes_StdBindingKeysTheToolchain: the Go toolchain's version is
// part of the wrapper's key when a binding names a standard library package,
// and absent otherwise, so a project with no standard library binding keeps
// its key.
func TestComputeHashes_StdBindingKeysTheToolchain(t *testing.T) {
	root, got := discoverStd(t, stdBindingsSource)
	h, err := computeHashes(root, got, nil)
	if err != nil {
		t.Fatal(err)
	}
	tc := stdToolchain()
	if h.GoStd != tc.version+" "+tc.root {
		t.Errorf("GoStd = %q, want the toolchain %q at %q", h.GoStd, tc.version, tc.root)
	}
	other := []DiscoveredPackage{{ImportPath: "example.com/elsewhere"}}
	h, err = computeHashes(root, other, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h.GoStd != "" {
		t.Errorf("GoStd = %q for a project with no standard library binding", h.GoStd)
	}
}
