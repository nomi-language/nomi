package stdlibadapters

// THE BUILTIN SUITE: the RtFuncs rows that joined the table after the VM's
// hand-written hosts were deleted, each against recorded outcomes for the same
// key (testdata/builtins.golden, 373 argument sets). Those rows had no
// hand-written host to record, and the marshaller cannot carry their types.
//
// An error is recorded as an rt trap. The adapters
// for all 373 agreed with it when it was taken. A temporary directory's path
// reads `<dir>` in both the argument text and the outcome.
//
// An outcome is the RtFuncs suite's canonical text (goldenOutcome): a value by
// dynamic type, qualified record name, variant and fields, a trap by its text.
// Regenerating the recording is not a way to make this pass: the reference it
// was taken from no longer exists. NOMI_UPDATE_HOST_GOLDEN rewrites it from
// the adapters, for a deliberate change of behaviour.
//
// The Context rows are held to rt's own functions over rt values in
// TestRtFuncs_ContextRowsConvertRtsContext.

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/rt"
)

const builtinGoldenPath = "testdata/builtins.golden"

// builtinCheckedRows are the RtFuncs rows this suite covers.
var builtinCheckedRows = map[string]bool{
	"decimal.Decimal.from_int": true, "decimal.Decimal.from_string": true, "decimal.Decimal.to_int": true,
	"decimal.Decimal.to_float": true, "decimal.Decimal.from_float": true, "decimal.Decimal.divide": true,
	"decimal.Decimal.round": true, "decimal.Decimal.normalize": true, "decimal.Decimal.scale": true,
	"strings.String.repeat": true, "int.Int.shift_left": true, "int.Int.shift_right": true,
	"bytes.Bytes.to_list": true, "bytes.Bytes.from_list": true, "bytes.Bytes.hash": true,
	"strings.String.normalize": true, "io.read_file": true, "io.write_file": true,
	"dynamic.Dynamic.field": true, "dynamic.Dynamic.index": true, "dynamic.Dynamic.path": true,
	"dynamic.Dynamic.as_string": true, "dynamic.Dynamic.as_int": true, "dynamic.Dynamic.as_float": true,
	"dynamic.Dynamic.as_bool": true, "dynamic.Dynamic.as_list": true, "dynamic.Dynamic.as_dict": true,
	"dynamic.Dynamic.null?": true, "dynamic.Dynamic.has?": true, "dynamic.Dynamic.inspect": true,
	"json.Json.to_dynamic": true,
}

// contextCheckedRows are held to rt directly.
var contextCheckedRows = map[string]bool{
	"context.Context.deadline": true, "context.Context.deadline_remaining": true, "context.Context.with_deadline": true,
	// Reads the frame's input: TestRtFuncs_ReadLineReadsTheFramesInput.
	"io.read_line": true,
}

// TestRtFuncs_ReadLineReadsTheFramesInput: `io.read_line` answers each line of
// the input the frame's context carries, then Err("eof"), and Err("eof") at
// once over a frame with no input.
func TestRtFuncs_ReadLineReadsTheFramesInput(t *testing.T) {
	table, err := Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	fr := rt.NewFrame(rt.WithInput(context.Background(), rt.NewInput(strings.NewReader("one\ntwo"))))
	for _, want := range []string{
		"results.Result/4.Ok{0=String(\"one\")}",
		"results.Result/4.Ok{0=String(\"two\")}",
		"results.Result/4.Err{0=String(\"eof\")}",
	} {
		got, err := table["io.read_line"](fr, nil)
		if err != nil || canonText(got, false) != want {
			t.Errorf("read_line answered %s, %v; want %s", canonText(got, false), err, want)
		}
	}
	got, err := table["io.read_line"](rt.NewFrame(context.Background()), nil)
	if err != nil || canonText(got, false) != "results.Result/4.Err{0=String(\"eof\")}" {
		t.Errorf("read_line with no input answered %s, %v", canonText(got, false), err)
	}
}

