package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// The release rules (spec §9 *Releasing resources*): a resource a body
// acquires is released by a deferred call or handed on, and a Commit value
// whose discard is deferred is committed on every normal return.

const releaseImports = "import std::fs\nimport std::io.{self, Error, Reader, Writer}\n\n"

// releaseErrors checks src with std and returns the release rules' errors.
func releaseErrors(t *testing.T, src string) []analysis.TypeError {
	t.Helper()
	var out []analysis.TypeError
	for _, e := range checkWithStdlib(releaseImports + src) {
		if e.Code == analysis.UnreleasedResourceCode || e.Code == analysis.UncommittedResourceCode || e.Code == analysis.DiscardedResourceCode || e.Code == analysis.EscapedReleaseCode ||
			e.Code == analysis.ErrorExitLeakCode || e.Code == analysis.ReturnLeakCode || e.Code == analysis.DeferOnErrorNoFailureCode {
			out = append(out, e)
		} else if !strings.Contains(e.Message, "never read") {
			t.Fatalf("the front end rejects this for another reason: %s", e.Error())
		}
	}
	return out
}

// releaseLine is line n of src as releaseErrors numbers it.
func releaseLine(n int) int { return n + strings.Count(releaseImports, "\n") }

func expectReleaseErrors(t *testing.T, src string, want ...string) []analysis.TypeError {
	t.Helper()
	errs := releaseErrors(t, src)
	var got []string
	for _, e := range errs {
		got = append(got, e.Error())
	}
	if len(got) != len(want) {
		t.Fatalf("got %d errors, want %d:\n  %s", len(got), len(want), strings.Join(got, "\n  "))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("error %d:\n got  %s\n want %s", i, got[i], want[i])
		}
	}
	return errs
}

func at(line, col int, msg string) string {
	return analysis.TypeError{Line: releaseLine(line), Col: col, Message: msg}.Error()
}

func TestReleaseDeferredIsAccepted(t *testing.T) {
	expectReleaseErrors(t, `fn first(path: String) -> Result<Maybe<String>, Error>
    r = fs.open(path)?
    defer Reader.close_quietly(r)
    Reader.read_line(r)
end
`)
}

func TestReleaseMissingDeferIsAnError(t *testing.T) {
	expectReleaseErrors(t, `fn all(path: String) -> Result<String, Error>
    r = fs.open(path)?
    Reader.read_all_text(r)
end
`, at(2, 5, "`r` is never released; write `defer Reader.close_quietly(r)` after it, or return it"))
}

func TestReleaseHandedOnIsAccepted(t *testing.T) {
	expectReleaseErrors(t, `struct Pair
    reader: Reader
    name: String
end

fn returned(path: String) -> Result<Reader, Error>
    r = fs.open(path)?
    Ok(r)
end

fn returned_early(path: String, early: Bool) -> Result<Reader, Error>
    r = fs.open(path)?
    if early then return Ok(r)
    Ok(r)
end

fn in_a_tuple(path: String) -> Result<(Reader, String), Error>
    r = fs.open(path)?
    Ok((r, path))
end

fn stored(path: String) -> Result<Pair, Error>
    r = fs.open(path)?
    pair = Pair{reader: r, name: path}
    Ok(pair)
end

fn from_a_case(path: String) -> Result<Reader, Error>
    r = case fs.open(path)
        Ok(r) -> r
        Err(e) -> return Err(e)
    end
    Ok(r)
end
`)
}

func TestReleasePassingToACallIsAUse(t *testing.T) {
	expectReleaseErrors(t, `fn consume(r: Reader) -> Result<String, Error>
    defer Reader.close_quietly(r)
    Reader.read_all_text(r)
end

fn caller(path: String) -> Result<String, Error>
    r = fs.open(path)?
    consume(r)
end
`, at(7, 5, "`r` is never released; write `defer Reader.close_quietly(r)` after it, or return it"))
}

