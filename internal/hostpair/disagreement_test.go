package hostpair

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
)

// The existing derivations of "this Nomi declaration ↔ this Go symbol" do not
// all agree, and the disagreements are the design constraint on any unified
// one. Each test below pins one that REMAINS, with the mechanism.
//
// They are DISAGREEMENTS rather than bugs-with-a-fix, because in both cases
// the two answers are legal for the consumer that produces them: a binding may
// be found under any member of an ORDERED CANDIDATE LIST (Pairing.Keys), and
// each derivation names a different member of it. Normalising one onto the
// other here would break the consumer whose spelling lost, which is why these
// tests report them instead. The key rules that must agree are in
// agree_implkey_test.go.

// ffirunKeysForSource is what ffirun derives from one file, flattened to
// `<kind> <key> -> <symbol>` lines and sorted.
func ffirunKeysForSource(t *testing.T, name, source string) []string {
	t.Helper()
	root := writeProject(t, map[string]string{name: source})
	packages, err := ffirun.DiscoverInScope(root, root)
	if err != nil {
		t.Fatalf("ffirun.DiscoverInScope: %v", err)
	}
	var out []string
	for _, p := range flattenFfirun(packages) {
		out = append(out, p.String())
	}
	sort.Strings(out)
	return out
}

func hostpairKeysForSource(t *testing.T, name, source string) ([]string, []Pairing) {
	t.Helper()
	root := writeProject(t, map[string]string{name: source})
	pairings := deriveProject(t, root, map[string]string{name: source})
	var out []string
	for _, p := range pairings {
		out = append(out, p.String())
	}
	sort.Strings(out)
	return out, pairings
}

// TestDisagree_ModulePrefixOnAnImplOwnedDeclaration is the disagreement that
// is LIVE today, in the shipped table.
//
// Mechanism: ffirun.externDeclKey returns `owner + "." + name` when the
// declaration sits in an impl block, DROPPING the module. irbuild.stdKey always
// keeps it. So `Regex.compile` in internal/stdlibbindings is
// `regex.Regex.compile` to the IR builder.
//
// Both are members of Pairing.Keys, the candidate list a binding may have
// been registered under, module-prefixed first.
//
// THE DERIVATION HALF IS OVER A FIXTURE, AND IT USED TO BE OVER std/regex.
// That module declares `host fn` now and names no Go symbol, so hostpair
// derives nothing from it. The fixture below is std/regex's shape: one module
// with an impl-owned binding and a top-level one, which is what the two
// branches of externDeclKey need to both fire.
//
// THE CONSEQUENCE HALF IS OVER THE SHIPPED TABLE, and it has to be —
// a fixture cannot say which spelling `internal/stdlibbindings` really ships,
// and that table is hand-written now rather than generated, so the spelling is
// a choice somebody could change without touching any derivation.
func TestDisagree_ModulePrefixOnAnImplOwnedDeclaration(t *testing.T) {
	const source = `gopkg "example.com/app/ffi" as ffi

pub opaque type Pat go ffi.Pattern

impl Pat {
  pub fn compile(source: String): Pat go ffi.Compile
}

fn helper(n: Int): Int go ffi.Helper
`
	_, pairings := hostpairKeysForSource(t, "patterns.nomi", source)

	var implOwned, topLevel int
	for _, p := range pairings {
		if p.Receiver == "" {
			topLevel++
			if p.BindingKey() != p.BuilderKey() {
				t.Errorf("top-level %s: the two derivations should agree, got BindingKey %q vs BuilderKey %q",
					p.Name, p.BindingKey(), p.BuilderKey())
			}
			continue
		}
		implOwned++
		if p.BindingKey() == p.BuilderKey() {
			t.Errorf("impl-owned %s.%s: expected the module prefix to differ, both derivations said %q",
				p.Receiver, p.Name, p.BindingKey())
		}
		if want := p.Module + "." + p.BindingKey(); p.BuilderKey() != want {
			t.Errorf("impl-owned %s.%s: BuilderKey %q is not BindingKey with the module prefixed (%q)",
				p.Receiver, p.Name, p.BuilderKey(), want)
		}
	}
	if implOwned == 0 || topLevel == 0 {
		t.Fatalf("need both shapes to show the disagreement is confined to one of them; got %d impl-owned, %d top-level", implOwned, topLevel)
	}

	// The live consequence, read off the shipped table rather than argued.
	shipped := map[string]bool{}
	for _, b := range stdlibbindings.Funcs() {
		shipped[b.Name] = true
	}
	if !shipped["Regex.compile"] {
		t.Error("internal/stdlibbindings no longer ships `Regex.compile`; re-derive this disagreement before trusting it")
	}
	if shipped["regex.Regex.compile"] {
		t.Error("internal/stdlibbindings now ships the module-prefixed spelling too; the disagreement has changed shape")
	}
	t.Logf("%d impl-owned pairing(s) disagree on the module prefix; %d top-level agree", implOwned, topLevel)
}

