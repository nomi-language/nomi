package vmhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/std"
)

// decodedScenarioEnv names the scenario a child of
// TestStdObjects_DecodedForWhatAProcessReaches runs, and decodedOutEnv the
// file it writes its counts to.
const (
	decodedScenarioEnv = "NOMI_VMHOST_DECODED_SCENARIO"
	decodedOutEnv      = "NOMI_VMHOST_DECODED_OUT"
)

const decodedHello = `import std::io

fn main()
    io.print("hello")
end
`

// decodedHTTP is the first block of the tour's HTTP page.
const decodedHTTP = `import
    std::http.{self, Response, Status}
    std::http::testing
    std::url.Url
end

fn greeting(name: String) -> Result<String, http.Error>
    response = http.get(Url"https://api.example.com/greet/${name}")?
    if not Status.is_success(response.status) then return Ok("no greeting: ${response.status}")
    Response.text(response)
end

fn main()
    ran = testing.run(fn request ->
        Ok(Response{body: .Text("hello from ${request.url.path}")})
    end, fn ->
        greeting("ann b")
    end)

    dbg ran.value
end
`

// TestStdObjects_DecodedForWhatAProcessReaches counts what a process decodes
// of the shared std modules' objects (irbuild.StdObjectsDecodedForTest) when
// it loads one program, each in a fresh child over one warm artifact cache:
// hello world, the tour's first HTTP block, std/json's cases as `nomi test
// std/json.nomi` loads them, and hello world after building every std
// module (std.All), as the language server does. A process decodes a shared
// module's object when the stdlib builds the module, with the modules whose
// objects define what it names, so each of the first three decodes fewer
// modules than the last, which decodes every one with IR, and hello world,
// which builds only the base modules and what they import, decodes fewer
// than the HTTP block.
func TestStdObjects_DecodedForWhatAProcessReaches(t *testing.T) {
	if scenario := os.Getenv(decodedScenarioEnv); scenario != "" {
		decodedChild(t, scenario)
		return
	}
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string]string{
		"hello": filepath.Join(dir, "hello.nomi"),
		"http":  filepath.Join(dir, "http.nomi"),
		"json":  filepath.Join(root, "std", "json.nomi"),
		"all":   filepath.Join(dir, "hello.nomi"),
	}
	for name, src := range map[string]string{"hello": decodedHello, "http": decodedHTTP} {
		if err := os.WriteFile(files[name], []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cache := t.TempDir()
	scenarios := []string{"hello", "http", "json", "all"}
	run := func(scenario string) [3]int {
		out := filepath.Join(t.TempDir(), "counts")
		cmd := exec.Command(os.Args[0], "-test.run=^TestStdObjects_DecodedForWhatAProcessReaches$", "-test.count=1")
		cmd.Env = append(os.Environ(), decodedScenarioEnv+"="+scenario, decodedOutEnv+"="+out,
			decodedFileEnv+"="+files[scenario], artifactcache.RootEnv+"="+cache)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: the child failed: %v\n%s", scenario, err, b)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("%s: the child wrote no counts: %v", scenario, err)
		}
		var c [3]int
		if _, err := fmt.Sscan(string(data), &c[0], &c[1], &c[2]); err != nil {
			t.Fatalf("%s: counts %q: %v", scenario, data, err)
		}
		return c
	}
	// The first runs write the bundle's sections to the cache.
	for _, s := range scenarios {
		run(s)
	}
	got := map[string][3]int{}
	var table strings.Builder
	fmt.Fprintf(&table, "\n  %-6s %8s %10s %10s", "", "modules", "functions", "bytes")
	for _, s := range scenarios {
		got[s] = run(s)
		fmt.Fprintf(&table, "\n  %-6s %8d %10d %10d", s, got[s][0], got[s][1], got[s][2])
	}
	t.Logf("the shared std modules' objects a fresh process decodes:%s", table.String())
	all := got["all"][0]
	if all == 0 {
		t.Fatal("a process that builds every std module decoded none, so nothing was counted")
	}
	for _, s := range scenarios[:3] {
		if got[s][0] >= all {
			t.Errorf("%s decoded %d shared modules, as many as a process that builds every module (%d)", s, got[s][0], all)
		}
	}
	if got["hello"][0] >= got["http"][0] {
		t.Errorf("hello world decoded %d shared modules and the HTTP block %d", got["hello"][0], got["http"][0])
	}
}

// decodedFileEnv is the program a child of
// TestStdObjects_DecodedForWhatAProcessReaches loads.
const decodedFileEnv = "NOMI_VMHOST_DECODED_FILE"

// decodedChild loads the scenario's program and writes what the process
// decoded.
func decodedChild(t *testing.T, scenario string) {
	file := os.Getenv(decodedFileEnv)
	var err error
	switch scenario {
	case "json":
		_, err = LoadStdlib(file)
	case "all":
		std.All()
		_, err = Load(file)
	default:
		_, err = Load(file)
	}
	if err != nil {
		t.Fatalf("%s: %v", scenario, err)
	}
	m, f, b := irbuild.StdObjectsDecodedForTest()
	if err := os.WriteFile(os.Getenv(decodedOutEnv), []byte(fmt.Sprintf("%d %d %d\n", m, f, b)), 0o644); err != nil {
		t.Fatal(err)
	}
}