func TestReleaseDoesNotApplyToStandardStreamsOrMemory(t *testing.T) {
	expectReleaseErrors(t, `fn greet()
    w = io.stdout
    _ = Writer.print(w, "hi")
    r = Reader.from_text("a\nb\n")
    _ = Reader.read_line(r)
end
`)
}

func TestReleaseOnlyTheBindingsBlockDefers(t *testing.T) {
	expectReleaseErrors(t, `fn half(path: String, close: Bool) -> Result<String, Error>
    r = fs.open(path)?
    if close then
        defer Reader.close_quietly(r)
    end
    Reader.read_all_text(r)
end
`, at(2, 5, "`r` is never released; write `defer Reader.close_quietly(r)` after it, or return it"))
}

func TestReleaseCaseArmBindsAnAcquisition(t *testing.T) {
	expectReleaseErrors(t, `fn arm(path: String) -> Result<String, Error>
    case fs.open(path)
        Ok(r) -> Reader.read_all_text(r)
        Err(e) -> Err(e)
    end
end

fn arm_block(path: String) -> Result<String, Error>
    case fs.open(path)
        Ok(r) ->
            defer Reader.close_quietly(r)
            Reader.read_all_text(r)
        Err(e) -> Err(e)
    end
end
`, at(3, 12, "`r` is never released; write `defer Reader.close_quietly(r)` after it, or return it"))
}

func TestReleaseUserType(t *testing.T) {
	expectReleaseErrors(t, `struct Lock
    name: String
end

impl Release for Lock
    fn release(lock: Lock)
        unlock(lock)
    end
end

fn unlock(lock: Lock)
    _ = lock
end

fn take(name: String) -> Result<Lock, String>
    Ok(Lock{name:})
end

fn held() -> Result<Unit, String>
    lock = take("a")?
    defer unlock(lock)
    Ok(Unit)
end

fn forgotten() -> Result<Unit, String>
    lock = take("a")?
    _ = lock.name
    Ok(Unit)
end
`, at(26, 5, "`lock` is never released; write `defer unlock(lock)` after it, or return it"))
}

func TestReleaseAViewOfAResourceIsNotAnAcquisition(t *testing.T) {
	expectReleaseErrors(t, `import std::sql.{self, Conn}

fn work(conn: Conn) -> Result<Unit, sql.Error>
    tx = Conn.begin_transaction(conn)?
    Conn.commit(tx)
end
`)
}

func TestCommitOnTheSuccessPathIsAccepted(t *testing.T) {
	expectReleaseErrors(t, `fn save(path: String, rows: List<String>) -> Result<Unit, Error>
    w = fs.create(path, mode: .ReplaceAtomically)?
    defer Writer.discard(w)
    Writer.write_lines(w, rows)?
    Writer.close(w)?
    Ok(Unit)
end

fn save_tail(path: String) -> Result<Unit, Error>
    w = fs.create(path)?
    defer Writer.discard(w)
    Writer.write_text(w, "x")?
    Writer.close(w)
end
`)
}

func TestCommitIsNotNeededOnAFailurePath(t *testing.T) {
	expectReleaseErrors(t, `fn save(path: String, fail: Bool) -> Result<Unit, Error>
    w = fs.create(path)?
    defer Writer.discard(w)
    Writer.write_text(w, "x")?
    if fail then return Err(Error.TimedOut)
    _ = Writer.write_text(w, "y")!
    if fail then unreachable "stopped"
    Writer.close(w)?
    Ok(Unit)
end
`)
}

func TestCommitMissingOnOkIsAnError(t *testing.T) {
	expectReleaseErrors(t, `fn save(path: String) -> Result<Unit, Error>
    w = fs.create(path)?
    defer Writer.discard(w)
    Writer.write_text(w, "x")?
    Ok(Unit)
end
`, at(5, 5, "`w` is discarded here: this path returns without `Writer.close(w)?`"))
}

