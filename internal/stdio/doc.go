// Package stdio is std/fs's file system host implementation, and it is
// deliberately not in rt.
//
// std/fs, std/path and std/fs/testing are lowered with each program. std/fs
// crosses through the internal module std/fs/host (the `files` class) to a
// Backend here. std/path cleans, joins and splits with Go's path package
// through std/path/host (rt/path.go, pure, so a typed literal's handler may
// reach it); `extension`, `stem` (no leading dot, none for `.bashrc`),
// `is_absolute` (drives too), `relative` and `matches` (`**`, the
// hidden-name rule) are Nomi.
//
// rt is linked by everything that runs Nomi, and it makes no file system
// call. It holds the io.Error value (rt/ioerror.go), the stream handles
// (rt/streams.go) and a slot on each frame for the file system a call
// reaches (rt/files.go), which is opaque to it. This package fills the
// slot: a Backend is a file system, and every std/fs crossing (fs.go) asks
// the frame for its backend and calls it.
//
// # Backends
//
//   - The operating system's (osfs.go), over Go's os package: one
//     implementation for the whole disk and for an *os.Root inside
//     `fs.within`. It is the default of every native build and of wasip1,
//     where WASI's preopened directories decide what it reaches.
//   - Memory (memfs.go): a map-backed file system with handles, which
//     resolves symlinks a name at a time. It is the default of a js/wasm
//     build (the browser, Go's Node loader, the tour), where Go's os calls
//     answer ENOSYS, and it is the sandbox `std::fs::testing.run` installs
//     (sandbox.go). One implementation serves both, so the browser and the
//     tests cannot drift.
//
// A machine's default is the host's (rt.WithFiles; vmhost.WithFileSystem),
// else this build's (defaultBackend). `std::fs::testing.run` and `fs.within`
// replace it for their body (rt.EnterFiles); tasks inherit it as they
// inherit `io.capture`. `nomi test`, in process and in a Go FFI wrapper,
// gives a test file the disk with its working directory at its module root.
//
// `std::fs::testing.conformance` holds the memory backend to the disk, and std's
// tests run it against both. A test that touches the disk uses a directory
// from `fs.make_temp_dir` with `defer fs.remove_all_quietly(dir)`, or a Go
// `t.TempDir()`.
//
// # Paths
//
// A Nomi path is a String that uses `/` on every operating system
// (ospath.go). Every error names the path as the program passed it.
//
// # Classification
//
// The operating system backend turns a Go error into the io.Error variant
// that names it (classify): absence is NotFound, an existing path
// AlreadyExists, a refusal PermissionDenied, and so on by errno, each
// carrying the path the program passed. What no variant names is `Other`,
// with Go's operation word (`open`, `read`) and the errno's name, never Go's
// prose. A write into a missing directory is NotFound naming the directory.
// The memory backend answers the same variants itself.
package stdio
