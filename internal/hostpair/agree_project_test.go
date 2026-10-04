package hostpair

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ffirun"
)

// The std adapters exercise three declaration shapes: a top-level `host fn`
// bound to a `gopkg` selector, a top-level `host type`, and an impl-owned
// `host fn`. ffirun handles four more that no adapter uses, and an agreement
// proof over the adapters alone would say nothing about them:
//
//   - the ENTRY file, whose declarations are unqualified (Module == "");
//   - a sibling module file, whose declarations carry a second entry-scoped key;
//   - an inline `go { }` function body, which has no package symbol at all;
//   - a `go { }` prelude `import`, which supplies an alias no `gopkg` declared.
//
// These fixtures cover all four, plus two `gopkg` handles in one file and an
// aliased extern `import`. Every one is compared against ffirun over the SAME
// temp project, so the comparison is between two readings of one input.
//
// The last two fixtures are a generic receiver, and two instantiations of
// one generic interface for one receiver. They are here rather than only in agree_implkey_test.go so that a
// regression in either half of ffirun.implOwnerKey is reported by the standing
// agreement check, not by a program failing to start.
var projectFixtures = []struct {
	name  string
	files map[string]string
}{
	{
		name: "entry file declarations are unqualified",
		files: map[string]string{
			"main.nomi": `gopkg "example.com/app/ffi" as ffi

opaque type RawBox go ffi.Box

fn echo(s: String): String go ffi.Echo
`,
		},
	},
	{
		name: "sibling module file carries an entry-scoped second key",
		files: map[string]string{
			"main.nomi": "import bindings\n",
			"bindings.nomi": `gopkg "example.com/app/ffi" as ffi

opaque type RawBox go ffi.Box

pub fn echo(s: String): String go ffi.Echo

fn tick(): Int go ffi.Tick
`,
		},
	},
	{
		name: "impl-owned host fn keys on its receiver",
		files: map[string]string{
			"main.nomi": "import store\n",
			"store.nomi": `gopkg "example.com/app/db" as db

opaque type RawConn go db.Conn

impl RawConn {
  fn open(path: String): Result<RawConn, String> go db.Open

  fn close(conn: RawConn): Unit go db.Close
}
`,
		},
	},
	{
		name: "two gopkg handles in one file",
		files: map[string]string{
			"main.nomi": `gopkg "example.com/app/one" as one
gopkg "example.com/app/two" as two

fn first(): Int go one.First

fn second(): Int go two.Second
`,
		},
	},
	{
		name: "test file declarations are unqualified",
		files: map[string]string{
			"main.nomi": "import lib\n",
			"lib.nomi": `gopkg "example.com/app/ffi" as ffi

pub fn echo(s: String): String go ffi.Echo
`,
			"lib_test.nomi": `gopkg "example.com/app/ffi" as ffi

fn probe(): Int go ffi.Probe
`,
		},
	},
	{
		name: "hyphenated import path aliases to a legal identifier",
		files: map[string]string{
			"main.nomi": `gopkg "example.com/app/url-tools" as urltools

fn parse(s: String): String go urltools.Parse
`,
		},
	},
	{
		name: "inline go body has no package symbol",
		files: map[string]string{
			"main.nomi": "go {\n  import gostrings \"strings\"\n}\n\nfn shout(s: String): String go {\n  return gostrings.ToUpper(s)\n}\n",
		},
	},
	{
		name: "go prelude import supplies an alias no gopkg declared",
		files: map[string]string{
			"main.nomi": "go {\n  import gostrings \"strings\"\n}\n\nfn upper(s: String): String go gostrings.ToUpper\n",
		},
	},
	{
		name: "inline go type body names a prelude-imported type",
		files: map[string]string{
			"main.nomi": "go {\n  import gotime \"time\"\n}\n\nopaque type Moment go { gotime.Time }\n\nfn now(): Moment go gotime.Now\n",
		},
	},
	{
		name: "generic receiver keys on its base name",
		files: map[string]string{
			"main.nomi": "import boxes\n",
			"boxes.nomi": `gopkg "example.com/app/ffi" as ffi

pub struct Box<T> {
  value: T
}

impl Box<T> {
  pub fn peek(b: Box<T>): Int go ffi.Peek

  pub fn origin(): Int go ffi.Origin
}
`,
		},
	},
	{
		name: "two instantiations of one generic interface for one receiver",
		files: map[string]string{
			"main.nomi": "import scoring\n",
			"scoring.nomi": `gopkg "example.com/app/ffi" as ffi

pub struct Score {
  points: Int
}

pub struct Bonus {
  points: Int
}

impl Add<Score, Score> for Score {
  fn add(a: Score, b: Score): Score go ffi.AddScores
}

impl Add<Bonus, Score> for Score {
  fn add(a: Score, b: Bonus): Score go ffi.AddBonus
}

impl Display for Score {
  fn to_string(s: Score): String go ffi.ScoreString
}
`,
		},
	},
}

