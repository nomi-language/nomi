package stdlibadapters

// THE RtFuncs SUITE: every generated adapter for an
// internal/stdlibbindings.RtFuncs row against the outcomes the VM's
// hand-written hosts recorded before they were deleted
// (testdata/rtfuncs.golden, 966 argument sets). The marshaller cannot be the
// reference for these rows: it carries neither rt.Maybe[int64], rt.Json nor
// rt.Decimal.
//
// Each argument set runs twice, once over records laid out in name order by
// rtbuild_test.go, as the records the recording was taken over were laid out
// (the adapters' by-name path), and once over records
// re-laid onto the adapters' own descriptors (the by-offset path), and both
// outcomes must be the recorded one. An outcome is a value in a canonical text stricter than
// rt.Equal (dynamic types, qualified record names, variants, fields by name,
// Float by bits), an rt trap's text, a panic's Go type and text, or a refusal.
// A trap is what the VM reports as a fault: the adapters for these rows let
// it unwind (stdlibbindings.Binding.PanicsPropagate) and the machine turns it
// into its fault at the call.
//
// Regenerating the recording is not a way to make this pass: it was taken
// from the hand-written hosts (commit "vm: record the hand-written hosts'
// outcomes"), which no longer exist. NOMI_UPDATE_HOST_GOLDEN rewrites it from
// the adapters, for a deliberate change of behaviour.
//
// A line is
//
//	"<key>#<case index><argument text>"	"<outcome>"
//
// each half Go-quoted, so no line breaks inside one. A Context result is its
// deadline's remaining time rounded to the second; a result that depends on
// the clock (Instant.now, a fresh Supervisor) is recorded by shape.

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"github.com/nomi-language/nomi/rt"
)

type hostOutcome struct {
	val   rt.Value
	fault string // an rt trap's text
	panic string // any other panic, "%T: %v"
	err   error  // an engine-level refusal
}

// classify runs call and sorts what it did into a hostOutcome.
func classify(call func() (rt.Value, error)) (out hostOutcome) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(*rt.Error); ok {
				out = hostOutcome{fault: e.Error()}
				return
			}
			out = hostOutcome{panic: fmt.Sprintf("%T: %v", p, p)}
		}
	}()
	v, err := call()
	if err != nil {
		return hostOutcome{err: err}
	}
	return hostOutcome{val: v}
}

type hostCase struct {
	args      []any
	frame     *rt.Frame // nil: a plain frame
	shapeOnly bool
}

// idleSupervisor is a Supervisor with no work, spelled `Supervisor` in the
// recording's argument text, as the VM's IdleSupervisor was.
type idleSupervisor struct{ s rt.Supervisor }

func idleSupervisorValue() rt.Supervisor {
	fr := rt.EnterBoot(rt.NewFrame(context.Background()))
	return rt.SupervisorNewExact(fr, 1, rt.DefaultShutdownTimeout, rt.Restart{Tag: 1},
		rt.Backoff{Tag: 1, MaxRestarts: rt.BackoffDefaultMaxRestarts, MaxElapsed: rt.BackoffDefaultMaxElapsed}, rt.GiveUp{Tag: 1})
}

type rtFuncsSuite struct {
	t       *testing.T
	table   map[string]hostadapt.Func
	byName  map[string]*rt.TypeDesc
	covered map[string]int
	golden  map[string]string
	lines   []string

	calls, values, faults, panics, refusals, fastRecords int
}

func newRtFuncsSuite(t *testing.T) *rtFuncsSuite {
	p := &rtFuncsSuite{
		t:       t,
		byName:  map[string]*rt.TypeDesc{},
		covered: map[string]int{},
		golden:  map[string]string{},
	}
	rb = newRecordBuilder()
	env := &hostadapt.Env{}
	var err error
	if p.table, err = Bind(env); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	for _, spec := range Specs() {
		if spec.Kind == rt.KindStruct || spec.Kind == rt.KindDistinct {
			d, err := env.Desc(spec)
			if err != nil {
				t.Fatal(err)
			}
			p.byName[spec.Name] = d
		}
	}
	data, err := os.ReadFile(hostGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		k, v, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("%s: malformed line %q", hostGoldenPath, line)
		}
		key, err1 := strconv.Unquote(k)
		outcome, err2 := strconv.Unquote(v)
		if err1 != nil || err2 != nil {
			t.Fatalf("%s: malformed line %q", hostGoldenPath, line)
		}
		p.golden[key] = outcome
	}
	return p
}

