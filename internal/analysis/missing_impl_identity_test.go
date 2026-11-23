package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// implIdentityErrs is the whole analyzer: build sweep, CheckTypes, then
// FinalizeCoherence. The missing-impl diagnostic exists only once a project
// impl index has been built and every recording has fired, so a test about
// what the analyzer catches statically has to run all three.
func implIdentityErrs(t *testing.T, src string) []string {
	t.Helper()
	fa, nodes := implIdentityFA(t, src)
	var msgs []string
	for _, e := range fa.TypeErrors {
		msgs = append(msgs, e.Message)
	}
	for _, e := range analysis.CheckTypes(fa, nodes) {
		msgs = append(msgs, e.Message)
	}
	for _, e := range analysis.FinalizeCoherence(fa) {
		msgs = append(msgs, e.Message)
	}
	return msgs
}

func implIdentityFA(t *testing.T, src string) (*analysis.FileAnalysis, []ast.Node) {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "main.nomi"), []byte(src), 0644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	loader := func(root string, mod []string) ([]ast.Node, error) {
		data, err := os.ReadFile(filepath.Join(root, filepath.Join(mod...)) + ".nomi")
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		return nodes, nil
	}
	lib := loadStd()
	fa, _, _ := analysis.BuildProjectWithCache(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	return fa, entryNodes
}

func hasMissingImpl(msgs []string, iface, typeName string) bool {
	want := "no impl of `" + iface + "` for `" + typeName + "`"
	for _, m := range msgs {
		if strings.Contains(m, want) {
			return true
		}
	}
	return false
}

// TestMissingImpl_TheOnlyVariableIsTheTypesName is the instrument that found
// the defect, kept verbatim because it isolates the cause to one character
// class: the two programs are identical except for the name of the enum.
//
// `std/calendar.nomi` carries `derive Equatable for Error`. With `Impls` keyed by
// BARE type name, a user enum named `Error` borrowed that conformance and the
// program was ACCEPTED with no impl of its own. An enum named `Zonk` was
// correctly refused.
//
// Both enums hold a function, so neither has equality, and neither meets
// Equatable without an impl (spec §5 *Equality bounds*).
//
// Both rows must produce the diagnostic. A row that stops producing one is
// a regression whichever direction it moves in, so neither is written as
// "expect no error".
func TestMissingImpl_TheOnlyVariableIsTheTypesName(t *testing.T) {
	const tmpl = `import { std::io }

pub enum %[1]s
  Bad String
  Run (Int) -> Int
end

fn mk(s: String) -> Result<Int, %[1]s>
  Err(%[1]s.Bad(s))
end

fn main()
  io.print("equal: ${Equatable.equal(mk("a"), mk("b"))}")
end
`
	for _, name := range []string{"Error", "Zonk"} {
		src := strings.ReplaceAll(tmpl, "%[1]s", name)
		msgs := implIdentityErrs(t, src)
		if !hasMissingImpl(msgs, "Equatable", name) {
			t.Errorf("enum %s with no Equatable impl was ACCEPTED; diagnostics: %v", name, msgs)
		}
	}
}

// TestMissingImpl_AnEntryImplIsNotMistakenForAnotherModules is the fail-safe
// direction, and it is the one a too-eager identity check breaks. The user's
// own `impl Equatable for Error` is a real conformance; rejecting it because
// std/calendar also declares an `Error` would turn a correct program into an
// error, which is strictly worse than the bug being fixed.
func TestMissingImpl_AnEntryImplIsNotMistakenForAnotherModules(t *testing.T) {
	const src = `import
    std::io
end

pub enum Error
  Bad String
end

impl Equatable for Error
  fn equal(_a: Error, _b: Error) -> Bool
      True
  end
end

fn main()
  io.print("equal: ${Equatable.equal(Error.Bad("a"), Error.Bad("b"))}")
end
`
	for _, m := range implIdentityErrs(t, src) {
		if strings.Contains(m, "no impl of `Equatable`") {
			t.Fatalf("an entry-declared impl was read as another module's: %s", m)
		}
	}
}

