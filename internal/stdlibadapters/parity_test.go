package stdlibadapters

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"github.com/nomi-language/nomi/rt"
)

// THE PARITY SUITE: every generated adapter for a Funcs row against the
// outcomes a reflective marshaller produced for the same argument sets
// (testdata/funcs.golden, 698 lines).
//
// A marshaller error is recorded as `error <text>`. The adapters agreed with
// every line when it was taken (same dynamic types, qualified record names,
// variants and fields; a failure by its text).
//
// An outcome is the RtFuncs suite's canonical text (canonText) or, for a
// failure, the error's text: the same dynamic types, the same QUALIFIED record
// names (which rt.Equal does not check), the same variants, fields by name,
// Float by bits. shapeOnly stops at types and names, for a host function
// whose answer is not a function of its operands (OS entropy, a fresh
// handle). Two answers are recorded by shape or masked where the marshaller
// and the adapter were once compared exactly, because each run starts its own
// server: Server.addr (the port), and a Date header the Go server generated
// itself, which reads `<server date>`.
//
// Each adapter runs TWICE per argument set: once over records laid out by
// NAME (rtbuild_test.go), which exercise the adapters' by-name slow path, and
// once over records re-laid onto the adapters' own descriptors, which
// exercise the by-offset fast path an engine that links descriptors takes.
// Both must answer the recorded outcome.
//
// Regenerating the recording is not a way to make this pass: the marshaller
// no longer exists. NOMI_UPDATE_HOST_GOLDEN rewrites it from the adapters, for
// a deliberate change of behaviour.

const funcsGoldenPath = "testdata/funcs.golden"

var testEnv hostadapt.Env

type argSet struct {
	args      []any
	shapeOnly bool
}

func i(n int64) any  { return n }
func s(v string) any { return v }
func strs(v ...string) any {
	items := make([]any, len(v))
	for k, x := range v {
		items[k] = x
	}
	return rtList(items...)
}

func record(name string, kv ...any) *rt.Record {
	pairs := make([]any, 0, len(kv))
	for k := 0; k < len(kv); k += 2 {
		v := kv[k+1]
		if n, isInt := v.(int); isInt {
			v = int64(n)
		}
		pairs = append(pairs, kv[k], v)
	}
	return rb.Struct(name, pairs...)
}

func date(y, m, d int) any { return record("calendar.Date", "year", y, "month", m, "day", d) }
func clock(h, m, sec, ns int) any {
	return record("calendar.Time", "hour", h, "minute", m, "second", sec, "nanosecond", ns)
}
func naive(y, mo, d, h, mi, sec, ns int) any {
	return record("calendar.NaiveDateTime", "year", y, "month", mo, "day", d, "hour", h, "minute", mi, "second", sec, "nanosecond", ns)
}
func offsetDT(nanos int64, offsetSeconds int) any {
	return record("calendar.OffsetDateTime", "instant_nanos", nanos, "offset_seconds", offsetSeconds)
}
func zoned(nanos int64, zone string) any {
	return record("calendar.DateTime", "instant_nanos", nanos, "zone", zone)
}

func sets(argLists ...[]any) []argSet {
	out := make([]argSet, len(argLists))
	for k, a := range argLists {
		out[k] = argSet{args: a}
	}
	return out
}

func a(v ...any) []any { return v }

