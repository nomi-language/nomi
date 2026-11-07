package analysis_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// The derivable interfaces whose derived body dispatches on each component,
// so a component with no impl cannot take the derive. Debug is left out:
// every type has one.
var componentCheckedDerives = []string{"Equatable", "Hashable", "Comparable", "Display", "ToJson", "FromJson"}

// deriveImports is the import a derive of iface needs in a user file.
func deriveImports(iface string) string {
	switch iface {
	case "ToJson":
		return "import std::json.{Json, ToJson}\n\n"
	case "FromJson":
		return "import std::json.{FromJson, Json}\n\n"
	}
	return "import std::json.Json\n\n"
}

// deriveDiag answers the error whose message is msg, with its text and help.
func deriveDiag(errs []analysis.TypeError, msg string) (analysis.TypeError, bool) {
	for _, e := range errs {
		if e.Message == msg {
			return e, true
		}
	}
	return analysis.TypeError{}, false
}

// A derive over a field or payload of a std type that has no impl of the
// interface is refused at the derive, and the help says that only std can
// write that impl: the program can neither derive nor implement it there.
// `Json.ShapeError` has a `Display`, so Display is not among them.
func TestDeriveOverAStdComponentWithoutTheImplIsRefused(t *testing.T) {
	for _, iface := range componentCheckedDerives {
		if iface == "Display" {
			continue
		}
		t.Run(iface, func(t *testing.T) {
			src := deriveImports(iface) + fmt.Sprintf(`enum E {
  Shape Json.ShapeError
  Empty
}

derive %[1]s for E

struct S {
  shape: Json.ShapeError
}

derive %[1]s for S
`, iface)
			fa := buildWithStdlibForDerive(src)
			help := fmt.Sprintf("only std can implement `%[1]s` for `Json.ShapeError`; wrap it in a type of your own that implements `%[1]s`", iface)
			for _, via := range []string{"`derive " + iface + " for E`, field `Shape`", "`derive " + iface + " for S`, field `shape`"} {
				msg := fmt.Sprintf("no impl of `%s` for `Json.ShapeError` (required via %s)", iface, via)
				e, ok := deriveDiag(fa.TypeErrors, msg)
				if !ok {
					// The two derives share a bucket, so the second may be a
					// related location of the first.
					if strings.Contains(via, "for S") && hasRelated(fa.TypeErrors, "also required here via "+via) {
						continue
					}
					t.Fatalf("the front end admits a derive over a std component with no `%s`: want %q, got:\n  %s", iface, msg, errMsgs(fa.TypeErrors))
				}
				if len(e.Hints) != 1 || e.Hints[0] != help {
					t.Errorf("help = %q, want %q", e.Hints, help)
				}
			}
		})
	}
}

// hasRelated reports whether an error carries a related location whose
// message is msg.
func hasRelated(errs []analysis.TypeError, msg string) bool {
	for _, e := range errs {
		for _, r := range e.Related {
			if r.Message == msg {
				return true
			}
		}
	}
	return false
}

// A derive over a component of the program's own type keeps the help to
// derive or implement the interface on that type.
func TestDeriveOverAnOwnComponentWithoutTheImplSaysToDeriveIt(t *testing.T) {
	for _, iface := range componentCheckedDerives {
		t.Run(iface, func(t *testing.T) {
			src := deriveImports(iface) + fmt.Sprintf(`struct Inner {
  n: Int
}

enum E {
  A Inner
  Empty
}

derive %s for E

fn shape() -> Maybe<Json> {
  None
}
`, iface)
			fa := buildWithStdlibForDerive(src)
			msg := fmt.Sprintf("no impl of `%s` for `Inner` (required via `derive %s for E`, field `A`)", iface, iface)
			e, ok := deriveDiag(fa.TypeErrors, msg)
			if !ok {
				t.Fatalf("the front end admits a derive over a component with no `%s`: want %q, got:\n  %s", iface, msg, errMsgs(fa.TypeErrors))
			}
			help := fmt.Sprintf("add `derive %[1]s` to `Inner` or write an `impl %[1]s for Inner` block", iface)
			if len(e.Hints) != 1 || e.Hints[0] != help {
				t.Errorf("help = %q, want %q", e.Hints, help)
			}
		})
	}
}

