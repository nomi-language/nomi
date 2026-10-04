package hostpair

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"github.com/nomi-language/nomi/std"
)

// THE HALF OF GENERATION THAT CAN STILL BE DONE.
//
// Every std declaration is a bare `host fn` / `host type`, so the SYMBOL half
// of an internal/stdlibbindings row is not in `.nomi` source and cannot be
// generated. Measured, and each measurement is a test rather than a claim:
//
//   - No std declaration names a Go symbol at all:
//     TestNoStdlibHostDeclarationNamesAGoSymbol (0 bound, 261 silent).
//   - The Go symbol is not a mangle of its Nomi key:
//     TestTheBoundGoSymbolIsNotAMangleOfItsNomiKey.
//   - Pairing by SIGNATURE against the adapter package is not a function:
//     34 of 66 FFI-shaped exports share a signature with another, worst group
//     9 members (every `func(d DateTime, n int64) DateTime` adder). See
//     TestSignaturePairingCannotDetermineTheGoSymbol.
//
// The KEY half is a different matter: it is exactly what hostpair derives from
// source, and this file is the check the doc comment on
// internal/stdlibbindings promises. It cannot write a row, but it can prove
// that no row is missing, misspelled or orphaned — which is what generation
// actually protected against.

// registrationReport is the derived answer, kept as data so a planted positive
// can drive the same rule over a fixture.
type registrationReport struct {
	// GoBacked names the std modules with at least one registered
	// declaration. DERIVED rather than listed: a fifth Go-backed module
	// appears here on its first row.
	GoBacked []string
	// Unregistered is a host declaration in a Go-backed module that no row
	// claims. This is the failure a missing or misspelled key produces.
	Unregistered []string
	// Orphans is a row no declaration claims — a rename on the Nomi side, or
	// the other half of a misspelling.
	Orphans []string
	// Registered counts declarations matched, and Silent counts host
	// declarations in modules with no Go backing at all. Both are asserted
	// non-trivial so a clean report cannot come from an empty walk.
	Registered int
	Silent     int
}

// checkRegistration is the rule: in a module where ANY host declaration is
// registered, EVERY host declaration must be, and no row may go unclaimed.
//
// "Go-backed" is derived from the table rather than from a list of four names,
// because the property worth holding is the absence of a PARTIAL module. A
// module whose Go implementation exists supplies all of it; a module with no
// rows is a pure-Nomi or rt-backed module and its declarations are somebody
// else's business (the Nomi bodies, or the `RtFuncs` rows).
func checkRegistration(byModule map[string][]hostDecl, funcs []stdlibbindings.Binding, types []stdlibbindings.TypeBinding) registrationReport {
	fnRows := map[string]bool{}
	for _, b := range funcs {
		fnRows[b.Name] = true
	}
	typeRows := map[string]bool{}
	for _, b := range types {
		typeRows[b.Name] = true
	}

	claimedFn := map[string]bool{}
	claimedType := map[string]bool{}
	var report registrationReport
	modules := make([]string, 0, len(byModule))
	for m := range byModule {
		modules = append(modules, m)
	}
	sort.Strings(modules)

	type pending struct {
		key  string
		kind Kind
	}
	unmatched := map[string][]pending{}

	for _, module := range modules {
		hits := 0
		var misses []pending
		for _, d := range byModule[module] {
			rows, claimed := fnRows, claimedFn
			if d.kind == KindType {
				rows, claimed = typeRows, claimedType
			}
			// The candidate list, most specific first. A row under ANY
			// member is a registration.
			hit := ""
			for _, k := range d.p.Keys() {
				if rows[k] {
					hit = k
					break
				}
			}
			if hit == "" {
				misses = append(misses, pending{d.key(), d.kind})
				continue
			}
			claimed[hit] = true
			hits++
		}
		if hits == 0 {
			report.Silent += len(byModule[module])
			continue
		}
		report.GoBacked = append(report.GoBacked, module)
		report.Registered += hits
		unmatched[module] = misses
	}
	for _, module := range report.GoBacked {
		for _, m := range unmatched[module] {
			report.Unregistered = append(report.Unregistered, m.kind.String()+" "+m.key)
		}
	}

	for _, b := range funcs {
		if !claimedFn[b.Name] {
			report.Orphans = append(report.Orphans, "func "+b.Name)
		}
	}
	for _, b := range types {
		if !claimedType[b.Name] {
			report.Orphans = append(report.Orphans, "type "+b.Name)
		}
	}
	sort.Strings(report.Unregistered)
	sort.Strings(report.Orphans)
	return report
}