// readOutcomes reads a recording: one `"<key>"\t"<outcome>"` line per
// argument set, each half Go-quoted.
func readOutcomes(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		k, v, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("%s: malformed line %q", path, line)
		}
		key, err1 := strconv.Unquote(k)
		outcome, err2 := strconv.Unquote(v)
		if err1 != nil || err2 != nil {
			t.Fatalf("%s: malformed line %q", path, line)
		}
		if _, dup := out[key]; dup {
			t.Fatalf("%s: %s is recorded twice", path, key)
		}
		out[key] = outcome
	}
	return out
}

// updateOutcomes rewrites a recording from the lines a run produced, when
// NOMI_UPDATE_HOST_GOLDEN is set.
func updateOutcomes(t *testing.T, path string, lines []string) {
	t.Helper()
	if os.Getenv("NOMI_UPDATE_HOST_GOLDEN") == "" {
		return
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dec(t *testing.T, text string) any {
	d, err := rt.ParseDecimal(text)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mode(name string) any { return rb.Variant("decimal.RoundingMode", name) }

// TestRtFuncs_NewRowsReproduceTheirRecordedOutcomes holds each of these rows'
// adapters to its outcomes in testdata/builtins.golden.
func TestRtFuncs_NewRowsReproduceTheirRecordedOutcomes(t *testing.T) {
	rb = newRecordBuilder()
	table, err := Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	golden := readOutcomes(t, builtinGoldenPath)
	dir := t.TempDir()
	existing := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(existing, []byte("read me"), 0o644); err != nil {
		t.Fatal(err)
	}
	iv := func(n int64) any { return n }
	fv := func(x float64) any { return x }
	sv := func(s string) any { return s }
	decimals := []any{dec(t, "1.50"), dec(t, "-0.001"), dec(t, "42"), dec(t, "123456789012345678901234.5"), dec(t, "0")}
	modes := []string{"HalfEven", "HalfUp", "Down", "Floor", "Ceiling", "Up", "Unnecessary"}
	cases := map[string][][]any{
		"decimal.Decimal.from_int":    {{iv(0)}, {iv(-5)}, {iv(math.MaxInt64)}, {iv(math.MinInt64)}},
		"decimal.Decimal.from_string": {{sv("1.50")}, {sv("abc")}, {sv("-0.001")}, {sv("")}, {sv("1e5")}, {sv(" 2")}},
		"decimal.Decimal.to_float":    {},
		"decimal.Decimal.to_int":      {},
		"decimal.Decimal.normalize":   {},
		"decimal.Decimal.scale":       {},
		"decimal.Decimal.from_float":  {},
		"decimal.Decimal.divide":      {},
		"decimal.Decimal.round":       {},
		"strings.String.repeat":       {{sv("ab"), iv(3)}, {sv("x"), iv(0)}, {sv("é"), iv(2)}, {sv("x"), iv(-1)}},
		"int.Int.shift_left":          {{iv(1), iv(3)}, {iv(1), iv(63)}, {iv(1), iv(64)}, {iv(-8), iv(1)}, {iv(1), iv(-1)}},
		"int.Int.shift_right":         {{iv(8), iv(3)}, {iv(-8), iv(1)}, {iv(1), iv(64)}, {iv(1), iv(-1)}},
		"bytes.Bytes.to_list":         {{rt.Bytes("")}, {rt.Bytes("ab\xff")}},
		"bytes.Bytes.from_list":       {{rtList()}, {rtList(rt.Byte(1), rt.Byte(255))}},
		"bytes.Bytes.hash":            {{rt.Bytes("")}, {rt.Bytes("ab")}, {rt.Bytes("ab\xff")}},
		"strings.String.normalize":    {},
		"io.read_file":                {{sv(existing)}, {sv(filepath.Join(dir, "missing.txt"))}, {sv(dir)}},
		"io.write_file":               {{sv(filepath.Join(dir, "out.txt")), sv("written")}, {sv(filepath.Join(dir, "no", "such", "dir.txt")), sv("x")}},
	}
	// A Json tree is the one the json.Json.decode adapter builds (held to the
	// recorded hosts' outcomes in the RtFuncs suite), rebuilt as another
	// producer lays it out.
	jsonOf := func(src string) any {
		r, err := table["json.Json.decode"](rt.NewFrame(context.Background()), []rt.Value{src})
		if err != nil {
			t.Fatal(err)
		}
		res := r.(*rt.Record)
		if res.Variant().Name != "Ok" {
			t.Fatalf("Json.decode(%s) answered %s", src, rt.RowText(r))
		}
		return rb.Restyle(res.Field(0))
	}
	dyn := func(inner any) any { return rt.Dynamic{Inner: inner} }
	obj := dyn(map[string]any{"name": "ada", "age": int64(36), "pi": 3.5, "on": true, "none": nil,
		"tags": []any{"x", int64(2)}, "inner": map[string]any{"n": int64(7)}})
	dyns := []any{obj, dyn("s"), dyn(int64(-4)), dyn(2.0), dyn(2.5), dyn(false), dyn(nil),
		dyn([]any{"a", int64(1), nil}), dyn(map[string]any{})}
	for _, d := range dyns {
		for _, k := range []string{"as_string", "as_int", "as_float", "as_bool", "as_list", "as_dict", "null?", "inspect"} {
			cases["dynamic.Dynamic."+k] = append(cases["dynamic.Dynamic."+k], []any{d})
		}
		for _, name := range []string{"name", "missing", "none"} {
			cases["dynamic.Dynamic.field"] = append(cases["dynamic.Dynamic.field"], []any{d, sv(name)})
			cases["dynamic.Dynamic.has?"] = append(cases["dynamic.Dynamic.has?"], []any{d, sv(name)})
		}
		for _, i := range []int64{0, 2, 5, -1} {
			cases["dynamic.Dynamic.index"] = append(cases["dynamic.Dynamic.index"], []any{d, iv(i)})
		}
		for _, dotted := range []string{"inner.n", "name", "inner.missing", "", "tags.0"} {
			cases["dynamic.Dynamic.path"] = append(cases["dynamic.Dynamic.path"], []any{d, sv(dotted)})
		}
	}
	for _, src := range []string{`{"a": [1, 2.5, "x", true, null], "b": {}}`, `null`, `"s"`, `-3`, `[]`} {
		cases["json.Json.to_dynamic"] = append(cases["json.Json.to_dynamic"], []any{jsonOf(src)})
	}
	for _, text := range []string{"e\u0301", "\u00e9", "\ufb01", "plain"} {
		for _, form := range []string{"NFC", "NFD", "NFKC", "NFKD"} {
			cases["strings.String.normalize"] = append(cases["strings.String.normalize"],
				[]any{sv(text), rb.Variant("strings.NormalForm", form)})
		}
	}
	for _, d := range decimals {
		cases["decimal.Decimal.to_float"] = append(cases["decimal.Decimal.to_float"], []any{d})
		cases["decimal.Decimal.to_int"] = append(cases["decimal.Decimal.to_int"], []any{d})
		cases["decimal.Decimal.normalize"] = append(cases["decimal.Decimal.normalize"], []any{d})
		cases["decimal.Decimal.scale"] = append(cases["decimal.Decimal.scale"], []any{d})
		for _, m := range modes {
			cases["decimal.Decimal.round"] = append(cases["decimal.Decimal.round"], []any{d, iv(1), mode(m)})
			cases["decimal.Decimal.divide"] = append(cases["decimal.Decimal.divide"], []any{d, dec(t, "3"), iv(2), mode(m)})
		}
		cases["decimal.Decimal.divide"] = append(cases["decimal.Decimal.divide"], []any{d, dec(t, "0"), iv(2), mode("HalfEven")})
	}
	for _, x := range []float64{0.1, 3.14159, -2.5, math.NaN(), math.Inf(1), 1e300} {
		for _, m := range []string{"HalfEven", "Floor", "Unnecessary"} {
			cases["decimal.Decimal.from_float"] = append(cases["decimal.Decimal.from_float"], []any{fv(x), iv(2), mode(m)})
		}
	}

	keys := make([]string, 0, len(cases))
	for k := range cases {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	compared, faults := 0, 0
	var lines []string
	for _, key := range keys {
		fn := table[key]
		if fn == nil {
			t.Errorf("%s: no adapter", key)
			continue
		}
		for n, args := range cases[key] {
			label := strings.ReplaceAll(fmt.Sprintf("%s#%d%s", key, n, hostArgText(args)), dir, "<dir>")
			got := classify(func() (rt.Value, error) { return fn(rt.NewFrame(context.Background()), args) })
			text := strings.ReplaceAll(goldenOutcome(got, false), dir, "<dir>")
			lines = append(lines, strconv.Quote(label)+"\t"+strconv.Quote(text))
			want, recorded := golden[label]
			switch {
			case !recorded:
				t.Errorf("%s: no recorded outcome", label)
				continue
			case text != want:
				t.Errorf("%s:\n  recorded %s\n  adapter  %s", label, want, text)
				continue
			}
			compared++
			if got.fault != "" {
				faults++
			}
		}
	}
	updateOutcomes(t, builtinGoldenPath, lines)
	if len(lines) != len(golden) {
		t.Errorf("%d argument sets ran and %s records %d", len(lines), builtinGoldenPath, len(golden))
	}
	t.Logf("%d rows, %d argument sets agree with the recorded builtins, %d of them as the same trap", len(keys), compared, faults)
	if faults == 0 {
		t.Error("no case traps, so a trap's text is not being compared")
	}
	for key := range builtinCheckedRows {
		if len(cases[key]) == 0 {
			t.Errorf("%s has no case", key)
		}
	}
}

// TestRtFuncs_ContextRowsConvertRtsContext: the three Context rows pass the
// machine's rt.Context through and build Instant and Duration records as the
// VM reads them.
func TestRtFuncs_ContextRowsConvertRtsContext(t *testing.T) {
	table, err := Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	fr := rt.NewFrame(context.Background())
	root := rt.ContextRoot()
	if got, err := table["context.Context.deadline"](fr, []rt.Value{root}); err != nil || canonText(got, false) != "maybe.Maybe/4.None{}" {
		t.Errorf("deadline(root) answered %s, %v", canonText(got, false), err)
	}
	at := rt.InstantNow() + rt.Instant(time.Hour)
	instantDesc := rt.NewDistinctDesc("instant.Instant", hostadapt.Slot(rt.SlotInt))
	inst := instantDesc.New()
	inst.W[0] = rt.IntWord(int64(at))
	bounded, err := table["context.Context.with_deadline"](fr, []rt.Value{root, inst})
	if err != nil {
		t.Fatal(err)
	}
	c, ok := bounded.(rt.Context)
	if !ok || rt.ContextDeadline(c).Some != at {
		t.Fatalf("with_deadline answered %T with deadline %v, want %d", bounded, rt.ContextDeadline(c), at)
	}
	if got, err := table["context.Context.deadline"](fr, []rt.Value{c}); err != nil ||
		canonText(got, false) != fmt.Sprintf("maybe.Maybe/4.Some{0=instant.Instant/5{0=Int(%d)}}", int64(at)) {
		t.Errorf("deadline(bounded) answered %s, %v", canonText(got, false), err)
	}
	left, err := table["context.Context.deadline_remaining"](fr, []rt.Value{c})
	text := canonText(left, true)
	if err != nil || !strings.HasPrefix(text, "maybe.Maybe/4.Some{0=duration.Duration/5{") {
		t.Errorf("deadline_remaining(bounded) answered %s, %v", text, err)
	}
}