// TestDisagree_EntryScopedSecondKeyExistsOnlyInFfirun records the disagreement
// in the opposite direction: ffirun derives something irbuild.stdKey has no
// notion of.
//
// Mechanism: ffirun.entryScopedKey gives a module-qualified TOP-LEVEL
// declaration a second key — its bare name — because `nomi run app/ffi.nomi`
// loads ffi.nomi AS THE ENTRY and its own declarations are then keyed
// unqualified. stdKey has no analogue: it resolves the key at build time,
// where the module is known.
//
// So a unified derivation must carry this and expose it as OPTIONAL, which is
// what Pairing.EntryKey does. Collapsing it away would break `nomi run` on a
// non-main entry; requiring it would put a run-mode concept into stdKey's
// key.
func TestDisagree_EntryScopedSecondKeyExistsOnlyInFfirun(t *testing.T) {
	const source = `gopkg "example.com/app/ffi" as ffi

opaque type RawBox go ffi.Box

pub fn echo(s: String): String go ffi.Echo

impl RawBox {
  fn label(b: RawBox): String go ffi.Label
}
`
	root := writeProject(t, map[string]string{"main.nomi": "import ffi\n", "ffi.nomi": source})
	pairings := deriveProject(t, root, map[string]string{"ffi.nomi": source})

	byName := map[string]Pairing{}
	for _, p := range pairings {
		byName[p.Name] = p
	}
	for _, name := range []string{"RawBox", "echo", "label"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("hostpair did not derive %q from the fixture", name)
		}
	}
	if got := byName["echo"].EntryKey(); got != "echo" {
		t.Errorf("top-level func entry key is %q, want echo", got)
	}
	if got := byName["RawBox"].EntryKey(); got != "RawBox" {
		t.Errorf("top-level type entry key is %q, want RawBox", got)
	}
	if got := byName["label"].EntryKey(); got != "" {
		t.Errorf("impl-owned func entry key is %q, want empty — its key does not depend on which file is the entry", got)
	}
	if got := byName["echo"].BuilderKey(); got != "ffi.echo" {
		t.Errorf("BuilderKey is %q, want ffi.echo — the module-qualified key has no entry-scoped form", got)
	}
}

// TestKeysAreEvalsResolutionOrder is the claim the disagreements above rest
// on: for an IMPL-OWNED declaration, BindingKey and BuilderKey are not rival
// answers but two members of one ordered candidate list, and for a top-level
// one there is a single member and they coincide.
//
// If that is false then the disagreements are correctness bugs rather than
// consumer choices. Pinned so the conclusion is falsifiable rather than
// asserted.
//
// `bindingAt` is the index BindingKey occupies, and it is not always the last:
// an instantiated generic interface puts ffirun's key at index 2 of 4 —
// module-prefixed candidates first, then the interface-qualified bare one,
// which is what the wrapper registers.
//
// In the `top-level in a module` row, ffirun's entry-scoped key is not a
// fallback in the same list: the wrapper's nomiExternKey helper picks ONE of the two at
// run time from the entry path it was handed, so a top-level declaration has
// exactly one registered name. Folding the entry key into Keys would have had
// a wrapper register two names for one declaration, and a duplicate
// registration is rejected outright.
func TestKeysAreEvalsResolutionOrder(t *testing.T) {
	cases := []struct {
		name      string
		p         Pairing
		want      []string
		bindingAt int
	}{
		{
			name: "impl-owned, generic interface",
			p:    Pairing{Module: "calendar", Receiver: "NaiveDateTime", Interface: "Add<Duration, NaiveDateTime>", Name: "add"},
			want: []string{
				"calendar.NaiveDateTime.Add<Duration, NaiveDateTime>.add",
				"calendar.NaiveDateTime.add",
				"NaiveDateTime.Add<Duration, NaiveDateTime>.add",
				"NaiveDateTime.add",
			},
			bindingAt: 2,
		},
		{
			name:      "impl-owned, inherent",
			p:         Pairing{Module: "regex", Receiver: "Regex", Name: "compile"},
			want:      []string{"regex.Regex.compile", "Regex.compile"},
			bindingAt: 1,
		},
		{
			name:      "impl-owned, generic interface, entry file",
			p:         Pairing{Receiver: "Score", Interface: "Add<Bonus, Score>", Name: "add"},
			want:      []string{"Score.Add<Bonus, Score>.add", "Score.add"},
			bindingAt: 0,
		},
		{
			name:      "impl-owned in the entry file",
			p:         Pairing{Receiver: "RawBox", Name: "label"},
			want:      []string{"RawBox.label"},
			bindingAt: 0,
		},
		{
			name:      "top-level in a module",
			p:         Pairing{Module: "random", Name: "below_state"},
			want:      []string{"random.below_state"},
			bindingAt: 0,
		},
		{
			name:      "top-level in the entry",
			p:         Pairing{Name: "echo"},
			want:      []string{"echo"},
			bindingAt: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keys := tc.p.Keys()
			if strings.Join(keys, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("Keys() = %v, want %v", keys, tc.want)
			}
			if keys[0] != tc.p.BuilderKey() {
				t.Errorf("BuilderKey %q is not the first candidate %q", tc.p.BuilderKey(), keys[0])
			}
			if keys[tc.bindingAt] != tc.p.BindingKey() {
				t.Errorf("BindingKey %q is not candidate %d, %q", tc.p.BindingKey(), tc.bindingAt, keys[tc.bindingAt])
			}
			if tc.p.Receiver == "" && tc.p.BuilderKey() != tc.p.BindingKey() {
				t.Errorf("a top-level declaration must have one key, got BuilderKey %q and BindingKey %q", tc.p.BuilderKey(), tc.p.BindingKey())
			}
		})
	}
}