// stdHostDeclsByModule enumerates every std module's host declarations, keyed
// by module name.
func stdHostDeclsByModule(t *testing.T) map[string][]hostDecl {
	t.Helper()
	out := map[string][]hostDecl{}
	for _, logicalPath := range stdModulePaths(t) {
		module := strings.TrimPrefix(logicalPath, "std/")
		src, ok := std.ReadFile(module)
		if !ok {
			t.Fatalf("std.ReadFile(%q) returned nothing for a module the tree walk found", module)
		}
		nodes, err := parser.Parse(lexer.Lex(string(src)))
		if err != nil {
			t.Fatalf("parsing std/%s: %v", module, err)
		}
		if decls := hostDecls(module, "", "", nodes); len(decls) > 0 {
			out[module] = decls
		}
	}
	return out
}

// TestEveryHostDeclarationInAGoBackedModuleIsRegistered is the check
// internal/stdlibbindings' package comment names.
//
// It exists because the failure a missing key produces is NOT what that
// comment used to claim. Measured by planting each shape and running the
// binary:
//
//	impl-owned func omitted     `line 5: cannot access field 'compile' on Unit`
//	impl-owned func misspelled   identical to omission
//	top-level func omitted      `analysis: type 'calendar.Date' has no member 'add_days'`
//	host type omitted           `Regex.compile: return payload ... names no type at this position`
//
// None fails at LOAD and none names the missing row. The first two are the
// silent-resolution failure the table's own header says holding function
// values removes: it removes it on the GO side, where a rename is a compile
// error, and leaves it whole on the NOMI side. So the key is the half that
// needs a check, and this is it.
func TestEveryHostDeclarationInAGoBackedModuleIsRegistered(t *testing.T) {
	report := checkRegistration(stdHostDeclsByModule(t), stdlibbindings.Funcs(), stdlibbindings.Types())

	for _, u := range report.Unregistered {
		t.Errorf("%s is a host declaration in a Go-backed std module with no row in "+
			"internal/stdlibbindings. Add one. Nothing else will tell you: the call fails "+
			"at RUN TIME with a message that does not name the declaration — `cannot access "+
			"field ... on Unit` for an impl-owned function, an analyzer complaint about a "+
			"DIFFERENT member for a top-level one, a boundary marshalling error for a type.", u)
	}
	for _, o := range report.Orphans {
		t.Errorf("internal/stdlibbindings row %s matches no host declaration in std. "+
			"Either the declaration was renamed and the row is now dead text, or the "+
			"row is misspelled and the real declaration is unregistered.", o)
	}

	// CONTROLS. A clean report from an empty walk would say nothing, so every
	// population this rule depends on is asserted non-trivial.
	if want := len(stdlibbindings.Funcs()) + len(stdlibbindings.Types()); report.Registered != want {
		t.Errorf("matched %d declaration(s) against %d row(s); with no orphans and no "+
			"unregistered declarations these must be equal, so the walk or the rule is wrong",
			report.Registered, want)
	}
	if report.Registered == 0 {
		t.Fatal("no declaration matched any row; the walk is broken and the zeros above are vacuous")
	}
	if report.Silent < 150 {
		t.Errorf("only %d host declaration(s) in modules with no Go backing; std has ~250, so "+
			"the walk is not reaching most of the stdlib and the Go-backed set below is a lower bound",
			report.Silent)
	}
	if got, want := strings.Join(report.GoBacked, " "), "calendar random regex"; got != want {
		t.Errorf("Go-backed std modules derived as %q, want %q. A NEW module here is fine — "+
			"update this control. A MISSING one means its rows stopped matching its declarations.",
			got, want)
	}
	t.Logf("%d row(s) across %d Go-backed module(s) (%s); %d host declaration(s) in the rest of std have no Go row",
		report.Registered, len(report.GoBacked), strings.Join(report.GoBacked, ", "), report.Silent)
}