// calendarCases covers every calendar binding. The values sit on the rule
// edges the adapter's conversions could get wrong without changing a
// calendar answer: leap days, month ends, DST gaps and folds, negative and
// large instants, unknown zones, and malformed text.
func calendarCases() map[string][]argSet {
	dates := []any{date(2024, 2, 29), date(2026, 1, 31), date(1, 1, 1), date(9999, 12, 31)}
	times := []any{clock(0, 0, 0, 0), clock(23, 59, 59, 999_999_999), clock(12, 30, 15, 500)}
	naives := []any{naive(2026, 3, 8, 2, 30, 0, 0), naive(2024, 2, 29, 23, 59, 59, 999_999_999), naive(2026, 11, 1, 1, 30, 0, 0)}
	offsets := []any{offsetDT(1_700_000_000_000_000_000, 19800), offsetDT(0, -18000), offsetDT(-1_000_000_000_000_000_000, 0)}
	zones := []any{
		zoned(1_700_000_000_000_000_000, "America/New_York"),
		zoned(0, "UTC"),
		zoned(1_741_417_200_000_000_000, "America/New_York"), // 2026-03-08, the spring-forward day
		zoned(-1_000_000_000_000_000_000, "Europe/London"),
		zoned(1_700_000_000_000_000_000, "Asia/Kolkata"),
	}
	ns := []any{i(0), i(1), i(-1), i(12), i(1000), i(1 << 40)}

	unary := func(xs []any) []argSet {
		out := make([]argSet, len(xs))
		for k, x := range xs {
			out[k] = argSet{args: a(x)}
		}
		return out
	}
	withN := func(xs []any) []argSet {
		var out []argSet
		for _, x := range xs {
			for _, n := range ns {
				out = append(out, argSet{args: a(x, n)})
			}
		}
		return out
	}
	pairs := func(xs []any) []argSet {
		var out []argSet
		for _, x := range xs {
			for _, y := range xs {
				out = append(out, argSet{args: a(x, y)})
			}
		}
		return out
	}
	texts := func(v ...string) []argSet {
		out := make([]argSet, len(v))
		for k, x := range v {
			out[k] = argSet{args: a(s(x))}
		}
		return out
	}

	c := map[string][]argSet{
		"Date.days_between":            pairs(dates),
		"Date.to_string":               unary(dates),
		"Time.to_string":               unary(times),
		"NaiveDateTime.to_date":        unary(naives),
		"NaiveDateTime.to_time":        unary(naives),
		"NaiveDateTime.to_string":      unary(naives),
		"OffsetDateTime.to_string":     unary(offsets),
		"DateTime.to_string":           unary(zones),
		"DateTime.to_offset":           unary(zones),
		"calendar.zoned_offset_nanos":  unary(zones),
		"calendar.date_add_days":       withN(dates[:2]),
		"calendar.date_add_months":     withN(dates[:2]),
		"calendar.date_add_years":      withN(dates[:2]),
		"calendar.time_add_nanos":      withN(times),
		"calendar.naive_add_days":      withN(naives),
		"calendar.naive_add_months":    withN(naives),
		"calendar.naive_add_nanos":     withN(naives),
		"calendar.naive_add_years":     withN(naives),
		"calendar.naive_between_nanos": pairs(naives),
		"calendar.offset_add_months":   withN(offsets),
		"calendar.offset_add_years":    withN(offsets),
		"calendar.date_new_raw": sets(
			a(i(2026), i(5), i(4)), a(i(2024), i(2), i(29)), a(i(2023), i(2), i(29)),
			a(i(2026), i(2), i(30)), a(i(2026), i(13), i(1)), a(i(0), i(0), i(0))),
		"calendar.naive_new_exact_raw": sets(
			a(i(2026), i(5), i(4), i(12), i(30), i(0), i(0)),
			a(i(2026), i(5), i(4), i(24), i(0), i(0), i(0)),
			a(i(2024), i(2), i(29), i(23), i(59), i(59), i(999_999_999)),
			a(i(2026), i(5), i(4), i(1), i(2), i(3), i(1_000_000_000))),
		"calendar.date_parse_raw":   texts("2026-05-04", "2024-02-29", "2023-02-29", "bad", ""),
		"calendar.time_parse_raw":   texts("12:34:56.789", "00:00", "25:00:00", "12:61:00", "x"),
		"calendar.naive_parse_raw":  texts("2026-03-08T02:30:00", "2026-03-08T02:30:00.5", "2026-03-08 02:30", "nope"),
		"calendar.offset_parse_raw": texts("2026-03-08T02:30:00+05:30", "2026-03-08T02:30:00Z", "2026-03-08T02:30:00", "2026-03-08T02:30:00+25:00"),
		"calendar.zoned_parse_raw": texts("2026-03-08T02:30:00-05:00[America/New_York]", "2026-06-15T12:00:00+01:00[Europe/London]",
			"2026-06-15T12:00:00+01:00[Mars/Olympus]", "2026-06-15T12:00:00"),
		"calendar.offset_with_offset_raw": sets(
			a(naives[0], i(19800)), a(naives[1], i(-18000)), a(naives[2], i(90_000)), a(naives[0], i(1))),
		"calendar.offset_from_instant_nanos": sets(
			a(i(1_700_000_000_000_000_000), i(19800)), a(i(0), i(0)), a(i(-1), i(-3600))),
		"calendar.zoned_from_instant_in_raw": sets(
			a(i(1_700_000_000_000_000_000), s("America/New_York")), a(i(0), s("UTC")), a(i(0), s("Mars/Olympus"))),
		"calendar.zoned_with_zone_raw": sets(
			a(zones[0], s("Asia/Tokyo")), a(zones[1], s("America/Los_Angeles")), a(zones[0], s("Mars/Olympus"))),
	}
	var inZone []argSet
	for _, n := range naives {
		for _, mode := range []int64{0, 1, 2, 3} {
			inZone = append(inZone, argSet{args: a(n, s("America/New_York"), i(mode))})
		}
		inZone = append(inZone, argSet{args: a(n, s("Mars/Olympus"), i(0))})
	}
	c["calendar.zoned_in_zone_raw"] = inZone
	// A DateTime whose zone the tzdb does not carry cannot be built by a
	// program, and the Go side traps on it. The trap is a panic, so it is the
	// population on which the FAILURE texts are compared.
	unknownZone := zoned(0, "Mars/Olympus")
	for _, unit := range []string{"years", "months", "days", "hours", "minutes", "seconds", "milliseconds", "microseconds", "nanoseconds"} {
		c["calendar.zoned_add_"+unit] = withN(append(zones[:3:3], unknownZone))
	}
	for _, acc := range []string{"year", "month", "day", "hour", "minute", "second", "nanosecond"} {
		c["OffsetDateTime."+acc] = unary(offsets)
		c["DateTime."+acc] = unary(append(zones[:len(zones):len(zones)], unknownZone))
	}
	return c
}