// toRT is the value an adapter receives for a case operand.
func (p *rtFuncsSuite) toRT(a any) rt.Value {
	switch x := a.(type) {
	case idleSupervisor:
		return x.s
	default:
		return x
	}
}

// relayout rebuilds every struct and distinct record the adapters have a
// descriptor for onto that descriptor, so the adapter reads it by offset.
func (p *rtFuncsSuite) relayout(v rt.Value) rt.Value {
	x, ok := v.(*rt.Record)
	if !ok {
		return v
	}
	d, ok := p.byName[x.Desc.Name]
	if !ok || x.Desc.Kind == rt.KindEnum {
		return v
	}
	vals := make([]any, len(d.Fields))
	for k, f := range d.Fields {
		var fv rt.Value
		if f.Name == "" {
			fv = x.Field(k)
		} else {
			fv, _ = x.FieldNamed(f.Name)
		}
		vals[k] = p.relayout(fv)
	}
	return d.Make(vals...)
}

func (p *rtFuncsSuite) check(key string, c hostCase) {
	p.t.Helper()
	fn := p.table[key]
	if fn == nil {
		p.t.Fatalf("%s: no adapter", key)
	}
	fr := c.frame
	if fr == nil {
		fr = rt.NewFrame(context.Background())
	}
	line := fmt.Sprintf("%s#%d%s", key, p.covered[key], hostArgText(c.args))
	want, recorded := p.golden[line]
	if !recorded {
		p.t.Errorf("%s: no recorded outcome", line)
	}
	rtArgs := make([]rt.Value, len(c.args))
	laid := make([]rt.Value, len(c.args))
	for k, a := range c.args {
		rtArgs[k] = p.toRT(a)
		laid[k] = p.relayout(rtArgs[k])
	}
	var first string
	for _, layout := range []string{"by name", "by offset"} {
		args := rtArgs
		if layout == "by offset" {
			args = laid
			for _, a := range args {
				if r, ok := a.(*rt.Record); ok && p.byName[r.Desc.Name] == r.Desc {
					p.fastRecords++
				}
			}
		}
		got := classify(func() (rt.Value, error) { return fn(fr, args) })
		p.calls++
		text := goldenOutcome(got, c.shapeOnly)
		if layout == "by name" {
			first = text
		}
		if recorded && text != want {
			p.t.Errorf("%s [%s]:\n  recorded %s\n  adapter  %s", line, layout, want, text)
			continue
		}
		switch {
		case got.fault != "":
			p.faults++
		case got.panic != "":
			p.panics++
		case got.err != nil:
			p.refusals++
		default:
			p.values++
		}
	}
	p.lines = append(p.lines, strconv.Quote(line)+"\t"+strconv.Quote(first))
	p.covered[key]++
}