func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// deriveProject runs hostpair over every `.nomi` file in the project, using
// ModuleNameForFile for the module segment — the same rule ffirun's
// discoverNomiFile applies.
func deriveProject(t *testing.T, root string, files map[string]string) []Pairing {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []Pairing
	for _, name := range names {
		if !strings.HasSuffix(name, ".nomi") {
			continue
		}
		full := filepath.Join(root, name)
		src, err := os.ReadFile(full)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		pairings, err := DeriveSource(full, ModuleNameForFile(root, full), src)
		if err != nil {
			t.Fatalf("DeriveSource %s: %v", name, err)
		}
		out = append(out, pairings...)
	}
	return out
}

// TestAgreesWithFfirunOverProjectShapes widens the agreement proof past the
// three co-located adapters onto the declaration shapes only ordinary FFI
// projects use.
func TestAgreesWithFfirunOverProjectShapes(t *testing.T) {
	for _, fixture := range projectFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			root := writeProject(t, fixture.files)
			packages, err := ffirun.DiscoverInScope(root, root)
			if err != nil {
				t.Fatalf("ffirun.DiscoverInScope: %v", err)
			}
			fromFfirun := flattenFfirun(packages)
			if len(fromFfirun) == 0 {
				t.Fatal("ffirun derived nothing from this fixture; the comparison would be vacuous")
			}
			fromHostpair := projectAll(deriveProject(t, root, fixture.files))
			if diffs := diffPairings(fromFfirun, fromHostpair); len(diffs) > 0 {
				t.Fatalf("hostpair disagrees with ffirun over %d pairing(s):\n%s",
					len(diffs), strings.Join(diffs, "\n"))
			}
			t.Logf("agreement over %d pairings", len(fromFfirun))
		})
	}
}

// TestProjectAgreementCoversEveryFfirunShape guards the fixture set itself.
//
// The agreement proof above is only worth what its inputs cover, and a fixture
// set that silently stopped producing (say) an entry-scoped key would keep
// passing while proving less. So assert the SHAPES are present, not just that
// the comparison agreed.
func TestProjectAgreementCoversEveryFfirunShape(t *testing.T) {
	seen := map[string]bool{}
	for _, fixture := range projectFixtures {
		root := writeProject(t, fixture.files)
		pairings := deriveProject(t, root, fixture.files)
		byReceiverName := map[string]map[string]bool{}
		for _, p := range pairings {
			if p.Kind == KindType {
				seen["host type"] = true
			}
			if p.Kind == KindFunc && p.Receiver == "" && p.Module == "" {
				seen["unqualified top-level func"] = true
			}
			if p.Kind == KindFunc && p.Receiver == "" && p.Module != "" {
				seen["module-qualified top-level func"] = true
			}
			if p.Receiver != "" {
				seen["impl-owned func"] = true
			}
			if p.EntryKey() != "" {
				seen["entry-scoped second key"] = true
			}
			if p.Module == "" && p.Kind == KindType {
				seen["unqualified host type"] = true
			}
			if p.Alias != "" && !strings.Contains(p.ImportPath, p.Alias) {
				seen["alias differs from last path segment"] = true
			}
			if p.Interface != "" {
				seen["instantiated generic interface"] = true
			}
			if p.Receiver != "" && p.Interface == "" && anySourceDeclares(fixture.files, "impl "+p.Receiver+"<") {
				// The fixture wrote `impl Box<T>` and the pairing came back
				// with the base name. Checked against the SOURCE because a
				// Pairing cannot say whether its receiver was written generic
				// — which is the whole point of dropping the binder.
				seen["generic receiver reduced to its base name"] = true
			}
			if p.Receiver != "" && p.Interface != "" {
				if byReceiverName[p.Receiver+"."+p.Name] == nil {
					byReceiverName[p.Receiver+"."+p.Name] = map[string]bool{}
				}
				byReceiverName[p.Receiver+"."+p.Name][p.Interface] = true
			}
		}
		for _, ifaces := range byReceiverName {
			if len(ifaces) > 1 {
				seen["two instantiations of one interface for one receiver"] = true
			}
		}
	}
	for _, shape := range []string{
		"host type",
		"unqualified top-level func",
		"module-qualified top-level func",
		"impl-owned func",
		"entry-scoped second key",
		"unqualified host type",
		"alias differs from last path segment",
		"instantiated generic interface",
		"generic receiver reduced to its base name",
		"two instantiations of one interface for one receiver",
	} {
		if !seen[shape] {
			t.Errorf("no fixture produces the %q shape, so the agreement proof does not cover it", shape)
		}
	}
}

