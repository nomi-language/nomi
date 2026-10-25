package irbuild

import "testing"

// A std type imported under another name lowers wherever the name is written:
// a variant's field, a struct's field, a parameter, a literal and a pattern.
// The file declares its own `Error` beside the import, so a lookup by the
// declared name would pick the wrong enum. verifyLambdaProgram fails when the
// front end rejects the program.
func TestImportAlias_StdEnumAsAFieldType(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/currency.{Currency, Error as CurrencyError}
  std/io
}
enum Error { Mine }
derive Display for Error
enum Problem {
  Bad {error: CurrencyError}
  Local {error: Error}
}
struct Holder {
  error: CurrencyError
}
fn describe(p: Problem) -> String {
  case p {
    #.Bad{error} -> "bad: ${error}"
    #.Local{error} -> "local: ${error}"
  }
}
fn code(e: CurrencyError) -> String {
  case e {
    #CurrencyError.NotIso{code} -> "not iso ${code}"
    _ -> "other"
  }
}
fn main() {
  case Currency.parse("BTC") {
    Ok(_) -> io.print("parsed")
    Err(e) -> {
      io.print(describe(#.Bad{error: e}))
      h = #Holder{error: e}
      io.print(code(h.error))
    }
  }
  io.print(describe(#.Local{error: .Mine}))
  io.print(code(#CurrencyError.NotIso{code: "XYZ"}))
}
`, "bad: `BTC` is not an ISO 4217 currency: a custom currency names its minor digits, as Currency`BTC:2`\n"+
		"not iso BTC\nlocal: Mine\nnot iso XYZ\n")
}

// The same for a std opaque type, a std enum named through its variants, and
// a generic std struct at a type argument.
func TestImportAlias_StdOpaqueEnumAndGenericFields(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/comparable.{Ordering as Order}
  std/duration.{Duration as Span}
  std/io
  std/sets.{Set as Bag}
}
struct Holder {
  span: Span
  bag: Bag<Int>
  order: Order
}
enum Wrap {
  Timed {span: Span}
  Bagged {bag: Bag<Int>}
}
fn show(w: Wrap) -> String {
  case w {
    #.Timed{span} -> "timed ${span}"
    #.Bagged{bag} -> "bagged ${Bag.size(bag)}"
  }
}
fn flip(o: Order) -> Order {
  case o {
    Order.Less -> Order.Greater
    _ -> Order.Less
  }
}
fn main() {
  h = #Holder{span: Span.seconds(2), bag: %[1, 2], order: .Less}
  io.print("${h.span} ${Bag.size(h.bag)} ${flip(h.order)}")
  io.print(show(#.Timed{span: Span.seconds(3)}))
  io.print(show(#.Bagged{bag: %[1, 2, 3]}))
}
`, "2s 2 Greater\ntimed 3s\nbagged 3\n")
}

// A user type, enum and generic struct imported from another file under
// other names.
func TestImportAlias_UserTypesFromAnotherFile(t *testing.T) {
	verifyLambdaProgram(t, `import {
  shapes.{Box as Crate, Point as P, Shade as Tone}
  std/io
}
enum Problem {
  At {spot: P}
  Toned {tone: Tone}
  Boxed {held: Crate<Int>}
}
struct Holder {
  point: P
  crate: Crate<String>
}
fn describe(p: Problem) -> String {
  case p {
    #.At{spot} -> "at ${spot.x},${spot.y}"
    #.Toned{tone} -> case tone {
      .Light -> "light"
      #.Dark{depth} -> "dark ${depth}"
    }
    #.Boxed{held} -> "boxed ${held.item}"
  }
}
fn main() {
  io.print(describe(#.At{spot: #P{x: 1, y: 2}}))
  io.print(describe(#.Toned{tone: #Tone.Dark{depth: 3}}))
  io.print(describe(#.Boxed{held: #Crate{item: 4}}))
  h = #Holder{point: #P{x: 5, y: 6}, crate: #Crate{item: "s"}}
  io.print("${h.point.x} ${h.crate.item}")
}
`, "at 1,2\ndark 3\nboxed 4\n5 s\n", map[string]string{
		"shapes.nomi": `pub struct Point {
  x: Int
  y: Int
}
pub enum Shade {
  Light
  Dark {depth: Int}
}
pub struct Box<T> {
  item: T
}
`,
	})
}