func TestCommitMissingAtTheEndOfAUnitFunctionIsAnError(t *testing.T) {
	expectReleaseErrors(t, `fn save(path: String)
    w = fs.create(path)!
    defer Writer.discard(w)
    Writer.write_text(w, "x")!
end
`, at(4, 5, "`w` is discarded here: this path returns without `Writer.close(w)!`"))
}

func TestCommitOnEveryBranchIsAccepted(t *testing.T) {
	expectReleaseErrors(t, `fn save(path: String, header: Bool) -> Result<Unit, Error>
    w = fs.create(path)?
    defer Writer.discard(w)
    if header then
        Writer.write_text(w, "h")?
        Writer.close(w)?
        Ok(Unit)
    else
        Writer.close(w)?
        Ok(Unit)
    end
end
`)
}

func TestCommitMissingOnOneBranchIsAnErrorThere(t *testing.T) {
	expectReleaseErrors(t, `fn save(path: String, header: Bool) -> Result<Unit, Error>
    w = fs.create(path)?
    defer Writer.discard(w)
    if header then
        Writer.write_text(w, "h")?
        return Ok(Unit)
    end
    Writer.close(w)?
    Ok(Unit)
end
`, at(6, 9, "`w` is discarded here: this path returns without `Writer.close(w)?`"))
}

func TestCommitNotNeededAfterACommittingRelease(t *testing.T) {
	expectReleaseErrors(t, `fn log(path: String) -> Result<Unit, Error>
    w = fs.create(path, mode: .Append)?
    defer Writer.close_quietly(w)
    Writer.write_text(w, "x")?
    Ok(Unit)
end
`)
}

func TestCommitDiscardingOnPurposeSettles(t *testing.T) {
	expectReleaseErrors(t, `fn abandon(path: String) -> Result<Unit, Error>
    w = fs.create(path, mode: .ReplaceAtomically)?
    defer Writer.discard(w)
    Writer.write_text(w, "half")?
    Writer.discard(w)
    Ok(Unit)
end
`)
}

func TestReleaseInATestBodyAndALambda(t *testing.T) {
	expectReleaseErrors(t, `fn each(paths: List<String>)
    Iter.each(paths, fn p ->
        r = fs.open(p)!
        _ = Reader.read_line(r)
    end)
end

test "reads"
    r = fs.open("a")?
    assert Reader.read_line(r) == Ok(None)
end
`,
		at(3, 9, "`r` is never released; write `defer Reader.close_quietly(r)` after it, or return it"),
		at(9, 5, "`r` is never released; write `defer Reader.close_quietly(r)` after it, or return it"))
}

func TestReleaseFixDefersAndCommits(t *testing.T) {
	errs := expectReleaseErrors(t, `fn save(path: String) -> Result<Unit, Error>
    w = fs.create(path)?
    Writer.write_text(w, "x")?
    Ok(Unit)
end
`, at(2, 5, "`w` is never released; write `defer Writer.discard(w)` after it, or return it"))
	fix := errs[0].Fix
	if fix == nil || fix.Title != "Release `w`" || len(fix.Edits) != 2 {
		t.Fatalf("fix = %+v, want the defer and the close", fix)
	}
	if e := fix.Edits[0]; e.Start.Line != releaseLine(3) || e.Start.Col != 1 || e.Text != "    defer Writer.discard(w)\n" {
		t.Errorf("first edit = %+v", e)
	}
	if e := fix.Edits[1]; e.Start.Line != releaseLine(4) || e.Start.Col != 1 || e.Text != "    Writer.close(w)?\n" {
		t.Errorf("second edit = %+v", e)
	}
}

