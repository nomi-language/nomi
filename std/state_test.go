package std

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// inFreshProcess runs the test named in a child process whose stdlib is
// made from the artifact this process makes, with env added, and reports
// whether the caller is that child. The stdlib's state is written only by a
// process whose own build of the base modules came first
// (analysis.EncodeStdState).
func inFreshProcess(t *testing.T, name string, env ...string) bool {
	if os.Getenv("NOMI_STD_STATE_CHILD") == "1" {
		return true
	}
	Shared()
	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.count=1", "-test.v")
	cmd.Env = append(append(os.Environ(), "NOMI_STD_STATE_CHILD=1"), env...)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: "+name) {
		t.Errorf("the child failed: %v\n%s", err, out)
	}
	return false
}

// TestStdState_AProcessReadsIt holds a process's stdlib to the state an
// earlier one wrote: the child writes it if no process has, and the
// grandchild reads it.
func TestStdState_AProcessReadsIt(t *testing.T) {
	if !inFreshProcess(t, "TestStdState_AProcessReadsIt") {
		return
	}
	if os.Getenv("NOMI_STD_STATE_GRANDCHILD") == "1" {
		if sharedCore().stateFrom == "" {
			t.Fatal("the base modules were built, not read from their state")
		}
		return
	}
	if Shared() == nil {
		t.Fatal("no stdlib")
	}
	name := "TestStdState_AProcessReadsIt"
	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), "NOMI_STD_STATE_GRANDCHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: "+name) {
		t.Errorf("the grandchild failed: %v\n%s", err, out)
	}
}

