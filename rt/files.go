package rt

import "context"

// The file system std/fs reaches, as a slot rt carries and never opens.
//
// rt makes no file system call (internal/stdio does), so the backend is an
// opaque value here: internal/stdio's Backend, which rt does not import. A
// machine's default comes from its host on the context its frames are built
// over (WithFiles): `nomi run` and a built executable leave it unset, which
// stdio reads as the operating system's, and the browser and the tour
// install an in-memory one. `fs_testing.run` and `fs.within` replace it for
// the dynamic extent of one call (EnterFiles), as `io.capture` replaces
// stdin and stdout, so tasks spawned inside inherit the replacement with the
// rest of the spawner's frame, and a nested call takes over for its own.

type filesKey struct{}

// WithFiles answers ctx carrying files as the file system every frame built
// over it reaches, unless a call replaces it.
func WithFiles(ctx context.Context, files any) context.Context {
	return context.WithValue(ctx, filesKey{}, files)
}

// EnterFiles returns the frame a call runs on with fs as its file system:
// a child of parent that carries it, and every frame and task derived from
// it does too.
func EnterFiles(parent *Frame, fs FileSystem) *Frame {
	child := *parent
	child.files = fs
	return &child
}

// FilesOf is the file system the frame reaches: the innermost replacement
// (EnterFiles), else the machine's (WithFiles), else nil, which stdio reads
// as the default for the build.
func FilesOf(fr *Frame) any {
	if fr == nil {
		return nil
	}
	if fs, ok := fr.files.(FileSystem); ok {
		return fs.files
	}
	if fr.ctx != nil {
		return fr.ctx.Value(filesKey{})
	}
	return nil
}

// FilesHeld reports whether a call on fr's lineage replaced the file system
// with one held in memory (`fs_testing.run`'s sandbox, or `fs.within` inside
// one): a debugger replays such a call's file operations rather than
// answering them from its recording, since the replay builds the
// replacement again. A replacement over the disk (`fs.within` outside a
// sandbox) is not held: its operations are answered from the recording, as
// the disk's are.
func (fr *Frame) FilesHeld() bool {
	if fr == nil {
		return false
	}
	fs, ok := fr.files.(FileSystem)
	return ok && fs.held
}

// FileSystem is a file system a call runs against: std/fs_testing's
// sandbox, or the directory `fs.within` confines a call to. It wraps
// internal/stdio's backend, which rt carries and does not name. A call runs
// its body on a frame that reaches it (EnterFiles).
type FileSystem struct {
	files any
	held  bool
}

// NewFileSystem wraps files, a backend, as a file system value. held says
// the backend keeps its files in memory, so a replay can build it again.
func NewFileSystem(files any, held bool) FileSystem { return FileSystem{files: files, held: held} }

// Files is the backend the value wraps.
func (s FileSystem) Files() any { return s.files }

// Held reports whether the backend keeps its files in memory.
func (s FileSystem) Held() bool { return s.held }