func TestReleaseStreamedResponse(t *testing.T) {
	expectReleaseErrors(t, `import std::http.{self, Request, StreamedResponse}
import std::url.Url

fn first(url: Url) -> Result<Maybe<String>, http.Error>
    response = http.stream(Request{url:})?
    defer StreamedResponse.close(response)
    Reader.read_line(response.body) |> Result.map_err(fn e -> http.Error.Body{error: e})
end

fn forgotten(url: Url) -> Result<Maybe<String>, http.Error>
    response = http.stream(Request{url:})?
    Reader.read_line(response.body) |> Result.map_err(fn e -> http.Error.Body{error: e})
end

fn fetched(url: Url) -> Result<String, http.Error>
    response = http.get(url)?
    http.Response.text(response)
end
`, at(11, 5, "`response` is never released; write `defer StreamedResponse.close(response)` after it, or return it"))
}

func TestReleaseDiscardedAcquisitionIsAnError(t *testing.T) {
	errs := expectReleaseErrors(t, `fn touch(path: String) -> Result<Unit, Error>
    _ = fs.open(path)?
    _ = fs.open(path)
    Ok(Unit)
end

test "opens"
    assert True
    fs.open("a")?
end

fn each(paths: List<String>)
    Iter.each(paths, fn p ->
        fs.open(p)!
    end)
end

fn named(path: String) -> Result<Unit, Error>
    reader = 1
    _ = reader
    _ = fs.open(path)?
    Ok(Unit)
end
`,
		at(2, 5, "the `Reader` this acquires is never released; bind it and write `defer Reader.close_quietly(reader)` after it"),
		at(3, 5, "the `Reader` this acquires is never released; bind it and write `defer Reader.close_quietly(reader)` after it"),
		at(9, 5, "the `Reader` this acquires is never released; bind it and write `defer Reader.close_quietly(reader)` after it"),
		at(14, 9, "the `Reader` this acquires is never released; bind it and write `defer Reader.close_quietly(reader)` after it"),
		at(21, 5, "the `Reader` this acquires is never released; bind it and write `defer Reader.close_quietly(reader2)` after it"))
	fix := errs[0].Fix
	if fix == nil || fix.Title != "Release the `Reader`" || len(fix.Edits) != 2 {
		t.Fatalf("fix = %+v, want the name and the defer", fix)
	}
	if e := fix.Edits[0]; e.Start.Line != releaseLine(2) || e.Start.Col != 5 || e.End.Col != 6 || e.Text != "reader" {
		t.Errorf("name edit = %+v", e)
	}
	if e := fix.Edits[1]; e.Start.Line != releaseLine(3) || e.Start.Col != 1 || e.Text != "    defer Reader.close_quietly(reader)\n" {
		t.Errorf("defer edit = %+v", e)
	}
	fix = errs[2].Fix
	if fix == nil || len(fix.Edits) != 2 {
		t.Fatalf("fix = %+v, want the binding and the defer", fix)
	}
	if e := fix.Edits[0]; e.Start.Line != releaseLine(9) || e.Start.Col != 5 || e.End != e.Start || e.Text != "reader = " {
		t.Errorf("binding edit = %+v", e)
	}
	if e := fix.Edits[1]; e.Start.Line != releaseLine(10) || e.Start.Col != 1 || e.Text != "    defer Reader.close_quietly(reader)\n" {
		t.Errorf("defer edit = %+v", e)
	}
}

func TestReleaseAcquisitionAsAValueIsHandedOn(t *testing.T) {
	expectReleaseErrors(t, `fn opened(path: String) -> Result<Reader, Error>
    fs.open(path)
end

fn chosen(path: String, other: String, first: Bool) -> Result<Reader, Error>
    r = if first then fs.open(path)? else fs.open(other)?
    Ok(r)
end
`)
}

