package ffxadapters

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/hostgen"
	"github.com/nomi-language/nomi/internal/hostgen/fixture"
	"github.com/nomi-language/nomi/rt"
)

func TestGeneratedFileIsCurrent(t *testing.T) {
	want, err := hostgen.Generate(fixture.Table())
	if err != nil {
		t.Fatalf("generation fails: %v", err)
	}
	got, err := os.ReadFile("adapters_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("adapters_gen.go is stale; run `go generate ./internal/hostgen/fixture/ffxadapters` from the repository root")
	}
}

// THE FIXTURE PARITY SUITE: every generated adapter for the fixture's
// bindings against the outcomes a reflective marshaller produced
// for the same argument sets (testdata/marshaller.golden, 58 argument sets).
// The five for ffx.query and ffx.sizes, the Go-map bindings, were recorded
// from the adapters when those bindings were added, after the marshaller was
// gone, and checked by hand.
//
// A marshaller error is recorded as `error <text>`.
// The adapters agreed with every line when it was taken.
//
// An outcome's text (outcome_test.go) is stricter than rt.Equal: dynamic
// types, qualified record names, variants, fields by name, Float by bits, a
// failure by its text. Regenerating the recording is not a way to make this
// pass: the marshaller no longer exists. NOMI_UPDATE_HOST_GOLDEN rewrites it
// from the adapters, for a deliberate change of behaviour.

const goldenPath = "testdata/marshaller.golden"

// fnRef is a function value operand. An adapter receives its name, and the
// Invoker below answers it.
type fnRef string

func i(n int64) any  { return n }
func s(v string) any { return v }
func strs(v ...string) any {
	items := make([]any, len(v))
	for k, x := range v {
		items[k] = x
	}
	return rtList(items...)
}
func point(x, y int64) any { return rb.Struct("ffx.Point", "x", x, "y", y) }
func some(v any) any       { return rb.Variant("maybe.Maybe", "Some", v) }
func none() any            { return rb.Variant("maybe.Maybe", "None") }
func ok(v any) any         { return rb.Variant("results.Result", "Ok", v) }
func fail(msg string) any  { return rb.Variant("results.Result", "Err", msg) }
func instant(n int64) any  { return rb.Distinct("instant.Instant", n) }
func duration(n int64) any { return rb.Distinct("duration.Duration", n) }
func tagged(label string, small, wide int64, ratio float64, at, wait int64, raw string, near any) any {
	return rb.Struct("ffx.Tagged",
		"label", label, "x", int64(1), "y", int64(2), "small", small, "wide", wide, "ratio", ratio,
		"at", instant(at), "wait", duration(wait), "raw", rt.Bytes(raw), "tags", strs("a", "b"), "near", near)
}

// The callbacks, as Go functions over rt values for the adapters' Invoker.
// Each rt function value is its name. A record a callback answers is built
// by rb, a producer other than the adapter.
var callbacks = map[string]func([]rt.Value) (rt.Value, error){
	"double": func(a []rt.Value) (rt.Value, error) { return a[0].(int64) * 2, nil },
	"ordered": func(a []rt.Value) (rt.Value, error) {
		p := a[0].(*rt.Record)
		x, _ := p.FieldNamed("x")
		y, _ := p.FieldNamed("y")
		return x.(int64) <= y.(int64), nil
	},
	"length": func(a []rt.Value) (rt.Value, error) {
		if a[0].(string) == "" {
			return fail("empty"), nil
		}
		return ok(int64(len(a[0].(string)))), nil
	},
	"succeed": func([]rt.Value) (rt.Value, error) { return ok(rt.Unit{}), nil },
	"refuse":  func([]rt.Value) (rt.Value, error) { return fail("nope"), nil },
	"broken":  func([]rt.Value) (rt.Value, error) { return nil, errors.New("callback broke") },
	"ignore":  func([]rt.Value) (rt.Value, error) { return rt.Unit{}, nil },
}

type argSet struct {
	args      []any
	shapeOnly bool
}

func sets(lists ...[]any) []argSet {
	out := make([]argSet, len(lists))
	for k, l := range lists {
		out[k] = argSet{args: l}
	}
	return out
}
func a(v ...any) []any   { return v }
func fn(name string) any { return fnRef(name) }