func randomCases() map[string][]argSet {
	return map[string][]argSet{
		"random.below_state": sets(a(i(1), i(10)), a(i(123456789), i(1)), a(i(-5), i(1000)),
			a(i(math.MaxInt64), i(math.MaxInt64)), a(i(7), i(0))),
		"random.unit_float_state": sets(a(i(1)), a(i(42)), a(i(-1)), a(i(math.MinInt64))),
		"random.os_state":         {{args: a(), shapeOnly: true}},
	}
}

// parity holds one comparison run.
type parity struct {
	t       *testing.T
	table   map[string]hostadapt.Func
	env     *hostadapt.Env
	byName  map[string]*rt.TypeDesc // the adapters' own struct and distinct descriptors
	covered map[string]int
	golden  map[string]string
	lines   []string
	calls   int
	// Tallies, so a clean run can be told apart from a run that compared
	// nothing interesting.
	failures    int // a recorded failure reproduced
	errResults  int // a recorded Result.Err reproduced
	fastRecords int // record operands laid onto the adapters' own descriptors
}

func newParity(t *testing.T) *parity {
	rb = newRecordBuilder()
	p := &parity{
		t:       t,
		env:     &hostadapt.Env{},
		byName:  map[string]*rt.TypeDesc{},
		covered: map[string]int{},
		golden:  readOutcomes(t, funcsGoldenPath),
	}
	var err error
	if p.table, err = Bind(p.env); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	for _, spec := range Specs() {
		if spec.Kind == rt.KindStruct || spec.Kind == rt.KindDistinct {
			d, err := p.env.Desc(spec)
			if err != nil {
				t.Fatal(err)
			}
			p.byName[spec.Name] = d
		}
	}
	return p
}