func TestReleaseDiscardedWriterFixCommits(t *testing.T) {
	errs := expectReleaseErrors(t, `fn make(path: String) -> Result<Unit, Error>
    _ = fs.create(path)?
    Ok(Unit)
end
`, at(2, 5, "the `Writer` this acquires is never released; bind it and write `defer Writer.discard(writer)` after it"))
	fix := errs[0].Fix
	if fix == nil || len(fix.Edits) != 3 {
		t.Fatalf("fix = %+v, want the name, the defer and the close", fix)
	}
	if e := fix.Edits[2]; e.Start.Line != releaseLine(3) || e.Text != "    Writer.close(writer)?\n" {
		t.Errorf("close edit = %+v", e)
	}
}

// lockSource declares a Release type of the program's own and functions
// that return one directly, on 26 lines.
const lockSource = `struct Lock
    name: String
end

impl Release for Lock
    fn release(lock: Lock)
        unlock(lock)
    end
end

fn unlock(lock: Lock)
    _ = lock
end

fn lock(name: String) -> Lock
    Lock{name:}
end

struct Holder
    lock: Lock
end

fn renewed(lock: Lock) -> Lock
    lock
end
`

func TestReleaseADirectReturnAcquires(t *testing.T) {
	expectReleaseErrors(t, lockSource+`
fn held()
    l = lock("a")
    defer unlock(l)
end

fn forgotten()
    l = lock("a")
    _ = l.name
end

fn dropped()
    _ = lock("a")
end

fn views(holder: Holder)
    a = renewed(holder.lock)
    _ = a
    b = Lock{name: "b"}
    _ = b
end
`,
		at(33, 5, "`l` is never released; write `defer unlock(l)` after it, or return it"),
		at(38, 5, "the `Lock` this acquires is never released; bind it and write `defer unlock(lock2)` after it"))
}

func TestReleaseADirectReaderOrWriterIsNoAcquisition(t *testing.T) {
	expectReleaseErrors(t, `import std::http.{Body, Request}
import std::process.Child

fn read(request: Request, child: Child) -> Result<Unit, Error>
    body = Body.reader(request.body)
    _ = Reader.read_line(body)?
    out = Child.stdout(child)
    _ = Reader.read_line(out)?
    _ = Reader.from_text("x")
    Ok(Unit)
end
`)
}

// escapeMsg is the error at a `defer` of released when subject leaves its
// block as how says, name being the name that leaves.
func escapeMsg(released, subject, name, how string) string {
	return "this `defer` releases `" + released + "` when the block ends, but " + subject + " " + how +
		"; defer the release where `" + name + "` is finally used, or hand `" + name + "` on without deferring it here"
}

func TestReleaseDeferredValueLeavingACaseBranchIsAnError(t *testing.T) {
	expectReleaseErrors(t, `struct Store
    reader: Reader
    path: String
end

struct App
    store: Store
end

fn open_store(path: String) -> Result<Store, Error>
    r = fs.open(path)?
    Ok(Store{reader: r, path:})
end

fn app(kind: String, path: String) -> Result<App, Error>
    store: Store = case kind
        "file" ->
            db = open_store(path)?
            defer Reader.close_quietly(db.reader)
            db
        _ -> open_store("/dev/null")?
    end
    Ok(App{store:})
end
`, at(19, 13, escapeMsg("db.reader", "`db`", "db", "leaves the block as its value")))
}

func TestReleaseDeferredValueReturnedIsAnError(t *testing.T) {
	expectReleaseErrors(t, `fn opened(path: String) -> Result<Reader, Error>
    r = fs.open(path)?
    defer Reader.close_quietly(r)
    Ok(r)
end

fn early(path: String, now: Bool) -> Result<Reader, Error>
    r = fs.open(path)?
    defer Reader.close_quietly(r)
    if now then
        return Ok(r)
    end
    fs.open("/dev/null")
end

fn bare(r: Reader) -> Reader
    defer Reader.close_quietly(r)
    r
end
`,
		at(3, 5, escapeMsg("r", "`r`", "r", "leaves the block inside its value")),
		at(9, 5, escapeMsg("r", "`r`", "r", "leaves the block inside a `return`'s value")),
		at(17, 5, escapeMsg("r", "`r`", "r", "leaves the block as its value")))
}

