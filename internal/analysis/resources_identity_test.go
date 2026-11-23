package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A call commits a Commit value when it is the protocol's function, by the
// declaration its callee names: `Commit.commit(w)` and `Writer.commit(w)`
// do. A program's own function named `commit` is not std's; read by the
// name, it took the commit's place and the writer's work was discarded
// with no error.
func TestResourceIdentity_AProgramCommitIsNoCommit(t *testing.T) {
	expectReleaseErrors(t, `fn keep(path: String) -> Result<Unit, Error>
    w = fs.create(path, mode: .ReplaceAtomically)?
    defer Writer.discard(w)
    Commit.commit(w)?
    Ok(Unit)
end

fn keep_too(path: String) -> Result<Unit, Error>
    w = fs.create(path, mode: .ReplaceAtomically)?
    defer Writer.discard(w)
    Writer.commit(w)?
    Ok(Unit)
end
`)
	expectReleaseErrors(t, `fn commit(_w: Writer) -> Result<Unit, Error>
    Ok(Unit)
end

fn save(path: String) -> Result<Unit, Error>
    w = fs.create(path, mode: .ReplaceAtomically)?
    defer Writer.discard(w)
    commit(w)?
    Ok(Unit)
end
`, at(9, 5, "`w` is discarded here: this path returns without `Writer.close(w)?`"))
}

// A deferred release is the discarding one when it is the protocol's
// function, by its declaration: `defer Release.release(w)` discards a
// writer, so the path commits. A program's own `release` is not std's;
// read by the name, a `defer` of it was taken for the discard.
func TestResourceIdentity_AProgramReleaseIsNoDiscard(t *testing.T) {
	expectReleaseErrors(t, `fn save(path: String) -> Result<Unit, Error>
    w = fs.create(path, mode: .ReplaceAtomically)?
    defer Release.release(w)
    Ok(Unit)
end
`, at(4, 5, "`w` is discarded here: this path returns without `Writer.close(w)?`"))
	expectReleaseErrors(t, `fn release(w: Writer)
    Writer.close_quietly(w)
end

fn save(path: String) -> Result<Unit, Error>
    w = fs.create(path, mode: .ReplaceAtomically)?
    defer release(w)
    Ok(Unit)
end
`)
}

// A return of std's `Err` or `None` leaves by failing, by the variant the
// checker resolved: under `import std::results.Result.{Err as Fail}`,
// `return Fail(...)` is one, which a `defer on error` covers; read by the
// name, it was taken for a normal return that leaked the reader.
func TestResourceIdentity_StdErrUnderAnotherNameFails(t *testing.T) {
	expectReleaseErrors(t, `import std::results.Result.{Err as Fail}

fn first(path: String, want: Bool) -> Result<Reader, String>
    Ok(r) = fs.open(path) else return Fail("no file")
    defer on error Reader.close_quietly(r)
    if not want then return Fail("unwanted")
    Ok(r)
end
`)
}

// A program enum's variant named `Err` is no failure: returning it is a
// normal return, which leaks the reader. Read by the name, it was taken
// for a failure and the leak went unreported.
func TestResourceIdentity_AProgramErrVariantIsNoFailure(t *testing.T) {
	expectReleaseErrors(t, `enum Opened
    Got Reader
    Err String
end

fn first(path: String, want: Bool) -> Opened
    Ok(r) = fs.open(path) else return Opened.Err("no file")
    if not want then return Opened.Err("unwanted")
    Opened.Got(r)
end
`, at(8, 0, "`r` leaks here: this path returns without handing it on or releasing it; call `Reader.close_quietly(r)` before the return"))
}

// A path commits a Commit value with a call of the function the type's
// `impl Commit` calls, by the declaration its callee names: under
// `import std::io.{Writer as Sink}`, `Sink.close(w)?` commits. A program's
// own `close` is not Writer's; read by the function's name, it took the
// commit's place and the writer's work was discarded with no error.
func TestResourceIdentity_AProgramCloseIsNoCommit(t *testing.T) {
	expectReleaseErrors(t, `import std::io.{Writer as Sink}

fn save(path: String) -> Result<Unit, Error>
    w = fs.create(path, mode: .ReplaceAtomically)?
    defer Sink.discard(w)
    Sink.close(w)?
    Ok(Unit)
end
`)
	expectReleaseErrors(t, `fn close(_w: Writer) -> Result<Unit, Error>
    Ok(Unit)
end

fn save(path: String) -> Result<Unit, Error>
    w = fs.create(path, mode: .ReplaceAtomically)?
    defer Writer.discard(w)
    close(w)?
    Ok(Unit)
end
`, at(9, 5, "`w` is discarded here: this path returns without `Writer.close(w)?`"))
}

// A deferred call is the type's usual release, which discards a Commit
// value's work, when its callee is the function the type's `impl Release`
// calls, by its declaration. A program's own `discard` is not Writer's;
// read by the name, a `defer` of it was taken for the discard and asked
// for a commit.
func TestResourceIdentity_AProgramDiscardIsNoUsualRelease(t *testing.T) {
	expectReleaseErrors(t, `fn discard(w: Writer)
    Writer.close_quietly(w)
end

fn save(path: String) -> Result<Unit, Error>
    w = fs.create(path, mode: .ReplaceAtomically)?
    defer discard(w)
    Ok(Unit)
end
`)
}

