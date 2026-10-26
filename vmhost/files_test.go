package vmhost_test

import (
	"bytes"
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

const filesTasksSrc = `import {
    std/fs
    std/fs_testing.{self, Sandbox}
    std/io
    std/tasks.Task
}

fn write_all(prefix: String) -> List<Result<Unit, io.Error>> {
    concurrent {
        tasks = 1..=8 |> Iter.map(fn i -> Task.spawn(fn -> fs.write_text("${prefix}${i}.txt", "${i}"))) |> Iter.to_list()
        tasks |> Iter.map(fn t -> Task.await(t)) |> Iter.to_list()
    }
}

fn main() {
    ran = fs_testing.run(#Sandbox{}, fn -> write_all("sandbox-"))
    io.print(Iter.count(Map.keys(ran.files)))
    io.print(Iter.all?(write_all("page-"), fn r -> r == Ok(Unit)))
    io.print(fs.read_text("page-3.txt"))
}
`

// Tasks share the file system of the frame they were spawned on: eight tasks
// inside fs_testing.run write to its sandbox at once, and eight outside it
// write to the program's own, here an in-memory one the host gave it
// (WithFileSystem), which the host reads back after the run.
func TestFiles_TasksShareTheirFileSystem(t *testing.T) {
	files := vmhost.NewMemoryFiles(map[string][]byte{"seed.txt": []byte("s")})
	p, err := vmhost.LoadSource("main", filesTasksSrc, vmhost.WithFileSystem(files))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if out.String() != "8\nTrue\nOk(3)\n" {
		t.Fatalf("output %q", out.String())
	}
	got := files.Files()
	want := map[string][]byte{"seed.txt": []byte("s")}
	for i := 1; i <= 8; i++ {
		want["page-"+string(rune('0'+i))+".txt"] = []byte(string(rune('0' + i)))
	}
	if !maps.EqualFunc(got, want, bytes.Equal) {
		t.Fatalf("the host's files are %v", got)
	}
	for name := range got {
		if strings.HasPrefix(name, "sandbox-") {
			t.Fatalf("a sandboxed write reached the program's file system: %s", name)
		}
	}
}
