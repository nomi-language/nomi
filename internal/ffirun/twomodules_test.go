package ffirun

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// twoModulesFiles is a project that reaches two Nomi modules, lite and pg,
// whose Go packages share the name goside, each with a describe.nomi that
// binds a struct named Table (of different fields) and an opaque Handle:
// sqlkit's tool, which imports sqlite/describe and postgres/describe, is
// such a program. Each binding must reach its own module's Go.
func twoModulesFiles(name, field, goField string) map[string]string {
	return map[string]string{
		name + "/go.mod":    "module example.com/sql/" + name + "\n\ngo 1.26.3\n",
		name + "/nomi.toml": "[module]\nname = \"" + name + "\"\n",
		name + "/goside/goside.go": fmt.Sprintf(`package goside

type Table struct {
	Name string
	%s int64
}

type Handle struct{ n int64 }

func Open(n int64) *Handle { return &Handle{n: n} }

func Count(h *Handle) int64 { return h.n }

func Describe(h *Handle) []Table { return []Table{{Name: %q, %s: h.n}} }
`, goField, name, goField),
		name + "/describe.nomi": fmt.Sprintf(`gopkg "example.com/sql/%s/goside"

pub struct Table {
    name: String
    %s: Int
}

pub opaque type Handle go goside.Handle

pub fn open(n: Int) -> Handle go goside.Open

pub fn count(h: Handle) -> Int go goside.Count

pub fn describe(h: Handle) -> List<Table> go goside.Describe
`, name, field),
	}
}

func TestFFIRun_TwoModulesWithOnePackageNameEachReachTheirOwnGo(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	root := t.TempDir()
	files := map[string]string{
		"go.mod": fmt.Sprintf(`module example.com/app

go 1.26.3

require (
	github.com/nomi-language/nomi v0.0.0
	example.com/sql/lite v0.0.0
	example.com/sql/pg v0.0.0
)

replace github.com/nomi-language/nomi => %s

replace example.com/sql/lite => ./lite

replace example.com/sql/pg => ./pg
`, repoRoot(t)),
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"main.nomi": `import {
    std/io
    lite/describe as ld
    pg/describe as pd
}

fn main() {
    a = ld.open(2)
    b = pd.open(3)
    io.inspect(ld.describe(a))
    io.inspect(pd.describe(b))
    io.print("${ld.count(a)} ${pd.count(b)}")
}
`,
	}
	for path, text := range twoModulesFiles("lite", "columns", "Columns") {
		files[path] = text
	}
	for path, text := range twoModulesFiles("pg", "rows", "Rows") {
		files[path] = text
	}
	for path, text := range files {
		mustWriteHelper(t, filepath.Join(root, filepath.FromSlash(path)), text)
	}
	entry := filepath.Join(root, "main.nomi")
	res, err := Prepare(entry)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cmd := exec.Command("go", "run", "-mod=mod", res.WrapperPath, wrapperModeFlag, "vmrun", entry)
	cmd.Dir = res.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run wrapper: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	want := `[Table{name: "lite", columns: 2}]
[Table{name: "pg", rows: 3}]
2 3`
	if got := strings.TrimSpace(stdout.String()); got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
}