func TestParity_FixtureReproducesItsRecordedOutcomes(t *testing.T) {
	rb = newRecordBuilder()
	golden := readOutcomes(t)
	env := &hostadapt.Env{}
	env.Invoke = func(_ *rt.Frame, f rt.Value, args []rt.Value) (rt.Value, error) {
		name, _ := f.(string)
		cb, known := callbacks[name]
		if !known {
			return nil, fmt.Errorf("unknown function value %v", f)
		}
		return cb(args)
	}
	table, err := Bind(env)
	if err != nil {
		t.Fatal(err)
	}

	box, err := table["ffx.new_box"](nil, a(i(41)))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]argSet{
		"ffx.echo": sets(
			a(tagged("n", 5, 7, 1.5, 1_700_000_000_000_000_000, 3_000_000_000, "raw", some(point(1, 2)))),
			a(tagged("", -128, 0, math.Inf(-1), 0, -1, "", none())),
			a(tagged("x", 200, 7, 0, 0, 0, "", none())),  // small overflows int8
			a(tagged("x", 1, -1, 0, 0, 0, "", none())),   // wide is negative
			a(tagged("x", 1, 1, 1e39, 0, 0, "", none())), // ratio overflows float32
		),
		"ffx.narrow": sets(
			a(i(1), i(2), i(3), i(4), i(5), i(6), i(7), i(8)),
			a(i(-128), i(-32768), i(math.MinInt32), i(math.MinInt64/2), i(0), i(65535), i(math.MaxUint32), i(math.MaxInt64/2)),
			a(i(128), i(0), i(0), i(0), i(0), i(0), i(0), i(0)),
			a(i(0), i(0), i(0), i(0), i(-1), i(0), i(0), i(0)),
			a(i(0), i(0), i(0), i(0), i(0), i(65536), i(0), i(0)),
		),
		"ffx.biggest":         sets(a()),
		"ffx.halve":           sets(a(3.0), a(math.NaN()), a(-1e39), a(math.Copysign(0, -1))),
		"ffx.later":           sets(a(instant(0), duration(1)), a(instant(1_700_000_000_000_000_000), duration(-5)), a(instant(math.MaxInt64), duration(1))),
		"ffx.zero_time":       sets(a()),
		"ffx.mirror":          sets(a(some(point(3, 4))), a(none())),
		"ffx.octets":          sets(a(rt.Bytes([]byte{0, 1, 254})), a(rt.Bytes(""))),
		"ffx.flip":            sets(a(rt.Byte(0)), a(rt.Byte(170))),
		"ffx.shout":           sets(a(s("hi")), a(s(""))),
		"ffx.negate":          sets(a(true), a(false)),
		"ffx.triple":          sets(a(i(7)), a(i(-2))),
		"ffx.triple_or_fail":  sets(a(i(7)), a(i(-1))),
		"ffx.nothing":         sets(a()),
		"ffx.nothing_or_fail": sets(a(false), a(true)),
		"ffx.points":          sets(a(i(0)), a(i(3))),
		"ffx.new_box":         {{args: a(i(1)), shapeOnly: true}},
		"ffx.box_value":       sets(a(box)),
		"ffx.apply":           sets(a(fn("double"), i(5)), a(fn("broken"), i(5))),
		"ffx.check":           sets(a(fn("ordered"), point(1, 2)), a(fn("ordered"), point(2, 2)), a(fn("broken"), point(0, 0))),
		"ffx.attempt":         sets(a(fn("length"), s("four")), a(fn("length"), s("")), a(fn("broken"), s("x"))),
		"ffx.run":             sets(a(fn("succeed")), a(fn("refuse")), a(fn("broken"))),
		"ffx.visit":           sets(a(fn("ignore"), i(3)), a(fn("broken"), i(2))),
		"ffx.query":           sets(a(s("/p"), strs("apple", "avocado", "bean", "")), a(s(""), strs())),
		"ffx.sizes": sets(
			a(rtMap("a", strs("x", "y"), "b", strs())),
			a(rtMap()),
			a(rtMap("a", i(1))), // a value that is not a List
		),
	}

	names := make([]string, 0, len(cases))
	for n := range cases {
		names = append(names, n)
	}
	sort.Strings(names)
	var failures, compared int
	var lines []string
	for _, name := range names {
		for n, set := range cases[name] {
			args := make([]rt.Value, len(set.args))
			for k, x := range set.args {
				args[k] = x
				if f, isFn := x.(fnRef); isFn {
					args[k] = string(f)
				}
			}
			key := fmt.Sprintf("%s#%d%s", name, n, argText(set.args))
			got := classify(func() (rt.Value, error) { return table[name](nil, args) })
			text := outcomeText(got, set.shapeOnly)
			lines = append(lines, strconv.Quote(key)+"\t"+strconv.Quote(text))
			want, recorded := golden[key]
			switch {
			case !recorded:
				t.Errorf("%s: no recorded outcome", key)
				continue
			case text != want:
				t.Errorf("%s:\n  recorded %s\n  adapter  %s", key, want, text)
				continue
			}
			if got.err != nil {
				failures++
			} else {
				compared++
			}
		}
	}
	if os.Getenv("NOMI_UPDATE_HOST_GOLDEN") != "" {
		if err := os.WriteFile(goldenPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if len(lines) != len(golden) {
		t.Errorf("%d argument sets ran and %s records %d", len(lines), goldenPath, len(golden))
	}
	var uncovered []string
	for _, n := range Names() {
		if len(cases[n]) == 0 {
			uncovered = append(uncovered, n)
		}
	}
	if len(uncovered) > 0 {
		t.Errorf("no case for %s", strings.Join(uncovered, ", "))
	}
	if failures == 0 || compared == 0 {
		t.Errorf("compared %d results and %d failures; one population is empty", compared, failures)
	}
	t.Logf("%d bindings, %d results compared, %d failures compared by text", len(Names()), compared, failures)
}

// readOutcomes reads the recording: one `"<key>"\t"<outcome>"` line per
// argument set, each half Go-quoted.
func readOutcomes(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		k, v, found := strings.Cut(line, "\t")
		key, err1 := strconv.Unquote(k)
		outcome, err2 := strconv.Unquote(v)
		if !found || err1 != nil || err2 != nil {
			t.Fatalf("%s: malformed line %q", goldenPath, line)
		}
		out[key] = outcome
	}
	return out
}

func argText(args []any) string {
	parts := make([]string, len(args))
	for k, x := range args {
		if f, isFn := x.(fnRef); isFn {
			parts[k] = "<fn " + string(f) + ">"
			continue
		}
		parts[k] = rt.RowText(x)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