// TestMissingImpl_StdlibsOwnDemandsAreNotBlamedOnTheUser pins the reason the
// check filters per RECORDING rather than judging the bucket.
//
// `ImplManifest["Equatable"]["Error"]` holds the demands the stdlib raised
// about its own `Error` types, which are supplied, and the one the user raised
// about theirs, which is not. A whole-bucket rule has no answer — the origins
// disagree — so it either accepts the borrow or reports the stdlib's own
// correct demands against the user. The diagnostic must name
// the user's position and nothing in std.
func TestMissingImpl_StdlibsOwnDemandsAreNotBlamedOnTheUser(t *testing.T) {
	const src = `import
  std::io
  std::calendar.{DateTime}
end

pub enum Error
  Bad String
  Run (Int) -> Int
end

fn mk(s: String) -> Result<Int, Error>
    Err(Error.Bad(s))
end

fn main()
  d = DateTime"2026-01-01T12:00:00[America/New_York]"
  io.print("x: ${d}")
  io.print("equal: ${Equatable.equal(mk("a"), mk("b"))}")
end
`
	msgs := implIdentityErrs(t, src)
	if !hasMissingImpl(msgs, "Equatable", "Error") {
		t.Fatalf("the user's un-implemented Error was accepted; diagnostics: %v", msgs)
	}
	for _, m := range msgs {
		if strings.Contains(m, "calendar.nomi") || strings.Contains(m, "random.nomi") {
			t.Errorf("a stdlib position was reported to the user: %s", m)
		}
	}
}

// TestImplIdentity_EveryKeyCarriesAnOrigin is the mutation net. Keying with
// `Origin: ""` — the lattice bottom — would make `implSuppliedByIdentity`
// answer "cannot judge" for everything and silently restore the old
// behaviour while every behavioural test above still passed, because those
// tests also pass under a build that never populated the index at all.
func TestImplIdentity_EveryKeyCarriesAnOrigin(t *testing.T) {
	fa, _ := implIdentityFA(t, "fn main() end\n")
	idx := fa.Program().ImplIndex
	if idx == nil {
		t.Fatal("nil ProjectImpls")
	}
	if len(idx.ImplsByIdentity) == 0 {
		t.Fatal("ImplsByIdentity is EMPTY, so the identity check cannot ever reject")
	}
	for k := range idx.ImplsByIdentity {
		if k.Origin == analysis.OriginUnresolved {
			t.Errorf("key %+v carries no origin, which reads as the lattice bottom", k)
		}
		if k.Type == "" || k.Iface == "" {
			t.Errorf("key %+v is incomplete", k)
		}
	}
}

// TestImplIdentity_TheStdlibErrorsAreSeparateConformances is the defect
// stated as data rather than as behaviour: under a bare-name key the std
// types called `Error` share one `Equatable` slot, and under an identity key
// each is its own entry.
func TestImplIdentity_TheStdlibErrorsAreSeparateConformances(t *testing.T) {
	fa, _ := implIdentityFA(t, "fn main() end\n")
	idx := fa.Program().ImplIndex
	if idx == nil {
		t.Fatal("nil ProjectImpls")
	}
	if !idx.Impls["Error"]["Equatable"] {
		t.Fatal("the bare-name table no longer claims Equatable for `Error`; " +
			"if the stdlib Errors dropped their derives this test is measuring nothing")
	}
	var with, without []string
	for _, origin := range []string{"std/calendar", "std/random"} {
		if idx.ImplsByIdentity[analysis.ImplIdentityKey{Origin: origin, Type: "Error", Iface: "Equatable"}] {
			with = append(with, origin)
		} else {
			without = append(without, origin)
		}
	}
	sort.Strings(with)
	// Both derive Equatable, because the diagnostic this check produces
	// names a stdlib type the user cannot add a `derive` to. The property
	// under test is that each one is its OWN entry, so the count is two and
	// not "one, shared".
	if len(with) != 2 {
		t.Errorf("Equatable for `Error` resolved per-declaration = %v, missing %v; want both as separate entries", with, without)
	}
}