func TestReleaseDeferredValueInAReturnedStructIsAnError(t *testing.T) {
	expectReleaseErrors(t, `struct Pair
    reader: Reader
    name: String
end

fn pair(path: String) -> Result<Pair, Error>
    r = fs.open(path)?
    defer Reader.close_quietly(r)
    p = Pair{reader: r, name: path}
    Ok(p)
end

fn direct(path: String) -> Result<Pair, Error>
    r = fs.open(path)?
    defer Reader.close_quietly(r)
    Ok(Pair{reader: r, name: path})
end
`,
		at(8, 5, escapeMsg("r", "`p`", "p", "leaves the block inside its value")),
		at(15, 5, escapeMsg("r", "`r`", "r", "leaves the block inside its value")))
}

func TestReleaseDeferredValueCapturedByAReturnedLambdaIsAnError(t *testing.T) {
	expectReleaseErrors(t, `fn reading(path: String) -> Result<() -> Result<String, Error>, Error>
    r = fs.open(path)?
    defer Reader.close_quietly(r)
    Ok(fn -> Reader.read_all_text(r))
end
`, at(3, 5, escapeMsg("r", "a lambda that captures `r`", "r", "leaves the block inside its value")))
}

func TestReleaseDeferredValueUsedInItsBlockIsAccepted(t *testing.T) {
	expectReleaseErrors(t, `struct Store
    reader: Reader
    path: String
end

fn open_store(path: String) -> Result<Store, Error>
    r = fs.open(path)?
    Ok(Store{reader: r, path:})
end

fn first(path: String) -> Result<String, Error>
    text = begin
        db = open_store(path)?
        defer Reader.close_quietly(db.reader)
        Reader.read_all_text(db.reader)?
    end
    Ok(text)
end

fn name(path: String) -> Result<String, Error>
    db = open_store(path)?
    defer Reader.close_quietly(db.reader)
    Ok(db.path)
end

fn outer(path: String) -> Result<String, Error>
    r = fs.open(path)?
    defer Reader.close_quietly(r)
    text = begin
        all = Reader.read_all_text(r)?
        all
    end
    next = fn -> Reader.read_all_text(r)
    Ok("${text}${next()?}")
end
`)
}

// A `defer` in the outer block of an entry boot runs after `main` finishes
// (spec §15 *Scoped application fields*), so the value it releases may
// leave in the App. One in a block nested in boot still runs at that
// block's end.
func TestReleaseDeferredInAnEntryBootMayLeaveInTheApp(t *testing.T) {
	expectReleaseErrors(t, `struct App
    reader: Reader
end

fn boot() -> Result<App, Error>
    r = fs.open("/dev/null")?
    defer Reader.close_quietly(r)
    Ok(App{reader: r})
end

fn main() end
`)
	expectReleaseErrors(t, `struct App
    reader: Reader
end

fn boot() -> Result<App, Error>
    reader = begin
        r = fs.open("/dev/null")?
        defer Reader.close_quietly(r)
        r
    end
    Ok(App{reader:})
end

fn main() end
`, at(8, 9, escapeMsg("r", "`r`", "r", "leaves the block as its value")))
}

// A value one path hands on is handed on or released on every other path
// that returns normally.
func leakMsg(name, release string) string {
	return "`" + name + "` leaks here: this path returns without handing it on or releasing it; call `" + release + "(" + name + ")` before the return"
}

