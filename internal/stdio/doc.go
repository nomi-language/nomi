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
// (rt/streams.go), the rt.FileSystem value that wraps a backend, and a
// slot on each frame for the file system a call reaches (rt/files.go),
// which is opaque to it. This package fills the slot: a Backend is a file
// system.
//
// # Crossings
//
// Every std/fs crossing (fs.go) acts through the rt.FileSystem it is
// handed, and one obtains it: `here`, the frame's. A path-taking std/fs
// function hands each crossing what `here` answers, and a `Dir` hands it
// the confined file system it holds, which `within` made from another
// (`fs.open_dir` from `here`'s, `Dir.subdir` from a Dir's). So there is one
// crossing per operation, and one place a path is confined: the backend's
// Within. `here` is the one crossing an effects list names (the `files`
// class, Outside); the others are held, since each acts only through its
// operand, and a Dir's reaches nothing outside its directory. `release`
// lets go of what `within` made, and every crossing through it after is
// Closed (reach).
//
// The memory backend's Within is a view with a floor; the disk's is an
// *os.Root, whose guarantees and gaps are Go's: on Linux, macOS, Windows
// and wasip1 each name is opened relative to the directory before it, so a
// symlink or a directory swapped in during a call cannot lead out, and a
// root follows its directory through a rename. It does not stop a mount
// point, a Linux bind mount, `/proc`'s links or a device file under the
// directory; on Windows it refuses reserved device names; on Unix its Chmod
// (`Dir.set_mode`) can be raced onto a symlink's target; on wasip1 it has
// no Chmod. Its Chtimes, Link, Chown and Lchown serve `Dir.set_times`,
// `Dir.create_link`, `Dir.set_owner` and `Dir.set_link_owner`. A path through more than 8 symlinks is refused on the disk
// (Go's limit) and 40 in memory (loopLimit). A js/wasm build reaches only
// memory, so Go's js Root, which checks symlinks without the race safety,
// is never used.
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
// inherit `io.capture`. `nomi test`
// gives a test file the disk with its working directory at its package root.
//
// `std::fs::testing.conformance` holds the memory backend to the disk, and std's
// tests run it against both. A test that touches the disk uses a directory
// from `fs.make_temp_dir` with `defer fs.remove_all_quietly(dir)`, or a Go
// `t.TempDir()`.
//
// # Syncs and locks
//
// A File's source (rt.FileSource) says how the file is made durable and how
// its handle locks a range of its bytes; rt keeps the handle, refuses an
// exclusive lock on one opened only to read, and waits for a lock by asking
// again (rt.FileLock). Each backend answers the asking once:
//
//   - On Linux and macOS (osfile_ofd.go) a sync is Go's Sync, which on macOS
//     is F_FULLFSYNC, and a data sync is fdatasync on Linux and fsync(2) on
//     macOS, which leaves the data in the drive's cache. A directory's sync
//     (`fs.sync_dir`) is fsync(2) on both, as SQLite's Unix layer makes it,
//     so on macOS it leaves the entries in the drive's cache too. A lock is an open file description lock
//     (F_OFD_SETLK), which belongs to the handle: two handles in one
//     process exclude each other as two processes do, and the kernel drops
//     a handle's locks when it closes.
//   - On Windows (osfile_windows.go) a sync is FlushFileBuffers and a lock
//     LockFileEx, which belongs to the handle too but is mandatory, stacks
//     rather than replacing a lock on the same bytes, and unlocks only a
//     range exactly as it was locked. A directory has no flush there, so
//     `fs.sync_dir` checks the directory and returns.
//   - In memory, and on wasip1's disk, where WASI has no locks, the process
//     keeps the locks itself (locks.go), by the same rules as the open file
//     description locks, so the sandbox answers as the disk does on Linux
//     and macOS; `testing.conformance` holds the two to the same rules.
//     Memory has nothing to make durable, and a sync there fails only as a
//     Failure on `Operation.Sync` says.
//
// # Metadata, links and owners
//
// A metadata crossing answers Info, whose fields past the kind, size,
// modification time and mode are what a file system may not keep: the
// access, status change and birth times, the link count, the device and
// inode, and the numeric owner. The disk reads them from the stat record
// Go already holds (sysInfo, stat_<os>.go), with no call of its own on
// macOS, Linux and wasip1, where an *os.Root's stat answers the same
// record; on Windows it opens the file again for GetFileInformationByHandle.
// Linux's birth time is statx's alone and is left out; Windows keeps no
// status change time and owns files by SID, so it answers no owner and
// `set_owner` is Unsupported there (ownerTarget), as it is on wasip1. The
// mode carries the setuid, setgid and sticky bits as Unix numbers them
// (ModeBits), and `set_mode` takes them numbered so (FileMode). Memory keeps every time, numbers each node (its inode on
// device 0), counts a node's names once it has a second (linksOf), and has
// an owner only once `set_owner` gives one.
//
// `create_link` never follows a symlink at its target, as an *os.Root's
// Link and Linux's link(2) do not; on macOS, whose link(2) does, the disk
// calls linkat (link_darwin.go). `disk_usage` opens its path, under a root
// as any other path, and asks about the open file's file system (fstatfs,
// or GetDiskFreeSpaceEx of the handle's volume), so no path is joined
// outside a root. Windows's calls go through the syscall package's kernel32
// procedures (osfile_windows.go), never golang.org/x/sys/windows, which
// imports net, which the lean runner does not link. Memory reports 1 GiB, less what its files hold.
// `same_file` is std/fs's own comparison of two metadata answers' device
// and inode, and `truncate` opens the file and truncates the File, so
// neither has a crossing. `user_name` and `group_name` cross through the
// frame's file system: the disk's asks os/user, and memory, a root and
// Windows name no one.
//
// # Paths
//
// A Nomi path is a String that uses `/` on every operating system
// (ospath.go). Every error names the path as the program passed it.
//
// # Classification
//
// The operating system backend turns a Go error into the io.Error variant
// that names it (Classify) with rt's one table, rt.IOErrorOf: absence is
// NotFound, an existing path AlreadyExists, a refusal PermissionDenied,
// and so on by errno, each carrying the path the program passed, and
// `Other` for what no variant names. Classify names first the one error
// that is a file system's own, a name that leaves its root (InvalidPath).
// The errno is the one Unix answers for the failure, on every system:
// rt.Errno reads a Windows code through one table, and std/net's errors
// are sorted by the same reading. A write into a missing directory is
// NotFound naming the directory. A file's reads and writes, a process's
// streams and standard input fail through the same table, as every stream
// with no Fail hook does (rt.ReadSource). A package that links an error's
// own package names that error first and hands the rest to rt.IOErrorOf:
// internal/stdhttp's streams name net/http's and net's (streamFailure),
// which this package does not link, since the lean runners leave net out.
// The memory backend answers the same variants itself.
package stdio