// TestImplIdentity_EveryStdConformanceIsJudgedByIdentity asserts that the
// identity index judges every (type, iface) pair the stdlib's bare table
// claims, the impls of generic interfaces included (`impl Add<Days, Date>
// for Date`, `impl CheckedLiteral<Display, Url> for Url`, Codepoint's
// synthesized `TryFrom<Int64>`): each is keyed by its receiver's identity
// and the interface's bare name, the name a lookup asks by. A pair listed
// here is one some supplier of which has a receiver whose origin did not
// resolve, so a same-named type of another file would borrow its impl.
func TestImplIdentity_EveryStdConformanceIsJudgedByIdentity(t *testing.T) {
	fa, _ := implIdentityFA(t, "fn main() end\n")
	idx := fa.Program().ImplIndex
	if idx == nil {
		t.Fatal("nil ProjectImpls")
	}
	var fellBack []string
	for ty, ifaces := range idx.Impls {
		for iface := range ifaces {
			if !idx.ImplIdentityCovers(ty, iface) {
				fellBack = append(fellBack, ty+"/"+iface)
			}
		}
	}
	sort.Strings(fellBack)
	if len(fellBack) != 0 {
		t.Errorf("std conformances the identity index cannot judge: %s", strings.Join(fellBack, " "))
	}
	key := analysis.ImplIdentityKey{Origin: "std/calendar", Type: "Date", Iface: "Add"}
	if !idx.ImplsByIdentity[key] {
		t.Errorf("std/calendar's `impl Add<Days, Date> for Date` is not keyed %+v", key)
	}
}

// An impl of a generic interface is its own receiver's, as any impl is: a
// type that shares a name with another file's or std's type that has one
// gets none of it. Each program is rejected, at the use, with the error a
// type with no such impl draws.
func TestMissingImpl_AGenericInterfacesImplIsItsReceiversOwn(t *testing.T) {
	const other = `import
    std::literals.{CheckedLiteral, Fragment, LiteralError}
end

pub enum Weekday
    Monday
    Tuesday
end

impl TryFrom<Int> for Weekday
    fn try_from(value: Int) -> Option<Weekday>
        _ = value
        Some(Weekday.Monday)
    end
end

pub struct Money
    cents: Int
end

impl Add<Money, Money> for Money
    fn add(lhs: Money, rhs: Money) -> Money
        Money{cents: lhs.cents + rhs.cents}
    end
end

impl Multiply<Int, Int> for Money
    fn multiply(lhs: Money, rhs: Int) -> Int
        lhs.cents * rhs
    end
end

pub struct Tag
    text: String
end

impl CheckedLiteral<String, Tag> for Tag
    fn check(statics: List<String>) -> Result<Unit, LiteralError>
        _ = statics
        Ok(Unit)
    end

    fn build(fragments: List<Fragment<String>>) -> Tag
        _ = fragments
        Tag{text: "lib"}
    end
end

pub interface Scale<F>
    fn scale(x: self, by: F) -> self
end

impl Scale<Int> for Money
    fn scale(x: Money, by: Int) -> Money
        Money{cents: x.cents * by}
    end
end
`
	const decls = `enum Weekday
    Monday
    Tuesday
end

struct Money
    cents: Int
end

struct Tag
    text: String
end

pub struct Duration
    seconds: Int
end

fn keep(w: lib.Weekday, m: lib.Money, t: lib.Tag) -> (lib.Weekday, lib.Money, lib.Tag)
    (w, m, t)
end
`
	for _, c := range []struct{ name, body, want string }{
		{"a TryFrom call", `_ = Weekday.try_from(2)`, "type 'Weekday' has no member 'try_from'"},
		{"a TryFrom bound", `day: Option<Weekday> = load(2)
    _ = day`, "Weekday does not implement TryFrom<Int> (required by `where T: TryFrom<Int>`)"},
		{"an operator", `_ = Money{cents: 1} + Money{cents: 2}`, "no impl of `Add` for `"},
		{"an operator whose arguments are not the receiver", `_ = Money{cents: 1} * 2`, "`*` needs two values of one type, and these are Money and Int"},
		{"a checked literal", `_ = Tag"hello"`, "type 'Tag' has no typed-literal handler"},
		{"a user generic interface's bound", `_ = twice(Money{cents: 3})`, "Money does not implement Scale<Int> (required by `where T: Scale<Int>`)"},
		{"std's operator", `_ = Duration{seconds: 1} + Duration{seconds: 2}`, "no impl of `Add` for `"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := `import
    lib
    lib.{Scale}
end

` + decls + `
fn load<T>(n: Int) -> Option<T> where T: TryFrom<Int>
    T.try_from(n)
end

fn twice<T>(x: T) -> T where T: Scale<Int>
    T.scale(x, 2)
end

fn main()
    ` + c.body + `
end
`
			msgs := implIdentityProjectErrs(t, src, map[string]string{"lib.nomi": other})
			found := false
			for _, m := range msgs {
				if strings.Contains(m, c.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("the program was not rejected with %q; diagnostics: %v", c.want, msgs)
			}
		})
	}
}