// buildStdFileForDerive analyses src as if it were the std module file
// std/<name>.nomi, with the stdlib's impl index attached, and answers
// CheckDeriveBounds' errors. The missing-impl pass leaves out a demand
// whose every recording is std's own, so this is the only place a std
// derive over a component without the impl is reported.
func buildStdFileForDerive(t *testing.T, name, src string) []analysis.TypeError {
	t.Helper()
	nodes, perrs := parser.ParseWithRecovery(lexer.Lex(src))
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs)
	}
	nodes, _ = analysis.LowerDerives(nodes)
	lib := loadStdlibOnce()
	fa := analysis.BuildFileWithStdlibAtPath(nodes, lib.Primitives, lib.Modules, "", nil, "std/"+name+".nomi")
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	return analysis.CheckDeriveBounds(fa, nodes, fa.Program().ImplIndex)
}

// A derive in std over a component with no impl of the interface is an
// error at the component, with the help a std author can act on: std/jwt's
// `derive Equatable for JwtError` over a `Claims Json.ShapeError` payload
// passed the front end, and its derived bodies (`Hashable`, `Comparable`,
// `ToJson`) were declined by the builder. The component here is a struct
// of the same file with no impls at all, so every interface applies.
func TestStdDeriveOverAComponentWithoutTheImplIsRefused(t *testing.T) {
	for _, iface := range componentCheckedDerives {
		t.Run(iface, func(t *testing.T) {
			src := fmt.Sprintf(`pub struct Shape {
  path: List<String>
}

pub enum JwtError {
  Expired
  Claims Shape
}

derive %s for JwtError
`, iface)
			errs := buildStdFileForDerive(t, "jwtgap", src)
			msg := fmt.Sprintf("no impl of `%s` for `Shape` (required via `derive %s for JwtError`, field `Claims`)", iface, iface)
			e, ok := deriveDiag(errs, msg)
			if !ok {
				t.Fatalf("a std derive over a component with no `%s` is admitted: want %q, got:\n  %s", iface, msg, errMsgs(errs))
			}
			if e.File != "std/jwtgap.nomi" || e.Line != 7 || e.Col != 3 {
				t.Errorf("at %s:%d:%d, want std/jwtgap.nomi:7:3 (the variant)", e.File, e.Line, e.Col)
			}
			help := fmt.Sprintf("add `derive %[1]s` to `Shape` or write an `impl %[1]s for Shape` block", iface)
			if len(e.Hints) != 1 || e.Hints[0] != help {
				t.Errorf("help = %q, want %q", e.Hints, help)
			}
		})
	}
}

// The mirror: a std derive whose components all have the impl, through a
// container's type argument too, is accepted.
func TestStdDeriveOverComponentsWithTheImplIsAccepted(t *testing.T) {
	src := `pub enum JwtError {
  Expired
  Claims String
  Missing List<String>
}

derive Equatable for JwtError
`
	if errs := buildStdFileForDerive(t, "jwtok", src); len(errs) > 0 {
		t.Fatalf("a std derive over components with impls is refused:\n  %s", errMsgs(errs))
	}
}

// A std derive whose container holds a component without the impl is
// refused at the container's field.
func TestStdDeriveOverAContainerOfAComponentWithoutTheImplIsRefused(t *testing.T) {
	src := `import json.Json

pub struct Report {
  shapes: List<Json.ShapeError>
}

derive Hashable for Report
`
	errs := buildStdFileForDerive(t, "reportgap", src)
	msg := "no impl of `Hashable` for `Json.ShapeError` (required via `derive Hashable for Report`, field `shapes`)"
	if _, ok := deriveDiag(errs, msg); !ok {
		t.Fatalf("a std derive over a List of a component with no `Hashable` is admitted: want %q, got:\n  %s", msg, errMsgs(errs))
	}
}