// relayout rebuilds every struct and distinct record the adapters have a
// descriptor for onto that descriptor, so the adapter reads it by offset.
func (p *parity) relayout(v rt.Value) rt.Value {
	switch x := v.(type) {
	case *rt.Record:
		d, ok := p.byName[x.Desc.Name]
		if !ok || x.Desc.Kind == rt.KindEnum {
			return v
		}
		vals := make([]any, len(d.Fields))
		for k, f := range d.Fields {
			fv, ok := x.FieldNamed(f.Name)
			if !ok {
				p.t.Fatalf("relayout %s: no field %q", x.Desc.Name, f.Name)
			}
			vals[k] = p.relayout(fv)
		}
		return d.Make(vals...)
	case *rt.List[any]:
		var items []any
		for c := x; c != nil; c = c.Tail {
			items = append(items, p.relayout(c.Head))
		}
		return rtList(items...)
	}
	return v
}

// compare holds one outcome to its recording.
func (p *parity) compare(key string, o hostOutcome, shapeOnly bool, layout string) {
	p.t.Helper()
	text := funcsOutcome(o, shapeOnly)
	if layout != "by offset" {
		p.lines = append(p.lines, strconv.Quote(key)+"\t"+strconv.Quote(text))
	}
	want, recorded := p.golden[key]
	switch {
	case !recorded:
		p.t.Errorf("%s: no recorded outcome", key)
		return
	case text != want:
		p.t.Errorf("%s [%s]:\n  recorded %s\n  adapter  %s", key, layout, want, text)
		return
	}
	if o.err != nil {
		p.failures++
	} else if r, ok := o.val.(*rt.Record); ok && r.Desc.Kind == rt.KindEnum && r.Variant().Name == "Err" {
		p.errResults++
	}
}

func (p *parity) check(name string, set argSet) {
	p.t.Helper()
	fn := p.table[name]
	if fn == nil {
		p.t.Fatalf("no adapter for %s", name)
	}
	key := fmt.Sprintf("%s#%d%s", name, p.covered[name], hostArgText(set.args))
	laid := make([]rt.Value, len(set.args))
	for k, x := range set.args {
		laid[k] = p.relayout(x)
	}
	for _, layout := range []string{"by name", "by offset"} {
		args := set.args
		if layout == "by offset" {
			args = laid
			for _, x := range args {
				if r, ok := x.(*rt.Record); ok && p.byName[r.Desc.Name] == r.Desc {
					p.fastRecords++
				}
			}
		}
		got := classify(func() (rt.Value, error) { return fn(nil, args) })
		p.calls++
		p.compare(key, got, set.shapeOnly, layout)
	}
	p.covered[name]++
}

func (p *parity) run(cases map[string][]argSet) {
	names := make([]string, 0, len(cases))
	for n := range cases {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, set := range cases[n] {
			p.check(n, set)
		}
	}
}

// regex needs handles, and a handle comes from compiling.
func (p *parity) regex() {
	for _, pattern := range []string{"a+b", `(\d+)-(\d+)`, "^$", "é+"} {
		p.check("Regex.compile", argSet{args: a(s(pattern)), shapeOnly: true})
		res, err := p.table["Regex.compile"](nil, a(s(pattern)))
		if err != nil {
			p.t.Fatal(err)
		}
		re := res.(*rt.Record).Field(0)
		for _, input := range []string{"xaab ab", "12-34 and 5-6", "", "ééé"} {
			p.check("Regex.pattern", argSet{args: a(re)})
			p.check("Regex.match?", argSet{args: a(re, s(input))})
			p.check("Regex.find", argSet{args: a(re, s(input))})
			p.check("Regex.find_all", argSet{args: a(re, s(input))})
			p.check("Regex.split", argSet{args: a(re, s(input))})
			p.check("Regex.replace_all", argSet{args: a(re, s(input), s("<$1>"))})
		}
	}
	p.check("Regex.compile", argSet{args: a(s("(")), shapeOnly: false})
	p.check("Regex.compile", argSet{args: a(s("[z-a]"))})
}