// A user type that shares a name with a std type implementing an operator
// interface keeps its own inherent function of the operator's name:
// `Duration.add` here is the struct's own `add`, not std/duration's `Add`.
func TestImplIdentity_AnInherentOperatorNameIsNotStdsImpl(t *testing.T) {
	src := `pub struct Duration
    seconds: Int
end

impl Duration
    pub fn add(a: Duration, b: Duration) -> Duration
        Duration{seconds: a.seconds + b.seconds}
    end
end

fn main()
    total = Duration.add(Duration{seconds: 1}, Duration{seconds: 2})
    _ = total.seconds
end
`
	if msgs := implIdentityErrs(t, src); len(msgs) != 0 {
		t.Fatalf("the user's own Duration.add should check, got %q", msgs)
	}
}

// A typed literal's one-handler checks ask about the tag's own type. Another
// file's `Shout`, with an `impl Literal` or an interface whose function is
// named `from_fragments`, gives this file's `Shout` neither: its
// `CheckedLiteral` is not a second handler, and its `Literal` has no rival.
// Asked by name, the first was "implements both `Literal` and
// `CheckedLiteral`" and the second "is ambiguous", naming the other file's
// line. A rival this `Shout` writes is still reported, at its own line,
// where by name the two files' `Build` impls hid each other and the builder
// declined the literal.
func TestImplIdentity_ALiteralHandlerIsJudgedOnItsOwnTag(t *testing.T) {
	const loud = `import std::literals.{Fragment, Literal}

pub type Shout

impl Literal<Int, String> for Shout
    fn from_fragments(fragments: List<Fragment<Int>>) -> String
        "${Iter.count(fragments)}"
    end
end

pub interface Build
    fn from_fragments(fragments: List<Fragment<String>>) -> Int
end

pub type Quiet

impl Build for Quiet
    fn from_fragments(fragments: List<Fragment<String>>) -> Int
        Iter.count(fragments)
    end
end
`
	const checked = `import
    std::literals.{CheckedLiteral, Fragment, LiteralError}
    lib
end

type Shout

impl CheckedLiteral<Int, String> for Shout
    fn check(statics: List<String>) -> Result<Unit, LiteralError>
        _ = statics
        Ok(Unit)
    end

    fn build(fragments: List<Fragment<Int>>) -> String
        "${Iter.count(fragments)}"
    end
end

fn main()
    _ = Shout"a ${1} b"
    _ = lib.Shout
end
`
	const literal = `import
    std::literals.{Fragment, Literal}
    lib
end

type Quiet

impl Literal<Int, String> for Quiet
    fn from_fragments(fragments: List<Fragment<Int>>) -> String
        "${Iter.count(fragments)}"
    end
end
RIVAL
fn main()
    _ = Quiet"a ${1} b"
    _ = lib.Quiet
end
`
	const rival = `
impl lib.Build for Quiet
    fn from_fragments(fragments: List<Fragment<String>>) -> Int
        Iter.count(fragments)
    end
end
`
	files := map[string]string{"lib.nomi": loud}
	for name, src := range map[string]string{
		"a CheckedLiteral beside another file's Literal": checked,
		"a Literal beside another file's from_fragments": strings.Replace(literal, "RIVAL", "", 1),
	} {
		if msgs := implIdentityProjectErrs(t, src, files); len(msgs) != 0 {
			t.Errorf("%s: the program should check, got %q", name, msgs)
		}
	}
	msgs := implIdentityProjectErrs(t, strings.Replace(literal, "RIVAL", rival, 1), files)
	want := "typed literal 'Quiet\"...\"' is ambiguous: `impl Build for Quiet` also declares `from_fragments`, at line 15,"
	found := false
	for _, m := range msgs {
		if strings.HasPrefix(m, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("the rival this file writes is not reported; want %q in %q", want, msgs)
	}
}

// implIdentityProjectErrs is implIdentityErrs over a project whose entry is
// main.nomi beside files, with each coherence error's hints after it.
func implIdentityProjectErrs(t *testing.T, src string, files map[string]string) []string {
	t.Helper()
	fa, entryNodes := implIdentityProject(t, src, files)
	var msgs []string
	for _, e := range fa.TypeErrors {
		msgs = append(msgs, e.Message)
	}
	for _, e := range analysis.CheckTypes(fa, entryNodes) {
		msgs = append(msgs, e.Message)
	}
	for _, e := range analysis.FinalizeCoherence(fa) {
		msgs = append(msgs, e.Message+" | "+strings.Join(e.Hints, " | "))
	}
	return msgs
}

// implIdentityProject builds a project whose entry is main.nomi beside
// files, and answers the entry's analysis and nodes, unchecked.
func implIdentityProject(t *testing.T, src string, files map[string]string) (*analysis.FileAnalysis, []ast.Node) {
	t.Helper()
	tmp := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(text), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "main.nomi"), []byte(src), 0644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	loader := func(root string, mod []string) ([]ast.Node, error) {
		data, err := os.ReadFile(filepath.Join(root, filepath.Join(mod...)) + ".nomi")
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		return nodes, nil
	}
	lib := loadStd()
	fa, _, _ := analysis.BuildProjectWithCache(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	return fa, entryNodes
}

// An impl for another file's type, written through its module
// (`impl Html for users.User`), is that type's impl even when the
// implementing file declares a `User` of its own: the receiver's identity is
// the module the header names, not the name in the file's scope. With an
// impl for each `User` the program checks, and neither impl collides with
// the other.
func TestMissingImpl_AQualifiedReceiverIsTheOtherFilesType(t *testing.T) {
	users := map[string]string{"users.nomi": "pub struct User\n  name: String\nend\n"}
	const both = `import
    std::io  users
end

interface Html
  fn to_html(value: self) -> String
end

struct User
  id: Int
end

impl Html for users.User
  fn to_html(u: users.User) -> String
      u.name
  end
end

impl Html for User
  fn to_html(u: User) -> String
      "${u.id}"
  end
end

fn main()
  io.print(Html.to_html(users.User{name: "ann"}))
  io.print(Html.to_html(User{id: 1}))
end
`
	if msgs := implIdentityProjectErrs(t, both, users); len(msgs) != 0 {
		t.Fatalf("an impl for each User was rejected: %v", msgs)
	}

	// Only the entry's User has the impl: the missing one names the other
	// file's type by its module, since a bare `User` would name the type
	// that has it.
	const one = `import
    std::io  users
end

interface Html
  fn to_html(value: self) -> String
end

struct User
  id: Int
end

impl Html for User
  fn to_html(u: User) -> String
      "${u.id}"
  end
end

fn main()
  io.print(Html.to_html(users.User{name: "ann"}))
end
`
	msgs := implIdentityProjectErrs(t, one, users)
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "no impl of `Html` for `users.User`") &&
			strings.Contains(m, "write an `impl Html for users.User` block") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the missing impl for users.User was not reported by its qualified name: %v", msgs)
	}
}
