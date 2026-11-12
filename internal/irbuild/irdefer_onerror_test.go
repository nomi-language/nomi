package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

// `defer on error call(...)` (spec §9 *`defer`*) runs its call only when
// the block is left through an error exit: a `?` or `return` answering
// `Err` or `None`, or a fault. A normal exit, and a return of `Ok`, drop
// it. It shares the LIFO list with plain `defer`.
func TestIRDefer_OnErrorRunsOnlyOnAnErrorExit(t *testing.T) {
	verifyDeferProgram(t, `import std::io

fn note(label: String) -> Unit
  io.print("cleanup ${label}")
end

fn check(ok: Bool) -> Result<Int, String>
  if ok then Ok(1) else Err("no")
end

fn by_try(ok: Bool) -> Result<Int, String>
  defer note("plain first")
  defer on error note("on error")
  defer note("plain last")
  v = check(ok)?
  Ok(v + 1)
end

fn by_return(ok: Bool) -> Result<Int, String>
  defer on error note("returned")
  if not ok then
    return Err("returned")
  end
  Ok(2)
end

fn in_a_block(ok: Bool) -> Result<Int, String>
  begin
    defer on error note("block")
    io.print("in the block")
  end
  v = check(ok)?
  Ok(v)
end

fn maybe(some: Bool) -> Maybe<Int>
  defer on error note("none")
  m = if some then Some(1) else None
  v = m?
  Some(v)
end

fn main()
  io.inspect(by_try(True))
  io.inspect(by_try(False))
  io.inspect(by_return(True))
  io.inspect(by_return(False))
  io.inspect(in_a_block(False))
  io.inspect(maybe(True))
  io.inspect(maybe(False))
end
`, "cleanup plain last\ncleanup plain first\nOk(2)\n"+
		"cleanup plain last\ncleanup on error\ncleanup plain first\nErr(\"no\")\n"+
		"Ok(2)\ncleanup returned\nErr(\"returned\")\n"+
		"in the block\nErr(\"no\")\n"+
		"Some(1)\ncleanup none\nNone\n", "by_try", "by_return", "in_a_block", "maybe")
}

// A fault is an error exit: the error-only call runs with the plain ones,
// most recent first, before the fault leaves the program.
func TestIRDefer_OnErrorRunsOnAFault(t *testing.T) {
	src := `import std::io

fn note(label: String) -> Unit
  io.print("cleanup ${label}")
end

fn divide(a: Int, b: Int) -> Result<Int, String>
  defer note("plain")
  defer on error note("on error")
  Ok(a / b)
end

fn main()
  io.inspect(divide(4, 2))
  io.inspect(divide(1, 0))
end
`
	const stdout = "cleanup plain\nOk(2)\ncleanup on error\ncleanup plain\n"
	const fault = "line 10: division by zero"
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, err = vm.NewProgram(res.IR[0], res.IRModules(), &out).Run("main")
	if out.String() != stdout || err == nil || err.Error() != fault {
		t.Fatalf("VM: %q, %v", out.String(), err)
	}
}

// The shape the release rules ask for: a resource acquired, released on
// error, and handed on when the function succeeds. A failing step after the
// acquisition releases it; success hands it on unreleased.
func TestIRDefer_OnErrorReleasesAResourceAFailedOpenAcquired(t *testing.T) {
	verifyDeferProgram(t, `import std::io

struct Conn
  name: String
end

impl Release for Conn
  fn release(conn: Conn)
    close(conn)
  end
end

fn close(conn: Conn)
  io.print("close ${conn.name}")
end

struct Store
  conn: Conn
end

fn connect(name: String) -> Result<Conn, String>
  Ok(Conn{name:})
end

fn migrate(conn: Conn, ok: Bool) -> Result<Unit, String>
  if ok then Ok(Unit) else Err("migrate ${conn.name} failed")
end

fn open(name: String, ok: Bool) -> Result<Store, String>
  conn = connect(name)?
  defer on error close(conn)
  _ = migrate(conn, ok)?
  Ok(Store{conn:})
end

fn main()
  case open("a", True)
    Ok(store) ->
      io.print("opened ${store.conn.name}")
      close(store.conn)
    Err(e) -> io.print(e)
  end
  case open("b", False)
    Ok(store) -> close(store.conn)
    Err(e) -> io.print(e)
  end
end
`, "opened a\nclose a\nclose b\nmigrate b failed\n", "open")
}
