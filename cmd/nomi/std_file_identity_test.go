package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStdFile_OneModuleHoweverNamed: one std source file is one module
// however a command names it. `nomi test` and `nomi check`, run from a
// working directory outside the checkout, accept std/sessions.nomi by its
// absolute path, by a relative path through `..`, and through a symlink to
// the file and to the std directory, and `nomi run` refuses it as a module
// each way. Through a symlink, `nomi check` named the file's module by the
// unresolved path (`std/../../<tmp>/sessions`), a second copy of the module
// whose types were not the stdlib's.
func TestStdFile_OneModuleHoweverNamed(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	file := filepath.Join(repoRoot(t), "std", "sessions.nomi")
	cwd := realPath(t, t.TempDir())
	names := []struct{ what, path string }{{"absolute", file}}
	if rel, err := filepath.Rel(cwd, realPath(t, file)); err != nil {
		// On Windows the temporary directory and the checkout may be on
		// two volumes, and no relative path joins them.
		t.Logf("no relative path from %s to %s: %v", cwd, file, err)
	} else if !strings.HasPrefix(rel, "..") {
		t.Fatalf("the relative path %s from %s does not go through ..", rel, cwd)
	} else {
		names = append(names, struct{ what, path string }{"relative with ..", rel})
	}
	links := t.TempDir()
	if err := os.Symlink(file, filepath.Join(links, "sessions.nomi")); err != nil {
		// Windows creates a symlink only with the privilege or developer
		// mode; the other spellings still hold there.
		t.Logf("no symlinks here (%v): checking the other spellings", err)
	} else {
		names = append(names, struct{ what, path string }{"symlink to the file", filepath.Join(links, "sessions.nomi")})
		// The link is not named `std`, which the analyzer reads as a std
		// directory by its name alone.
		if err := os.Symlink(filepath.Dir(file), filepath.Join(links, "library")); err != nil {
			t.Fatalf("a file symlink was made and a directory one was not: %v", err)
		}
		names = append(names, struct{ what, path string }{"symlink to std", filepath.Join(links, "library", "sessions.nomi")})
	}
	for _, n := range names {
		t.Run(n.what, func(t *testing.T) {
			out, err := runNomiInDir(t, cacheRoot, cwd, "", "test", n.path)
			requireNoPanic(t, "test", out)
			if err != nil || !strings.Contains(out, "test result: ok.") {
				t.Fatalf("nomi test %s did not pass (err %v):\n%s", n.path, err, out)
			}
			out, err = runNomiInDir(t, cacheRoot, cwd, "", "check", n.path)
			requireNoPanic(t, "check", out)
			if err != nil || strings.Contains(out, "error:") {
				t.Fatalf("nomi check %s did not pass (err %v):\n%s", n.path, err, out)
			}
			out, err = runNomiInDir(t, cacheRoot, cwd, "", "run", n.path)
			requireNoPanic(t, "run", out)
			if err == nil || !strings.Contains(out, "is a stdlib module, not a program") {
				t.Fatalf("nomi run %s was not refused as a stdlib module (err %v):\n%s", n.path, err, out)
			}
		})
	}
}

// TestStdFile_AnEditedListedModuleCallsItselfThroughItsOwnName: `nomi
// test` of a std module lowered with each program, whose file differs from
// the module this nomi embeds, tests the edited file. Its `//!` prompts
// import the module by its std path, which binds the module as the
// embedded std declares it, and each call of the module's own generic
// function through that name is a call of the edited file's function:
// qualified, bare, piped and as a function value. The qualified call
// panicked with "siblingGenericInstance: the owner unit is another file's
// gen". The standard library is a copy of this checkout's, named by
// NOMI_STD_PATH, so the edit touches no tracked file.
func TestStdFile_AnEditedListedModuleCallsItselfThroughItsOwnName(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	stdPath := copyStdTree(t, t.TempDir())
	sessions := filepath.Join(stdPath, "sessions.nomi")
	replaceInFile(t, sessions,
		"//! import std::sessions.{self, CookieStore, Session, SessionKey}\n//!\n//! store = CookieStore{key: SessionKey.derive(\"a test secret of 32 bytes or more\")!, secure: False}\n",
		"//! import std::sessions.{self, CookieStore, Session, SessionKey, write}\n//!\n//! store = CookieStore{key: SessionKey.derive(\"a test secret of 32 bytes or more\")!, secure: False}\n")
	replaceInFile(t, sessions,
		"//! cleared = sessions.write(Response{}, Session.clear(session))\n",
		"//! cleared = sessions.write(Response{}, Session.clear(session))\n"+
			"//! bare = write(Response{}, Session.set(session, \"bo\"))\n"+
			"//! piped = Response{} |> sessions.write(Session.set(session, \"cy\"))\n"+
			"//! by_name: (Response, Session<String>) -> Response = sessions.write\n"+
			"//! by_bare_name: (Response, Session<String>) -> Response = write\n"+
			"//! assert List.length(Response.cookies(bare)) == 1 and List.length(Response.cookies(piped)) == 1\n"+
			"//! assert List.length(Response.cookies(by_name(Response{}, Session.set(session, \"di\")))) == 1\n"+
			"//! assert List.length(Response.cookies(by_bare_name(Response{}, Session.set(session, \"ed\")))) == 1\n")
	out, err := runNomiWithStd(t, cacheRoot, stdPath, "test", sessions)
	requireNoPanic(t, "test", out)
	if err != nil || !strings.Contains(out, "test result: ok.") || !strings.Contains(out, "write //! test") {
		t.Fatalf("the edited std/sessions.nomi did not pass (err %v):\n%s", err, out)
	}
}

// requireNoPanic fails t when out, a nomi command's output, holds a Go
// panic.
func requireNoPanic(t *testing.T, cmd, out string) {
	t.Helper()
	if strings.Contains(out, "panic:") || strings.Contains(out, "goroutine ") {
		t.Fatalf("nomi %s panicked:\n%s", cmd, out)
	}
}

// realPath is path with its symlinks resolved: on macOS a temporary
// directory is under /var, a symlink to /private/var, and a relative path
// is counted from the real one.
func realPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
