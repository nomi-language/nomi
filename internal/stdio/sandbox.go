package stdio

import (
	"sort"

	"github.com/nomi-language/nomi/rt"
)

// std/fs_host's sandbox functions, which std/fs_testing's `run` is built
// from: a sandbox is a Memory file system wrapped as a held rt.FileSystem.
// The VM's `fs_host.run_on` runs the body on a frame that reaches it.

// NewSandbox is std/fs_host's `new_sandbox`: an in-memory file system whose
// working directory is cwd, holding files, dirs and the symlinks links maps
// to their targets, whose directories above them exist too.
func NewSandbox(cwd string, files rt.Map[string, rt.Bytes], dirs *rt.List[string], links rt.Map[string, string]) rt.FileSystem {
	m := NewMemory(cwd)
	for _, e := range rt.MapEntries(files) {
		m.Put(e.Key, []byte(e.Val))
	}
	for l := dirs; l != nil; l = l.Tail {
		m.PutDir(l.Head)
	}
	for _, e := range rt.MapEntries(links) {
		m.PutLink(e.Key, e.Val)
	}
	return rt.NewFileSystem(m, true)
}

func memoryOf(s rt.FileSystem) *Memory {
	m, ok := s.Files().(*Memory)
	if !ok {
		rt.Trap("fs_testing: the file system is not a sandbox")
	}
	return m
}

// SandboxFail is std/fs_host's `sandbox_fail`: the nth operation op on path
// fails with err, every one when nth is 0.
func SandboxFail(s rt.FileSystem, path string, op int64, err rt.IOError, nth int64) rt.Unit {
	if op < int64(OpRead) || op > int64(OpClose) {
		rt.Trap("fs_testing: the operation is not an Operation")
	}
	memoryOf(s).Fail(path, Operation(op), err, nth)
	return rt.Unit{}
}

// SandboxFiles is std/fs_host's `sandbox_files`: every file, by absolute
// path.
func SandboxFiles(s rt.FileSystem) rt.Map[string, rt.Bytes] {
	files := memoryOf(s).Files()
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	entries := make([]rt.MapEntry[string, rt.Bytes], len(paths))
	for i, p := range paths {
		entries[i] = rt.MapEntry[string, rt.Bytes]{Key: p, Val: rt.Bytes(files[p])}
	}
	return rt.MapOf(rt.HashString, rt.Eq[string], entries)
}

// SandboxLog is std/fs_host's `sandbox_log`: each operation, in order.
func SandboxLog(s rt.FileSystem) *rt.List[string] { return listOf(memoryOf(s).Log()) }

// SandboxOpen is std/fs_host's `sandbox_open`: the handles still open.
func SandboxOpen(s rt.FileSystem) *rt.List[string] { return listOf(memoryOf(s).OpenHandles()) }

func listOf(items []string) *rt.List[string] {
	var out *rt.List[string]
	for i := len(items) - 1; i >= 0; i-- {
		out = rt.Cons(items[i], out)
	}
	return out
}