func anySourceDeclares(files map[string]string, needle string) bool {
	for _, body := range files {
		if strings.Contains(body, needle) {
			return true
		}
	}
	return false
}

// TestProjectAgreementCatchesAnImplKeyRegression is the planted positive for
// the generic-receiver and generic-interface agreements.
//
// Each plant is a wrong key rule (dropping the interface's instantiation, or
// keeping the receiver's type parameters), applied to hostpair's side of a fixture that
// agrees with ffirun, and the check must report every affected pairing. That
// is what makes the check's pass mean something.
//
// `want` counts DIFF LINES, and diffPairings emits two per moved pairing —
// "only in ffirun" for the key that vanished and "only in hostpair" for the
// one that appeared. So two perturbed declarations are four lines. The exact
// count is asserted rather than `> 0` because a plant that perturbs more
// pairings than intended would otherwise read as a pass; the `impl Display for
// Score` binding in the first fixture is the control, and it must NOT move
// (Display is not instantiated, so it never carried an interface segment).
func TestProjectAgreementCatchesAnImplKeyRegression(t *testing.T) {
	plants := []struct {
		name    string
		fixture string
		mutate  func(Pairing) Pairing
		want    int
	}{
		{
			// v.Interface unread: both `add` declarations key on `Score.add`
			// and one of the two registrations fails at load.
			name:    "interface instantiation dropped",
			fixture: "two instantiations of one generic interface for one receiver",
			mutate:  func(p Pairing) Pairing { p.Interface = ""; return p },
			want:    4,
		},
		{
			// v.Receiver.TypeString(): the key is `Box<T>.peek` and no lookup
			// ever forms it.
			name:    "receiver keeps its type parameters",
			fixture: "generic receiver keys on its base name",
			mutate: func(p Pairing) Pairing {
				if p.Receiver == "Box" {
					p.Receiver = "Box<T>"
				}
				return p
			},
			want: 4,
		},
	}
	for _, plant := range plants {
		t.Run(plant.name, func(t *testing.T) {
			files := fixtureFiles(t, plant.fixture)
			root := writeProject(t, files)
			packages, err := ffirun.DiscoverInScope(root, root)
			if err != nil {
				t.Fatalf("ffirun.DiscoverInScope: %v", err)
			}
			fromFfirun := flattenFfirun(packages)
			derived := deriveProject(t, root, files)
			if diffs := diffPairings(fromFfirun, projectAll(derived)); len(diffs) > 0 {
				t.Fatalf("the unperturbed fixture already disagrees, so the plant proves nothing:\n%s",
					strings.Join(diffs, "\n"))
			}
			perturbed := make([]Pairing, 0, len(derived))
			for _, p := range derived {
				perturbed = append(perturbed, plant.mutate(p))
			}
			diffs := diffPairings(fromFfirun, projectAll(perturbed))
			if len(diffs) != plant.want {
				t.Fatalf("the check reported %d diff(s), want %d:\n%s",
					len(diffs), plant.want, strings.Join(diffs, "\n"))
			}
			t.Logf("reported:\n%s", strings.Join(diffs, "\n"))
		})
	}
}

func fixtureFiles(t *testing.T, name string) map[string]string {
	t.Helper()
	for _, fixture := range projectFixtures {
		if fixture.name == name {
			return fixture.files
		}
	}
	t.Fatalf("no project fixture named %q", name)
	return nil
}