// TestStdState_ReadsBackAsTheBuild holds the stdlib's state, written and
// read back under another directory, to the base modules' build it was
// written from: every field of every object reachable from it
// (analysis.CheckStdStateRoundTrip), and every function a module's tree
// declares has the identity it has in the build (stdTreeFuncs), so a
// declaration the builder reads from the tree is the one the analysis
// names.
func TestStdState_ReadsBackAsTheBuild(t *testing.T) {
	if !inFreshProcess(t, "TestStdState_ReadsBackAsTheBuild", "NOMI_STD_STATE=off") {
		return
	}
	lib := Shared()
	if sharedCore().stateFrom != "" {
		t.Fatal("the base modules were read from their state, not built")
	}
	st, dir := lib.state()
	start := time.Now()
	data, err := analysis.EncodeStdState(st, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d modules, %d bytes, encoded in %v", len(st.Files), len(data), time.Since(start))
	start = time.Now()
	got, err := analysis.DecodeStdState(data, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("decoded in %v", time.Since(start))
	built, read := stdTreeFuncs(st), stdTreeFuncs(got)
	for id, n := range built {
		if read[id] != n {
			t.Errorf("%v: the build's trees declare it %d times, the state's %d", id, n, read[id])
		}
	}
	if len(read) != len(built) {
		t.Errorf("the state's trees declare %d std functions, the build's %d", len(read), len(built))
	}
	// Each encoding walks the maps in another order, so what is written
	// first, and where a slice written before is named, differs: a shared
	// slice named near the end once failed a length check that assumed
	// its elements followed it.
	for i := range 20 {
		data, err := analysis.EncodeStdState(st, dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := analysis.DecodeStdState(data, dir, nil); err != nil {
			t.Fatalf("encoding %d does not read back: %v", i, err)
		}
	}
	if err := analysis.CheckStdStateRoundTrip(st, dir, "/elsewhere/std"); err != nil {
		t.Fatal(err)
	}
}

// TestStdState_TheEmbeddedStateIsRead makes the base modules' state as
// internal/irbuild/stdbundlegen does for a binary built with the
// nomi_stdbundle tag: this process makes the artifact (StdArtifact), a
// child whose first build is the base modules' writes their state
// (StdState), and a grandchild that embeds both, as such a binary does,
// reads the state on its first stdlib build. Its base modules are then
// held to Load's, as TestShared_ModulesAreLoadsModules holds a build's. A
// state another binary's codec wrote, or one of another artifact, is
// refused, and the grandchild builds the base modules instead. A copy of
// this test built with -trimpath and run outside the tree finds no stdlib
// directory, as the browser and a release find none: it reads the state
// and builds every other module over it, each named beside the state's
// prelude (analysis.StdFilesDir). vmhost's TestTourWasmReadsItsEmbeddedStdState holds the tour's wasm to
// the same.
func TestStdState_TheEmbeddedStateIsRead(t *testing.T) {
	const name = "TestStdState_TheEmbeddedStateIsRead"
	dir := os.Getenv("NOMI_STD_EMBED_DIR")
	iface, state := filepath.Join(dir, "stdiface.bin"), filepath.Join(dir, "stdstate.bin")
	switch os.Getenv("NOMI_STD_EMBED_STEP") {
	case "state":
		art, err := os.ReadFile(iface)
		if err != nil {
			t.Fatal(err)
		}
		data, err := StdState(art)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(state, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	case "read", "stale":
		var err error
		if embeddedStdInterfaces, err = os.ReadFile(iface); err != nil {
			t.Fatal(err)
		}
		if embeddedStdState, err = os.ReadFile(state); err != nil {
			t.Fatal(err)
		}
		stale := os.Getenv("NOMI_STD_EMBED_STEP") == "stale"
		if stale {
			// The digest of the codec that wrote it, changed.
			format := []byte(analysis.StdStateFormat())
			i := strings.Index(string(embeddedStdState), string(format))
			if i < 0 {
				t.Fatal("the state does not name its format")
			}
			embeddedStdState[i+len(format)-1] ^= 1
		}
		lib := Shared()
		got := sharedCore().stateFrom
		if stale {
			if got == "embedded" {
				t.Fatal("a state of another format was read")
			}
			return
		}
		if got != "embedded" {
			t.Fatalf("the base modules were read from %q, not the embedded state", got)
		}
		if len(lib.Files) < 50 {
			t.Fatalf("the embedded state holds %d modules", len(lib.Files))
		}
		requireLoadsModules(t, lib, Load())
		return
	case "nodir":
		if root, err := analysis.StdlibPath(); err == nil {
			t.Fatalf("this process finds the stdlib at %s, so it cannot stand for one that finds none", root)
		}
		var err error
		if embeddedStdInterfaces, err = os.ReadFile(iface); err != nil {
			t.Fatal(err)
		}
		if embeddedStdState, err = os.ReadFile(state); err != nil {
			t.Fatal(err)
		}
		lib := Shared()
		if got := sharedCore().stateFrom; got != "embedded" {
			t.Fatalf("the base modules were read from %q, not the embedded state", got)
		}
		if want := filepath.Join(stdBuildDir(), "prelude.nomi"); lib.Files["prelude"].FilePath != want {
			t.Fatalf("the state's prelude is at %s, not %s", lib.Files["prelude"].FilePath, want)
		}
		// Every module beyond the base, built over the state.
		c := sharedCore()
		c.mu.Lock()
		all, err := c.ensureLocked(c.names)
		c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if len(all.Files) <= len(lib.Files) {
			t.Fatalf("building every module added none to the %d the state holds", len(lib.Files))
		}
		return
	}
	dir = t.TempDir()
	art, err := StdArtifact()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stdiface.bin"), art, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"state", "read", "stale"} {
		cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.count=1", "-test.v")
		// NOMI_STD_STATE_CHILD is left unset, so no cached state is read
		// before the embedded one is tried; NOMI_ARTIFACT_CACHE=off keeps
		// the grandchild from reading or writing one after.
		cmd.Env = append(os.Environ(), "NOMI_STD_EMBED_STEP="+step, "NOMI_STD_EMBED_DIR="+dir, "NOMI_ARTIFACT_CACHE=off")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "--- PASS: "+name) {
			t.Fatalf("the %s step failed: %v\n%s", step, err, out)
		}
		if step == "stale" && !strings.Contains(string(out), "not its own") {
			t.Errorf("a stale embedded state was refused without a word:\n%s", out)
		}
	}
	if testing.Short() {
		return
	}
	// The browser and a release find no stdlib directory, so a build names
	// the stdlib's files under a relative one, and so must the state read
	// back there. This test binary finds the in-tree stdlib through
	// runtime.Caller; a copy built with -trimpath, run outside the tree,
	// finds none.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain to build a trimmed copy of this test with")
	}
	trimmed := filepath.Join(t.TempDir(), "std.test")
	if runtime.GOOS == "windows" {
		trimmed += ".exe"
	}
	build := exec.Command("go", "test", "-c", "-trimpath", "-o", trimmed, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building a trimmed copy of this test: %v\n%s", err, out)
	}
	cmd := exec.Command(trimmed, "-test.run=^"+name+"$", "-test.count=1", "-test.v")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "NOMI_STD_EMBED_STEP=nodir", "NOMI_STD_EMBED_DIR="+dir, "NOMI_ARTIFACT_CACHE=off", "NOMI_STD_PATH=")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: "+name) {
		t.Fatalf("the step with no stdlib directory failed: %v\n%s", err, out)
	}
}

// stdTreeFuncs counts, by identity, the functions the modules' trees
// declare: each node of a module's tree its analysis files a std function
// under (analysis.FileAnalysis.StdFuncs).
func stdTreeFuncs(s analysis.StdState) map[analysis.StdFunc]int {
	out := map[analysis.StdFunc]int{}
	for module, fa := range s.Files {
		ids := fa.StdFuncs()
		for _, n := range s.Nodes[module] {
			ast.Inspect(n, func(m ast.Node) bool {
				if id, ok := ids[m]; ok {
					out[id]++
				}
				return true
			})
		}
	}
	return out
}