func TestReleaseAPathThatDropsAHandedOnValueIsAnError(t *testing.T) {
	expectReleaseErrors(t, `fn maybe_open(path: String, want: Bool) -> Result<Maybe<Reader>, Error>
    r = fs.open(path)?
    defer on error Reader.close_quietly(r)
    if not want then
        return Ok(None)
    end
    Ok(Some(r))
end
`, at(5, 9, leakMsg("r", "Reader.close_quietly")))
	expectReleaseErrors(t, `fn maybe_open(path: String, want: Bool) -> Result<Maybe<Reader>, Error>
    r = fs.open(path)?
    if not want then
        return Ok(None)
    end
    Ok(Some(r))
end
`, at(4, 9, leakMsg("r", "Reader.close_quietly")))
	expectReleaseErrors(t, `fn maybe_open(path: String, want: Bool) -> Result<Maybe<Reader>, Error>
    r = fs.open(path)?
    if want then Ok(Some(r)) else Ok(None)
end
`, at(3, 35, leakMsg("r", "Reader.close_quietly")))
	expectReleaseErrors(t, `fn by_mode(path: String, mode: Int, no: Error) -> Result<Maybe<Reader>, Error>
    r = fs.open(path)?
    defer on error Reader.close_quietly(r)
    case mode
        0 -> Ok(None)
        1 -> Err(no)
        _ -> Ok(Some(r))
    end
end
`, at(5, 14, leakMsg("r", "Reader.close_quietly")))
}

func TestReleaseEveryPathHandingOnIsAccepted(t *testing.T) {
	expectReleaseErrors(t, `struct Holder
    reader: Reader
end

fn deferred(path: String, want: Bool) -> Result<Maybe<String>, Error>
    r = fs.open(path)?
    defer Reader.close_quietly(r)
    if not want then
        return Ok(None)
    end
    Ok(Some(Reader.read_all_text(r)?))
end

fn both(path: String, wrap: Bool) -> Result<Maybe<Reader>, Error>
    r = fs.open(path)?
    if wrap then Ok(Some(r)) else Ok(Some(r))
end

fn held(path: String, want: Bool) -> Result<Maybe<Holder>, Error>
    r = fs.open(path)?
    h = Holder{reader: r}
    if not want then
        return Ok(None)
    end
    Ok(Some(h))
end

fn released(path: String, want: Bool) -> Result<Maybe<Reader>, Error>
    r = fs.open(path)?
    defer on error Reader.close_quietly(r)
    if not want then
        Reader.close_quietly(r)
        return Ok(None)
    end
    Ok(Some(r))
end

fn fails(path: String, want: Bool, no: Error) -> Result<Reader, Error>
    r = fs.open(path)?
    defer on error Reader.close_quietly(r)
    if not want then
        return Err(no)
    end
    Ok(r)
end

fn as_a_value(path: String, want: Bool) -> Result<Maybe<Reader>, Error>
    r = fs.open(path)?
    found = if want then
        Some(r)
    else
        Reader.close_quietly(r)
        None
    end
    Ok(found)
end
`)
}

func TestReleasePathLeakFixReleasesBeforeTheReturn(t *testing.T) {
	errs := expectReleaseErrors(t, `fn maybe_open(path: String, want: Bool) -> Result<Maybe<Reader>, Error>
    r = fs.open(path)?
    if not want then
        return Ok(None)
    end
    Ok(Some(r))
end
`, at(4, 9, leakMsg("r", "Reader.close_quietly")))
	fix := errs[0].Fix
	if fix == nil || len(fix.Edits) != 1 {
		t.Fatalf("want one edit, got %+v", fix)
	}
	e := fix.Edits[0]
	if e.Start.Line != releaseLine(4) || e.Start.Col != 1 || e.Text != "        Reader.close_quietly(r)\n" {
		t.Errorf("edit = %+v", e)
	}
	// A return that shares its line with what comes before it gets no fix.
	errs = expectReleaseErrors(t, `fn maybe_open(path: String, want: Bool) -> Result<Maybe<Reader>, Error>
    r = fs.open(path)?
    if want then Ok(Some(r)) else Ok(None)
end
`, at(3, 35, leakMsg("r", "Reader.close_quietly")))
	if errs[0].Fix != nil {
		t.Errorf("want no fix, got %+v", errs[0].Fix)
	}
}