// TestTheRegistrationCheckCanFail plants each way the table can go wrong and
// requires the rule to report it.
//
// Without this the check above is a passing test over a passing tree, and
// nothing shows it could ever have said no.
func TestTheRegistrationCheckCanFail(t *testing.T) {
	const source = `pub host type Regex

impl Regex {
  pub host fn compile(pattern: String): Result<Regex, String>
  pub host fn pattern(re: Regex): String
}

pub host fn escape(value: String): String
`
	nodes, err := parser.Parse(lexer.Lex(source))
	if err != nil {
		t.Fatalf("the fixture does not parse: %v", err)
	}
	decls := map[string][]hostDecl{"regex": hostDecls("regex", "", "", nodes)}
	if len(decls["regex"]) != 4 {
		t.Fatalf("the fixture yields %d declaration(s), want 4; a planted positive over the "+
			"wrong population proves nothing", len(decls["regex"]))
	}

	full := []stdlibbindings.Binding{
		{Name: "Regex.compile", Fn: func() {}},
		{Name: "Regex.pattern", Fn: func() {}},
		{Name: "regex.escape", Fn: func() {}},
	}
	fullTypes := []stdlibbindings.TypeBinding{{Name: "regex.Regex", Prototype: (*struct{})(nil)}}

	// THE NEGATIVE FIRST. If the complete table did not pass, every plant
	// below would "fail" for a reason unrelated to what it plants.
	if r := checkRegistration(decls, full, fullTypes); len(r.Unregistered) != 0 || len(r.Orphans) != 0 {
		t.Fatalf("the COMPLETE fixture table is reported as broken (unregistered %v, orphans %v); "+
			"the plants below would be measuring the rule's own bug",
			r.Unregistered, r.Orphans)
	}

	for _, tc := range []struct {
		name            string
		funcs           []stdlibbindings.Binding
		types           []stdlibbindings.TypeBinding
		wantUnreg       []string
		wantOrphan      []string
		wantNotGoBacked bool
	}{
		{
			name:      "an impl-owned func has no row",
			funcs:     full[1:],
			types:     fullTypes,
			wantUnreg: []string{"func regex.Regex.compile"},
		},
		{
			name:      "a top-level func has no row",
			funcs:     full[:2],
			types:     fullTypes,
			wantUnreg: []string{"func regex.escape"},
		},
		{
			name:      "the host type has no row",
			funcs:     full,
			types:     nil,
			wantUnreg: []string{"type regex.Regex"},
		},
		{
			name: "a row is misspelled: BOTH halves are reported",
			funcs: []stdlibbindings.Binding{
				{Name: "Regex.compil", Fn: func() {}},
				full[1], full[2],
			},
			types:      fullTypes,
			wantUnreg:  []string{"func regex.Regex.compile"},
			wantOrphan: []string{"func Regex.compil"},
		},
		{
			name:       "a row survives a declaration that was deleted",
			funcs:      append(append([]stdlibbindings.Binding{}, full...), stdlibbindings.Binding{Name: "Regex.gone", Fn: func() {}}),
			types:      fullTypes,
			wantOrphan: []string{"func Regex.gone"},
		},
		{
			// The rule must not report a module it has no rows for at all,
			// or every pure-Nomi std module would be a failure.
			name:            "no rows at all: the module is not Go-backed and nothing is reported",
			funcs:           nil,
			types:           nil,
			wantNotGoBacked: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := checkRegistration(decls, tc.funcs, tc.types)
			if tc.wantNotGoBacked {
				if len(r.GoBacked) != 0 {
					t.Errorf("GoBacked = %v, want empty", r.GoBacked)
				}
				if len(r.Unregistered) != 0 || len(r.Orphans) != 0 {
					t.Errorf("a module with no rows was reported: unregistered %v, orphans %v",
						r.Unregistered, r.Orphans)
				}
				if r.Silent != 4 {
					t.Errorf("Silent = %d, want 4; the declarations went nowhere", r.Silent)
				}
				return
			}
			if got := strings.Join(r.Unregistered, " | "); got != strings.Join(tc.wantUnreg, " | ") {
				t.Errorf("unregistered = %q, want %q", got, strings.Join(tc.wantUnreg, " | "))
			}
			if got := strings.Join(r.Orphans, " | "); got != strings.Join(tc.wantOrphan, " | ") {
				t.Errorf("orphans = %q, want %q", got, strings.Join(tc.wantOrphan, " | "))
			}
		})
	}
}