func hostArgText(args []any) string {
	parts := make([]string, len(args))
	for k, a := range args {
		switch x := a.(type) {
		case rt.Context:
			parts[k] = "<context>"
		case rt.Supervisor:
			parts[k] = "<supervisor>"
		case idleSupervisor:
			parts[k] = "Supervisor"
		default:
			parts[k] = rt.RowText(x)
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// --- argument sets -------------------------------------------------------------

func hInt(n int64) any       { return n }
func hFloat(x float64) any   { return x }
func hStr(s string) any      { return s }
func hBytes(s string) any    { return rt.Bytes(s) }
func hByte(b byte) any       { return rt.Byte(b) }
func hDur(n int64) any       { return rb.Distinct("duration.Duration", n) }
func hCodepoint(n int64) any { return rb.Distinct("codepoints.Codepoint", n) }
func hDecimal(t *testing.T, text string) any {
	d, err := rt.ParseDecimal(text)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func hVariant(enum, variant string, payload any) any {
	if payload == nil {
		return rb.Variant(enum, variant)
	}
	return rb.Variant(enum, variant, payload)
}

func unary(xs ...any) []hostCase {
	out := make([]hostCase, len(xs))
	for k, x := range xs {
		out[k] = hostCase{args: []any{x}}
	}
	return out
}

func pairs(xs ...any) []hostCase {
	var out []hostCase
	for _, x := range xs {
		for _, y := range xs {
			out = append(out, hostCase{args: []any{x, y}})
		}
	}
	return out
}

func cases(argLists ...[]any) []hostCase {
	out := make([]hostCase, len(argLists))
	for k, a := range argLists {
		out[k] = hostCase{args: a}
	}
	return out
}

func args(v ...any) []any { return v }

func (p *rtFuncsSuite) scalarCases() map[string][]hostCase {
	t := p.t
	texts := []string{"", "a", "héllo", "  padded\t\n", "🙂x", "ÅNGSTRÖM", "ǅ", " 42 ", "42", "-9223372036854775808",
		"9223372036854775808", "12a", "+7", "e\u0301", "0x10", "٣"}
	strs := make([]any, len(texts))
	for k, x := range texts {
		strs[k] = hStr(x)
	}
	stringPairs := cases(
		args(hStr(""), hStr("")), args(hStr("hello"), hStr("ll")), args(hStr("hello"), hStr("he")),
		args(hStr("hello"), hStr("lo")), args(hStr("abc"), hStr("")), args(hStr(""), hStr("a")),
		args(hStr("a,b,,c"), hStr(",")), args(hStr("héllo"), hStr("é")), args(hStr("b"), hStr("a")),
		args(hStr("a"), hStr("ab")), args(hStr("é"), hStr("z")), args(hStr("🙂🙂"), hStr("🙂")),
	)
	wordsAndLines := unary(hStr(""), hStr("   "), hStr("  1   2\t3 \n"), hStr("café 🙂ok"), hStr("a\u00a0b\u3000c"),
		hStr("a\nb\r\nc\n"), hStr("a\n\nb"), hStr("\n"), hStr("a\rb"), hStr("x\r"), hStr("é\n🙂"))
	ints := []any{hInt(0), hInt(1), hInt(-1), hInt(255), hInt(1 << 40), hInt(math.MaxInt64), hInt(math.MinInt64)}
	floats := []any{hFloat(0), hFloat(math.Copysign(0, -1)), hFloat(1.5), hFloat(-2.5), hFloat(2.5), hFloat(0.5),
		hFloat(-0.4), hFloat(1e21), hFloat(1e-7), hFloat(0.1 + 0.2), hFloat(math.Inf(1)), hFloat(math.Inf(-1)),
		hFloat(math.NaN()), hFloat(9.3e18), hFloat(-9.3e18), hFloat(9.2e18), hFloat(-9.223372036854775808e18)}
	blobs := []any{hBytes(""), hBytes("\x00"), hBytes("ab"), hBytes("\xff\xfe"), hBytes("héllo")}
	var at, slice, bslice []hostCase
	for _, b := range blobs {
		for _, n := range []int64{-1, 0, 1, 4, 100} {
			at = append(at, hostCase{args: args(b, hInt(n))})
		}
		for _, se := range [][2]int64{{0, 0}, {0, 2}, {1, 100}, {-3, 1}, {3, 1}} {
			bslice = append(bslice, hostCase{args: args(b, hInt(se[0]), hInt(se[1]))})
		}
	}
	for _, s := range []string{"héllo", "abc", "🙂🙂", "e\u0301x", ""} {
		for _, se := range [][2]int64{{0, 0}, {1, 3}, {-1, 10}, {2, 1}, {0, 1}} {
			slice = append(slice, hostCase{args: args(hStr(s), hInt(se[0]), hInt(se[1]))})
		}
	}
	decimals := []any{hDecimal(t, "1.50"), hDecimal(t, "1.5"), hDecimal(t, "-0.00"), hDecimal(t, "0"),
		hDecimal(t, "123456789012345678901234.5"), hDecimal(t, "0.001"), hDecimal(t, "-7")}
	durations := []any{hDur(0), hDur(1), hDur(1_500_000), hDur(-3_600_000_000_000), hDur(90 * int64(time.Minute)),
		hDur(math.MaxInt64), hDur(math.MinInt64)}

	c := map[string][]hostCase{
		"strings.String.contains?":       stringPairs,
		"strings.String.starts_with?":    stringPairs,
		"strings.String.ends_with?":      stringPairs,
		"strings.string_compare":         stringPairs,
		"strings.String.split":           stringPairs,
		"strings.String.words":           wordsAndLines,
		"strings.String.lines":           wordsAndLines,
		"strings.String.to_upper":        unary(strs...),
		"strings.String.to_lower":        unary(strs...),
		"strings.String.trim":            unary(strs...),
		"strings.String.hash":            unary(strs...),
		"strings.String.to_int":          unary(strs...),
		"strings.String.length":          unary(strs...),
		"strings.String.reverse":         unary(strs...),
		"strings.String.to_codepoints":   unary(strs...),
		"strings.String.to_bytes":        unary(strs...),
		"strings.String.slice":           slice,
		"strings.String.replace":         cases(args(hStr("a-b-c"), hStr("-"), hStr("+")), args(hStr("aaa"), hStr(""), hStr("x")), args(hStr(""), hStr(""), hStr("y")), args(hStr("héllo"), hStr("l"), hStr("L"))),
		"bytes.Byte.from_int":            unary(hInt(-1), hInt(0), hInt(255), hInt(256), hInt(math.MinInt64)),
		"bytes.Byte.to_int":              unary(hByte(0), hByte(127), hByte(255)),
		"bytes.Bytes.length":             unary(blobs...),
		"bytes.Bytes.to_string":          unary(blobs...),
		"bytes.Bytes.at":                 at,
		"bytes.Bytes.slice":              bslice,
		"bytes.Bytes.concat":             pairs(blobs...),
		"int.Int.to_string":              unary(ints...),
		"int.Int.to_float":               unary(ints...),
		"int.Int.bitwise_not":            unary(ints...),
		"int.Int.wrapping_add":           pairs(ints...),
		"int.Int.wrapping_sub":           pairs(ints...),
		"int.Int.wrapping_mul":           pairs(ints...),
		"int.Int.bitwise_and":            pairs(ints...),
		"int.Int.bitwise_or":             pairs(ints...),
		"int.Int.bitwise_xor":            pairs(ints...),
		"float.Float.to_string":          unary(floats...),
		"float.Float.nan?":               unary(floats...),
		"float.Float.round":              unary(floats...),
		"float.Float.floor":              unary(floats...),
		"float.Float.ceil":               unary(floats...),
		"float.Float.trunc":              unary(floats...),
		"float.Float.to_int":             unary(floats...),
		"float.float_bits":               unary(floats...),
		"float.Float.nan":                cases(args()),
		"float.Float.positive_infinity":  cases(args()),
		"float.Float.negative_infinity":  cases(args()),
		"decimal.Decimal.to_string":      unary(decimals...),
		"decimal.Decimal.hash":           unary(decimals...),
		"decimal.Decimal.equal?":         pairs(decimals...),
		"decimal.Decimal.compare":        pairs(decimals...),
		"duration.Duration.to_string":    unary(durations...),
		"codepoints.Codepoint.to_string": unary(hCodepoint(65), hCodepoint(0x1F642), hCodepoint(0xE9), hCodepoint(0)),
		"instant.Instant.now":            {{args: args(), shapeOnly: true}},
	}
	return c
}

// jsonCases decodes texts, and encodes the trees the decoder builds (the
// json.Json.decode adapter's, whose outcomes are recorded here too, rebuilt
// as another producer lays them out) plus hand-built ones decode cannot
// produce.
func (p *rtFuncsSuite) jsonCases() map[string][]hostCase {
	texts := []string{`{"b":[1,2.5,"x",null,true],"a":{"z":false,"y":[]}}`, `[]`, `{}`, `"é\n\"\\"`, `-0`, `0.1`,
		`9223372036854775808`, `1e400`, `{`, `[1,]`, `1 2`, `""`, `"\ud800"`, `nul`, `{"a":1,"a":2}`, ` [ [ [ ] ] ] `}
	var decode, encode []hostCase
	for _, s := range texts {
		decode = append(decode, hostCase{args: args(hStr(s))})
		res, err := p.table["json.Json.decode"](rt.NewFrame(context.Background()), []rt.Value{s})
		if err != nil {
			p.t.Fatal(err)
		}
		if r := res.(*rt.Record); r.Variant().Name == "Ok" {
			encode = append(encode, hostCase{args: args(rb.Restyle(r.Field(0)))})
		}
	}
	j := func(variant string, payload any) any { return hVariant("json.Json", variant, payload) }
	for _, v := range []any{
		j("Float", hFloat(36.0)), j("Float", hFloat(math.Copysign(0, -1))), j("Float", hFloat(math.NaN())),
		j("Float", hFloat(math.Inf(1))), j("Int", hInt(math.MinInt64)), j("String", hStr("<tab>\t</tab>")),
		j("Bool", hVariant("bool.Bool", "False", nil)), j("Null", nil),
		j("Arr", rtList(j("Null", nil), j("Arr", rtList()))),
	} {
		encode = append(encode, hostCase{args: args(v)})
	}
	return map[string][]hostCase{"json.Json.decode": decode, "json.Json.encode": encode}
}

// concurrencyCases exercise the five hosts that act on a frame or a
// runtime handle.
func (p *rtFuncsSuite) concurrencyCases() map[string][]hostCase {
	boot := rt.EnterBoot(rt.NewFrame(context.Background()))
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := rt.NewFrame(cancelledCtx)

	root := rt.ContextRoot()
	bounded := rt.ContextWithTimeout(rt.ContextRoot(), rt.Duration(time.Hour))

	restart := func(v string) any { return hVariant("supervisors.Restart", v, nil) }
	giveUp := func(v string) any { return hVariant("supervisors.GiveUp", v, nil) }
	exponential := func(n int64, d int64) any {
		return rb.FieldVariant("supervisors.Backoff", "Exponential", "max_restarts", hInt(n), "max_elapsed", hDur(d))
	}
	// A Backoff record naming a variant std does not declare: the adapter
	// refuses it rather than reading a tag it has no Go value for.
	undeclared := hVariant("supervisors.Backoff", "Linear", nil)
	good := func(limit int64, timeout int64, r, g string) []any {
		return args(hInt(limit), hDur(timeout), restart(r), exponential(10, int64(15*time.Minute)), giveUp(g))
	}

	// A supervisor with a task still running, so a bounded flush times out.
	busy := rt.SupervisorNewExact(boot, 1, rt.DefaultShutdownTimeout, rt.Restart{Tag: rt.TagTemporary},
		rt.Backoff{Tag: rt.TagExponential, MaxRestarts: 1, MaxElapsed: rt.Duration(time.Minute)}, rt.GiveUp{Tag: rt.TagReport})
	release := make(chan struct{})
	rt.SupervisorSpawn(boot, busy, func(*rt.Frame) rt.Unit { <-release; return rt.Unit{} })
	p.t.Cleanup(func() {
		close(release)
		rt.SupervisorFlushBounded(rt.NewFrame(context.Background()), busy, rt.Wait{Tag: rt.TagForever})
	})
	// A cancelled TASK frame: timer.sleep on it unwinds the task with rt's
	// cancellation panic rather than trapping, and both sides must let that
	// panic through untouched.
	taskFrame := make(chan *rt.Frame)
	finish := make(chan struct{})
	holder := rt.SupervisorNewExact(boot, 1, rt.DefaultShutdownTimeout, rt.Restart{Tag: rt.TagTemporary},
		rt.Backoff{Tag: rt.TagExponential, MaxRestarts: 1, MaxElapsed: rt.Duration(time.Minute)}, rt.GiveUp{Tag: rt.TagReport})
	task := rt.SupervisorSpawn(boot, holder, func(fr *rt.Frame) rt.Unit {
		taskFrame <- fr
		<-finish
		return rt.Unit{}
	})
	inTask := <-taskFrame
	rt.TaskCancel(task)
	p.t.Cleanup(func() {
		close(finish)
		rt.SupervisorFlushBounded(rt.NewFrame(context.Background()), holder, rt.Wait{Tag: rt.TagForever})
	})
	idle := idleSupervisor{idleSupervisorValue()}
	wait := func(v string, payload any) any { return hVariant("supervisors.Wait", v, payload) }

	return map[string][]hostCase{
		"timer.sleep": {
			{args: args(hDur(0))}, {args: args(hDur(int64(time.Millisecond)))}, {args: args(hDur(-5))},
			{args: args(hDur(int64(time.Hour))), frame: cancelled},
			{args: args(hDur(int64(time.Hour))), frame: inTask},
		},
		"context.Context.root": cases(args()),
		"context.Context.with_timeout": cases(
			args(root, hDur(int64(time.Second))), args(root, hDur(0)), args(root, hDur(-1)),
			args(bounded, hDur(int64(2*time.Hour))), args(bounded, hDur(int64(time.Minute))),
		),
		"supervisors.Supervisor.new_exact": {
			{args: good(1, 0, "Temporary", "Report"), frame: boot, shapeOnly: true},
			{args: good(4, int64(time.Second), "Transient", "Exit"), frame: boot, shapeOnly: true},
			{args: good(2, 5, "Permanent", "Report"), frame: boot, shapeOnly: true},
			{args: good(0, 0, "Temporary", "Report"), frame: boot},
			{args: good(1, -1, "Temporary", "Report"), frame: boot},
			{args: good(1, 0, "Temporary", "Report")}, // not in boot
			{args: args(hInt(1), hDur(0), restart("Temporary"), undeclared, giveUp("Report")), frame: boot},
		},
		"supervisors.Supervisor.flush_bounded": {
			{args: args(idle, wait("Forever", nil))},
			{args: args(idle, wait("UpTo", hDur(int64(time.Millisecond))))},
			{args: args(busy, wait("UpTo", hDur(int64(time.Millisecond))))},
			{args: args(busy, wait("UpTo", hDur(0)))},
		},
	}
}

func TestRtFuncs_AdaptersReproduceTheRecordedHosts(t *testing.T) {
	p := newRtFuncsSuite(t)
	all := map[string][]hostCase{}
	for _, part := range []map[string][]hostCase{p.scalarCases(), p.jsonCases(), p.concurrencyCases()} {
		for k, v := range part {
			all[k] = v
		}
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, c := range all[k] {
			p.check(k, c)
		}
	}
	if os.Getenv("NOMI_UPDATE_HOST_GOLDEN") != "" {
		if err := os.WriteFile(hostGoldenPath, []byte(strings.Join(p.lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.lines) != len(p.golden) {
		t.Errorf("%d argument sets ran and %s records %d", len(p.lines), hostGoldenPath, len(p.golden))
	}

	// The rows that joined the table after the hand-written hosts were deleted
	// have no recorded outcomes. AssertionFailure.format is held to
	// rt.FormatNomiAssertionFailure over the machine's own values (internal/vm's
	// TestHostCall_AssertionFailureFormatReadsTheMachinesValues); the rest to
	// testdata/builtins.golden (builtins_parity_test.go).
	elsewhere := map[string]bool{"assertions.AssertionFailure.format": true}
	for k := range builtinCheckedRows {
		elsewhere[k] = true
	}
	for k := range contextCheckedRows {
		elsewhere[k] = true
	}
	var missing []string
	for _, b := range stdlibbindings.RtFuncs() {
		if p.covered[b.Name] == 0 && !elsewhere[b.Name] {
			missing = append(missing, b.Name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d binding(s) have no case: %s", len(missing), strings.Join(missing, ", "))
	}
	t.Logf("%d of %d RtFuncs rows covered here, %d argument sets, %d adapter calls: %d recorded values, "+
		"%d recorded fault texts, %d recorded panics, %d recorded refusals; %d record operands read by offset",
		len(p.covered), len(stdlibbindings.RtFuncs()), len(p.lines), p.calls, p.values, p.faults, p.panics, p.refusals, p.fastRecords)
	if p.faults == 0 || p.panics == 0 || p.refusals == 0 || p.fastRecords == 0 {
		t.Errorf("a population is empty (faults %d, panics %d, refusals %d, by-offset records %d); "+
			"the comparison has stopped reaching it", p.faults, p.panics, p.refusals, p.fastRecords)
	}
}

const hostGoldenPath = "testdata/rtfuncs.golden"

// funcsOutcome is a Funcs row's outcome. Those adapters recover a failure
// into their error, whose text is part of the outcome.
func funcsOutcome(o hostOutcome, shapeOnly bool) string {
	if o.err != nil {
		return "error " + o.err.Error()
	}
	return goldenOutcome(o, shapeOnly)
}

func goldenOutcome(o hostOutcome, shapeOnly bool) string {
	switch {
	case o.fault != "":
		return "fault " + o.fault
	case o.panic != "":
		return "panic " + o.panic
	case o.err != nil:
		return "refusal"
	}
	return "value " + canonText(o.val, shapeOnly)
}

// canonText is v's canonical text. shapeOnly prints the types of scalars
// without their values, for a result that depends on the clock.
func canonText(v rt.Value, shapeOnly bool) string {
	scalar := func(kind, text string) string {
		if shapeOnly {
			return kind
		}
		return kind + "(" + text + ")"
	}
	switch x := v.(type) {
	case int64:
		return scalar("Int", fmt.Sprint(x))
	case float64:
		if math.IsNaN(x) {
			return scalar("Float", "NaN")
		}
		return scalar("Float", fmt.Sprintf("%#x", math.Float64bits(x)))
	case bool:
		return scalar("Bool", fmt.Sprint(x))
	case string:
		return scalar("String", fmt.Sprintf("%q", x))
	case rt.Byte:
		return scalar("Byte", fmt.Sprint(uint8(x)))
	case rt.Bytes:
		return scalar("Bytes", fmt.Sprintf("%q", string(x)))
	case rt.Decimal:
		return scalar("Decimal", rt.DecimalToString(x))
	case rt.Unit:
		return "Unit"
	case *rt.Record:
		var b strings.Builder
		fmt.Fprintf(&b, "%s/%d", x.Desc.Name, x.Desc.Kind)
		if x.Desc.Kind == rt.KindEnum {
			b.WriteString("." + x.Variant().Name)
		}
		l := x.Layout()
		var parts []string
		for k, f := range l.Fields {
			if f.Name == "" {
				parts = append(parts, fmt.Sprintf("%d=%s", k, canonText(x.Field(k), shapeOnly)))
				continue
			}
			fv, _ := x.FieldNamed(f.Name)
			parts = append(parts, f.Name+"="+canonText(fv, shapeOnly))
		}
		sort.Strings(parts)
		b.WriteString("{" + strings.Join(parts, ", ") + "}")
		return b.String()
	case *rt.List[any]:
		var parts []string
		for n := x; n != nil; n = n.Tail {
			parts = append(parts, canonText(n.Head, shapeOnly))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case rt.Map[any, any]:
		var parts []string
		for _, e := range rt.MapEntries(x) {
			parts = append(parts, canonText(e.Key, shapeOnly)+"=>"+canonText(e.Val, shapeOnly))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case rt.Context:
		left := rt.ContextDeadlineRemaining(x)
		if left.Tag != rt.TagSome {
			return "Context(no deadline)"
		}
		return fmt.Sprintf("Context(deadline in %ds)", int64(time.Duration(left.Some).Round(time.Second)/time.Second))
	case rt.Dynamic:
		// %#v spells each Go shape, so an int64 and a float64 differ, and it
		// prints a map's keys sorted.
		return fmt.Sprintf("Dynamic(%#v)", x.Inner)
	case rt.Supervisor:
		return "Supervisor"
	case rt.HostHandle:
		// The handle's Go type, not its contents: a fresh handle is a new
		// pointer.
		return fmt.Sprintf("Handle(%s %T)", x.TypeName, x.Value)
	}
	return fmt.Sprintf("%T", v)
}