// std/sql's `impl Release for Conn` calls `close_quietly(conn)` by its
// bare name, which a caller writes `sql.close_quietly`: by the declaration
// the call names, that call releases on the path it is on.
func TestResourceIdentity_ABareNamedStdReleaseSettlesAPath(t *testing.T) {
	expectReleaseErrors(t, `import std::sql.{self, Conn}

fn option(connect: () -> Result<Conn, sql.Error>, want: Bool) -> Result<Option<Conn>, sql.Error>
    conn = connect()?
    defer on error sql.close_quietly(conn)
    if not want then
        sql.close_quietly(conn)
        return Ok(None)
    end
    Ok(Some(conn))
end
`)
}

// A program type's usual release and commit, declared in another file,
// are the declarations that file's impls call, the same in a check from
// source and in one against the file's interface, whose impl bodies no
// check reads. A program function of the same name in the entry is
// neither.
func TestResourceIdentity_AnotherFilesReleaseIsItsDeclaration(t *testing.T) {
	p := draftProject()
	want, got := interfaceDumps(p, p.texts)
	if want != got {
		t.Fatalf("checked against interfaces differs from a full check:\n%s", dumpDiff(want, got))
	}
	full := newManager().CheckProgram(p.root(), p.entry, p.texts)
	var msgs []string
	for _, e := range full.Errors[p.entry] {
		msgs = append(msgs, e.Error())
	}
	wantMsg := "line 18, col 5: `d` is discarded here: this path returns without `Draft.publish(d)?`"
	if len(msgs) != 1 || msgs[0] != wantMsg {
		t.Fatalf("got %q, want only %q", msgs, wantMsg)
	}
}

// An edit to the body of a type's `impl Commit` that makes it call another
// function changes what the entry's check reads of the file: the commit
// the entry's `kept` makes is no longer the type's. The recheck checks the
// program again rather than keep the entry's old answer.
func TestResourceIdentity_ARecheckReadsTheEditedCommit(t *testing.T) {
	p := draftProject()
	edit := replace("commit body calls another function", "draft", "        Draft.publish(d)\n", "        Draft.publish_quietly(d)\n")
	diff, outcomes := runEdits(t, p, []editStep{edit}, newManager().RecheckWithOutcome, true)
	if diff != "" {
		t.Fatal(diff)
	}
	if o := outcomes[0]; o.Path != analysis.RecheckFull {
		t.Fatalf("the edit went %s (%s), want a full check", o.Path, o.Reason)
	}
}

// draftProject is a program type with a usual release and commit in one
// file, and an entry that acquires it in another.
func draftProject() incrementalProject {
	return incrementalProject{name: "resource in another file", entry: "main", texts: map[string]string{
		"draft": `pub struct Draft
    text: String
end

impl Release for Draft
    fn release(d: Draft)
        Draft.discard(d)
    end
end

impl Commit for Draft
    fn commit(d: Draft) -> Result<Unit, String>
        Draft.publish(d)
    end
end

impl Draft
    pub fn open(text: String) -> Result<Draft, String>
        Ok(Draft{text:})
    end

    pub fn discard(_d: Draft)
        Unit
    end

    pub fn publish(_d: Draft) -> Result<Unit, String>
        Ok(Unit)
    end

    pub fn publish_quietly(_d: Draft) -> Result<Unit, String>
        Ok(Unit)
    end
end
`,
		"main": `import draft.{Draft}

fn publish(_d: Draft) -> Result<Unit, String>
    Ok(Unit)
end

fn kept() -> Result<Unit, String>
    d = Draft.open("a")?
    defer Draft.discard(d)
    Draft.publish(d)?
    Ok(Unit)
end

fn lost() -> Result<Unit, String>
    d = Draft.open("b")?
    defer Draft.discard(d)
    publish(d)?
    Ok(Unit)
end

fn main()
    _ = kept()
    _ = lost()
end
`,
	}}
}

// A call builds a value, and so neither acquires nor is a use that hands
// it on, when its callee names a variant or a type, by the declaration the
// checker resolved it to: `Handle(raw)`, a distinct type's constructor,
// acquires nothing. An impl function may be named in PascalCase; read by
// its capital, `Lock.Take("a")` was taken for a constructor and the lock
// it acquires went unreleased with no error.
func TestResourceIdentity_ACapitalizedFunctionIsNoConstructor(t *testing.T) {
	expectReleaseErrors(t, `struct Lock
    name: String
end

impl Release for Lock
    fn release(lock: Lock)
        Lock.unlock(lock)
    end
end

impl Lock
    fn Take(name: String) -> Result<Lock, String>
        Ok(Lock{name:})
    end

    fn unlock(lock: Lock)
        _ = lock
    end
end

type Handle Int

impl Release for Handle
    fn release(_h: Handle)
        Unit
    end
end

fn wrap(raw: Int) -> Int
    h = Handle(raw)
    Handle(n) = h
    n
end

fn forgotten() -> Result<Unit, String>
    lock = Lock.Take("a")?
    _ = lock.name
    Ok(Unit)
end
`, at(36, 5, "`lock` is never released; write `defer Lock.unlock(lock)` after it, or return it"))
}
