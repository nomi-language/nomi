package irbuild

import (
	"testing"
)

// Construction through a struct-shaped or embedded variant takes the
// construction forms of the variant's shape (analysis/variant_construction.go):
// the record call form beside the brace form for a struct shape, inline or
// embedded, and the distinct's call form for an embedded distinct.
//
// The TestIREmbedCtor programs run on the VM and must print the expected
// output.

func TestIREmbedCtor_EmbeddedStructCallForms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Circle {
  radius: Float = 3.14
}

enum Shape {
  embeds Circle
  Dot Float
}

fn area(s: Shape): Float {
  case s {
    Shape.Circle{radius} -> radius
    Shape.Dot(d) -> d
  }
}

fn main() {
  a = Shape.Circle({radius: 2.0})
  b = Shape.Circle({})
  c = {radius: 2.5} |> Shape.Circle()
  e = Shape.Circle{radius: 1.0}
  io.inspect(a)
  io.inspect(b)
  io.print(area(c))
  io.print(area(e))
}
`, "Circle{radius: 2.0}\nCircle{radius: 3.14}\n2.5\n1.0\n")
}

func TestIREmbedCtor_InlineStructVariantCallForms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

enum Shape {
  Rectangle {width: Float = 2.5, height: Float}
  Dot Float
}

fn area(s: Shape): Float {
  case s {
    Shape.Rectangle{width, height} -> width * height
    Shape.Dot(d) -> d
  }
}

fn main() {
  a = Shape.Rectangle({width: 4.0, height: 5.0})
  b = Shape.Rectangle({height: 2.0})
  c = {width: 1.0, height: 3.0} |> Shape.Rectangle()
  e: Shape = .Rectangle({height: 4.0})
  io.inspect(a)
  io.inspect(b)
  io.print(area(c))
  io.print(area(e))
}
`, "Rectangle{width: 4.0, height: 5.0}\nRectangle{width: 2.5, height: 2.0}\n3.0\n10.0\n")
}

func TestIREmbedCtor_EmbeddedMarker(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Live {
  since: Int
}

type Expired

enum Session {
  embeds Live
  embeds Expired
}

fn label(s: Session): String {
  case s {
    Session.Live{since} -> "live since ${since}"
    Session.Expired -> "expired"
  }
}

fn main() {
  gone = Session.Expired
  io.inspect(gone)
  io.print(label(gone))
  io.print(label(Session.Live({since: 3})))
}
`, "Expired\nexpired\nlive since 3\n")
}