func TestParity_EveryBindingReproducesItsRecordedOutcomes(t *testing.T) {
	p := newParity(t)
	p.run(calendarCases())
	p.run(randomCases())
	p.regex()

	updateOutcomes(t, funcsGoldenPath, p.lines)
	if len(p.lines) != len(p.golden) {
		t.Errorf("%d outcomes ran and %s records %d", len(p.lines), funcsGoldenPath, len(p.golden))
	}
	var missing []string
	for _, b := range stdlibbindings.Funcs() {
		if p.covered[b.Name] == 0 {
			missing = append(missing, b.Name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d binding(s) have no parity case: %s", len(missing), strings.Join(missing, ", "))
	}
	sets := 0
	for _, n := range p.covered {
		sets += n
	}
	t.Logf("%d of %d bindings covered, %d argument sets, %d adapter calls; %d recorded failures, "+
		"%d recorded Result.Err answers, %d record operands read by offset",
		len(p.covered), len(stdlibbindings.Funcs()), sets, p.calls, p.failures/2, p.errResults/2, p.fastRecords)
	if p.failures == 0 || p.errResults == 0 || p.fastRecords == 0 {
		t.Errorf("a population is empty (failures %d, Err answers %d, by-offset records %d); "+
			"the comparison has stopped reaching it", p.failures, p.errResults, p.fastRecords)
	}
}

// TestAdapterFailuresAreTheMarshallers pins the engine-level failures an
// adapter reports, which are the texts the marshaller reported: a wrong
// operand count, a wrong operand type, and a panic in the Go function.
func TestAdapterFailuresAreTheMarshallers(t *testing.T) {
	table, err := Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := table["Regex.compile"](nil, nil); err == nil || err.Error() != "Regex.compile: expected 1 args, got 0" {
		t.Errorf("arity: %v", err)
	}
	if _, err := table["Regex.compile"](nil, a(int64(1))); err == nil || !strings.Contains(err.Error(), "Regex.compile: arg 0: expected String, got int64") {
		t.Errorf("operand type: %v", err)
	}
	// A handle holding the wrong Go type is refused before the call.
	_, err = table["Regex.pattern"](nil, a(rt.HostHandle{TypeName: "regex.Regex", Value: "not a regex"}))
	if err == nil || !strings.Contains(err.Error(), "regex.Regex handle holds string") {
		t.Errorf("a handle of the wrong Go type: %v", err)
	}
	// A DateTime in a zone the tzdb does not carry traps inside the Go
	// function; the adapter reports the panic as the marshaller's wrapper did.
	dt := rt.NewStructDesc("calendar.DateTime", []rt.FieldSpec{{Name: "instant_nanos", Type: rt.SlotInt}, {Name: "zone", Type: rt.SlotString}})
	_, err = table["calendar.zoned_add_days"](nil, a(dt.Make(int64(0), "Mars/Olympus"), int64(1)))
	if err == nil || !strings.HasPrefix(err.Error(), "calendar.zoned_add_days: panic: ") {
		t.Errorf("a panic in the Go function: %v", err)
	}
	// A callback whose Nomi function fails hands the failure to the Go
	// callback's error result, and with no Invoker there is nothing to call.
	failing := &hostadapt.Env{Invoke: func(*rt.Frame, rt.Value, []rt.Value) (rt.Value, error) { return nil, errors.New("boom") }}
	if _, err := Bind(failing); err != nil {
		t.Fatal(err)
	}
	if _, err := (&hostadapt.Env{}).Call(nil, "f", nil); err == nil {
		t.Errorf("an Env with no Invoker called a function")
	}
}

// TestBindChecksAnEngineDescriptor holds the rule that an engine's own
// descriptor must match the declaration the adapter writes by offset.
func TestBindChecksAnEngineDescriptor(t *testing.T) {
	env := &hostadapt.Env{Resolve: func(spec *hostadapt.DescSpec) (*rt.TypeDesc, error) {
		if spec.Name == "calendar.Date" {
			return rt.NewStructDesc("calendar.Date", []rt.FieldSpec{{Name: "day", Type: rt.SlotInt}, {Name: "month", Type: rt.SlotInt}, {Name: "year", Type: rt.SlotInt}}), nil
		}
		return spec.Build(), nil
	}}
	if _, err := Bind(env); err == nil || !strings.Contains(err.Error(), "calendar.Date does not match the declaration") {
		t.Fatalf("a Date descriptor in name order was accepted: %v", err)
	}
}
