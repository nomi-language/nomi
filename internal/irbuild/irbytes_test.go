package irbuild

import (
	"testing"
)

func TestIRBytes_TourProgram(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 raw = String.to_bytes("café")
 dbg Bytes.length(raw)
 dbg raw |> Iter.map(Byte.to_int) |> Iter.to_list()
 prefix = String.to_bytes("go:")
 case Bytes.to_string(prefix + raw) {
  Ok(text) -> io.print(text)
  Err(reason) -> io.print(reason)
 }
}
`, "dbg line 4: Bytes.length(raw) = 5\ndbg line 5: raw |> Iter.map(Byte.to_int) |> Iter.to_list() = [99, 97, 102, 195, 169]\ngo:café\n")
}

func TestIRBytes_BoundariesAndImmutableViews(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn echo(data: Bytes): Bytes { data }
fn main() {
 raw = echo(String.to_bytes("café"))
 io.inspect(raw)
 io.inspect(Bytes.at(raw, 3))
 io.inspect(Bytes.at(raw, -1))
 io.inspect(Bytes.at(raw, 5))
 io.inspect(Byte.from_int(255))
 io.inspect(Byte.from_int(-1))
 io.inspect(Byte.from_int(256))
 io.inspect(Bytes.to_string(Bytes.slice(raw, 0, 4)))
 io.inspect(Bytes.to_string(Bytes.slice(raw, 0, 3)))
 io.inspect(Bytes.slice(raw, -9, 99))
 io.inspect(Bytes.slice(raw, 4, 2))
 view = Bytes.slice(raw, 3, 5)
 stream = view |> Iter.map(Byte.to_int)
 io.inspect(stream |> Iter.to_list())
 io.inspect(stream |> Iter.to_list())
 io.inspect(raw)
 io.inspect(String.to_bytes("") |> Iter.map(Byte.to_int) |> Iter.to_list())
}
`, "<<99, 97, 102, 195, 169>>\nSome(195)\nNone\nNone\nSome(255)\nNone\nNone\nErr(\"invalid UTF-8\")\nOk(\"caf\")\n<<99, 97, 102, 195, 169>>\n<<>>\n[195, 169]\n[195, 169]\n<<99, 97, 102, 195, 169>>\n[]\n")
}

func TestIRBytes_UserStructFieldDebug(t *testing.T) {
	verifyLambdaProgram(t, `struct Packet { data: Bytes }
fn main() { dbg Packet{data: String.to_bytes("a")} return }
`, "dbg line 2: Packet{data: String.to_bytes(\"a\")} = Packet{data: <<97>>}\n")
}

// A user struct with a Bytes or Byte field is retained, as a std struct's is.
// A request struct carrying `body: Bytes` is the shape a Go handler callback
// crosses (testdata/go_calls_nomi_handler); while the struct
// declined, so did every function naming it, and main blocked on the
// `handle` it passes to a Go binding as "an ident bound nowhere".
func TestIRBytes_UserStructFieldsCrossCallsAndCallbacks(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Request {
 path: String
 body: Bytes
 flag: Byte
}
fn first(data: Bytes): Byte {
 case Bytes.at(data, 0) {
  Some(b) -> b
  None -> first(String.to_bytes("?"))
 }
}
fn handle(req: Request): Result<Bytes, String> { Ok(req.body) }
fn apply(f: (Request) -> Result<Bytes, String>, r: Request): Result<Bytes, String> { f(r) }
fn main() {
 body = String.to_bytes("xyz")
 r = Request{path: "/", body, flag: first(body)}
 apply(handle, r) |> io.inspect()
 r |> io.inspect()
 (r == r) |> io.inspect()
 Bytes.length(r.body) |> io.print()
}
`, "Ok(<<120, 121, 122>>)\nRequest{path: \"/\", body: <<120, 121, 122>>, flag: 120}\nTrue\n3\n")
}
