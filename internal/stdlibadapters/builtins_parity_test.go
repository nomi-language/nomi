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
	"runtime"
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
	"int.Int.to_decimal": true, "strings.String.to_decimal": true, "decimal.Decimal.to_int": true,
	"decimal.Decimal.to_float": true, "float.Float.to_decimal": true, "decimal.Decimal.divide": true,
	"decimal.Decimal.divide_exact": true,
	"decimal.Decimal.round":        true, "decimal.Decimal.normalize": true, "decimal.Decimal.scale": true,
	"strings.String.repeat": true, "int.Int.shift_left": true, "int.Int.shift_right": true,
	"bytes.Bytes.to_list": true, "bytes.Bytes.from_list": true, "bytes.Bytes.hash": true,
	"strings.String.normalize": true, "strings.String.to_literal": true,
	"fs_host.read": true, "fs_host.read_text": true, "fs_host.write": true, "fs_host.list": true,
	"fs_host.create_dir": true, "fs_host.remove": true, "fs_host.remove_all": true, "fs_host.rename": true,
	"fs_host.copy": true, "fs_host.metadata": true, "fs_host.link_metadata": true, "fs_host.create_symlink": true,
	"fs_host.read_link": true, "fs_host.set_mode": true,
	"dynamic.Dynamic.field": true, "dynamic.Dynamic.index": true, "dynamic.Dynamic.path": true,
	"dynamic.Dynamic.as_string": true, "dynamic.Dynamic.as_int": true, "dynamic.Dynamic.as_float": true,
	"dynamic.Dynamic.as_bool": true, "dynamic.Dynamic.as_list": true, "dynamic.Dynamic.as_dict": true,
	"dynamic.Dynamic.is_null": true, "dynamic.Dynamic.has": true, "dynamic.Dynamic.inspect": true,
	"json.Json.to_dynamic": true,
	"int.Int.pow":          true, "int.Int.sqrt": true, "int.Int.gcd": true, "int.Int.lcm": true,
	"int.int_to_digits": true, "int.int_from_digits": true,
	"float.float_round": true, "float.Float.pow": true, "float.Float.sqrt": true, "float.Float.exp": true,
	"float.Float.log": true, "float.Float.log2": true, "float.Float.log10": true, "float.Float.sin": true,
	"float.Float.cos": true, "float.Float.tan": true, "float.Float.atan2": true,
	"float.require_tolerance": true,
	"decimal.Decimal.pow":     true, "decimal.Decimal.sqrt": true,
}

// contextCheckedRows are held to rt directly.
var contextCheckedRows = map[string]bool{
	"context.Context.deadline": true, "context.Context.deadline_remaining": true, "context.Context.with_deadline": true,
	// Reads the frame's input: TestRtFuncs_ReadLineReadsTheFramesInput.
	"io.read_line": true,
	// The standard streams: TestRtFuncs_StreamsReachTheFramesStreams.
	"io.standard_input": true, "io.standard_output": true, "io.standard_error": true,
	"io.Reader.read_line": true, "io.Writer.write": true, "io.Writer.write_text": true,
	// Streams over handles and the sandbox: TestRtFuncs_HandlesAndSandboxes.
	"io.read_up_to": true, "io.Reader.read_all": true, "io.Reader.read_all_text": true,
	"io.Reader.copy_to": true, "io.Reader.close": true, "io.Reader.from_bytes": true, "io.Reader.from_text": true,
	"io.Writer.flush": true, "io.Writer.close": true,
	// Calls back into Nomi on each read's frame: rt's
	// TestPullReaders_* and vmhost's TestReaderSources_*.
	"io.Reader.from_fn": true,
	"fs_host.open":      true, "fs_host.create": true, "fs_host.discard": true, "fs_host.temp_dir": true,
	"fs_host.make_temp_dir": true, "fs_host.current_dir": true, "fs_host.new_sandbox": true,
	"fs_host.sandbox_fail": true, "fs_host.sandbox_files": true, "fs_host.sandbox_log": true, "fs_host.sandbox_open": true,
	// A File on the disk and in a sandbox, and fs.within's crossings:
	// TestRtFuncs_FilesAndWithin.
	"fs_host.open_file": true, "fs_host.within": true, "fs_host.release": true, "io.file_read": true,
	"io.File.read_at": true, "io.File.write": true, "io.File.write_at": true, "io.File.seek": true,
	"io.File.position": true, "io.File.size": true, "io.File.truncate": true, "io.File.close": true,
}

// TestRtFuncs_ReadLineReadsTheFramesInput: `io.read_line` answers each line of
// the input the frame's context carries, then Ok(None), and Ok(None) at
// once over a frame with no input.
func TestRtFuncs_ReadLineReadsTheFramesInput(t *testing.T) {
	table, err := Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	fr := rt.NewFrame(rt.WithInput(context.Background(), rt.NewInput(strings.NewReader("one\ntwo"))))
	for _, want := range []string{
		"results.Result/4.Ok{0=maybe.Maybe/4.Some{0=String(\"one\")}}",
		"results.Result/4.Ok{0=maybe.Maybe/4.Some{0=String(\"two\")}}",
		"results.Result/4.Ok{0=maybe.Maybe/4.None{}}",
	} {
		got, err := table["io.read_line"](fr, nil)
		if err != nil || canonText(got, false) != want {
			t.Errorf("read_line answered %s, %v; want %s", canonText(got, false), err, want)
		}
	}
	got, err := table["io.read_line"](rt.NewFrame(context.Background()), nil)
	if err != nil || canonText(got, false) != "results.Result/4.Ok{0=maybe.Maybe/4.None{}}" {
		t.Errorf("read_line with no input answered %s, %v", canonText(got, false), err)
	}
}

// TestRtFuncs_StreamsReachTheFramesStreams: the crossings behind `io.stdin`,
// `io.stdout` and `io.stderr` answer the three handles, `Reader.read_line` on stdin reads
// what `io.read_line` reads, and `Writer.write` and std/io's `write_text`
// write to the output and error writers the frame's context carries.
func TestRtFuncs_StreamsReachTheFramesStreams(t *testing.T) {
	table, err := Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	var out, errs strings.Builder
	ctx := rt.WithStreams(rt.WithInput(context.Background(), rt.NewInput(strings.NewReader("line\n"))), &out, &errs)
	fr := rt.NewFrame(ctx)
	handle := func(key string) any {
		v, err := table[key](fr, nil)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return v
	}
	stdin, stdout, stderr := handle("io.standard_input"), handle("io.standard_output"), handle("io.standard_error")
	got, err := table["io.Reader.read_line"](fr, []any{stdin})
	if want := "results.Result/4.Ok{0=maybe.Maybe/4.Some{0=String(\"line\")}}"; err != nil || canonText(got, false) != want {
		t.Errorf("Reader.read_line answered %s, %v; want %s", canonText(got, false), err, want)
	}
	for _, call := range []struct {
		key  string
		args []any
	}{
		{"io.Writer.write", []any{stdout, rt.Bytes("a")}},
		{"io.Writer.write_text", []any{stdout, "b\n"}},
		{"io.Writer.write", []any{stderr, rt.Bytes("c")}},
		{"io.Writer.write_text", []any{stderr, "d\n"}},
	} {
		got, err := table[call.key](fr, call.args)
		if want := "results.Result/4.Ok{0=Unit}"; err != nil || canonText(got, false) != want {
			t.Errorf("%s answered %s, %v; want %s", call.key, canonText(got, false), err, want)
		}
	}
	if out.String() != "ab\n" || errs.String() != "cd\n" {
		t.Errorf("stdout %q, stderr %q; want \"ab\\n\" and \"cd\\n\"", out.String(), errs.String())
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
	binary := filepath.Join(dir, "binary.dat")
	if err := os.WriteFile(binary, []byte("ok\xff"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("read me"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The files std/fs's rows read, list, move and remove. Each case
	// works on paths of its own, since the cases run in key order.
	for name, data := range map[string]string{"sub/a.txt": "a", "sub/b.txt": "bb", "full/x": "x", "ren-a.txt": "r", "dated.txt": "1234567", "mode.txt": "m"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dated := filepath.Join(dir, "dated.txt")
	if err := os.Chmod(dated, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dated, time.Unix(1700000000, 0), time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}
	in := func(name string) string { return filepath.Join(dir, name) }
	iv := func(n int64) any { return n }
	fv := func(x float64) any { return x }
	sv := func(s string) any { return s }
	decimals := []any{dec(t, "1.50"), dec(t, "-0.001"), dec(t, "42"), dec(t, "123456789012345678901234.5"), dec(t, "0")}
	modes := []string{"HalfEven", "HalfUp", "Down", "Floor", "Ceiling", "Up", "Unnecessary"}
	cases := map[string][][]any{
		"int.Int.to_decimal":           {{iv(0)}, {iv(-5)}, {iv(math.MaxInt64)}, {iv(math.MinInt64)}},
		"strings.String.to_decimal":    {{sv("1.50")}, {sv("abc")}, {sv("-0.001")}, {sv("")}, {sv("1e5")}, {sv(" 2")}},
		"decimal.Decimal.to_float":     {},
		"decimal.Decimal.to_int":       {},
		"decimal.Decimal.normalize":    {},
		"decimal.Decimal.scale":        {},
		"float.Float.to_decimal":       {},
		"decimal.Decimal.divide":       {},
		"decimal.Decimal.divide_exact": {},
		"decimal.Decimal.round":        {},
		"strings.String.repeat":        {{sv("ab"), iv(3)}, {sv("x"), iv(0)}, {sv("é"), iv(2)}, {sv("x"), iv(-1)}},
		"int.Int.shift_left":           {{iv(1), iv(3)}, {iv(1), iv(63)}, {iv(1), iv(64)}, {iv(-8), iv(1)}, {iv(1), iv(-1)}},
		"int.Int.shift_right":          {{iv(8), iv(3)}, {iv(-8), iv(1)}, {iv(1), iv(64)}, {iv(1), iv(-1)}},
		"bytes.Bytes.to_list":          {{rt.Bytes("")}, {rt.Bytes("ab\xff")}},
		"bytes.Bytes.from_list":        {{rtList()}, {rtList(rt.Byte(1), rt.Byte(255))}},
		"bytes.Bytes.hash":             {{rt.Bytes("")}, {rt.Bytes("ab")}, {rt.Bytes("ab\xff")}},
		"strings.String.normalize":     {},
		"strings.String.to_literal":    {{sv("")}, {sv("plain é 🙂")}, {sv("a\tb\nc\rd")}, {sv(`q"b\s`)}, {sv("$5 ${x} \\${y}")}, {sv("\x00\x1f\x7f")}},
		"fs_host.read":                 {{sv(existing)}, {sv(binary)}, {sv(in("missing.txt"))}, {sv(dir)}, {sv("")}, {sv("a\x00b")}},
		"fs_host.read_text":            {{sv(existing)}, {sv(in("missing.txt"))}, {sv(dir)}, {sv(binary)}, {sv("")}, {sv("a\x00b")}},
		"fs_host.write": {{sv(in("out.txt")), rt.Bytes("written"), iv(0)}, {sv(in("no/such/dir.txt")), rt.Bytes("x"), iv(0)},
			{sv(""), rt.Bytes("x"), iv(0)}, {sv(dir), rt.Bytes("x"), iv(0)}, {sv(existing), rt.Bytes("x"), iv(3)},
			{sv(in("log.txt")), rt.Bytes("a"), iv(2)}, {sv(in("log.txt")), rt.Bytes("b"), iv(2)}, {sv(in("atomic.txt")), rt.Bytes("new"), iv(1)}},
		"fs_host.list":       {{sv(in("sub"))}, {sv(in("none"))}, {sv(existing)}},
		"fs_host.create_dir": {{sv(in("made")), false}, {sv(in("made")), false}, {sv(in("deep/er")), true}, {sv(in("x/y")), false}},
		"fs_host.remove":     {{sv(in("full"))}, {sv(in("none"))}, {sv(in("sub/b.txt"))}},
		"fs_host.remove_all": {{sv(in("full"))}, {sv(in("none"))}},
		"fs_host.rename":     {{sv(in("ren-a.txt")), sv(in("ren-b.txt"))}, {sv(in("none")), sv(in("x"))}, {sv(existing), sv(in("no/dir/x"))}},
		"fs_host.copy":       {{sv(existing), sv(in("copy.txt"))}, {sv(in("none")), sv(in("x"))}, {sv(dir), sv(in("x"))}},
		"fs_host.metadata":   {{sv(dated)}, {sv(in("none"))}},
		// Cases run in key order: create_symlink makes the link read_link
		// reads. A link's own modification time is not set here, so
		// link_metadata's Ok case is a file's.
		"fs_host.create_symlink": {{sv(in("link-a")), sv("in.txt")}, {sv(in("link-a")), sv("x")}, {sv(in("no/l")), sv("x")}, {sv(in("l")), sv("")}},
		"fs_host.link_metadata":  {{sv(dated)}, {sv(in("none"))}},
		"fs_host.read_link":      {{sv(in("link-a"))}, {sv(existing)}, {sv(in("none"))}},
		"fs_host.set_mode":       {{sv(in("mode.txt")), iv(0o600)}, {sv(in("mode.txt")), iv(0o1000)}, {sv(in("mode.txt")), iv(-1)}, {sv(in("none")), iv(0o600)}},
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
		for _, k := range []string{"as_string", "as_int", "as_float", "as_bool", "as_list", "as_dict", "is_null", "inspect"} {
			cases["dynamic.Dynamic."+k] = append(cases["dynamic.Dynamic."+k], []any{d})
		}
		for _, name := range []string{"name", "missing", "none"} {
			cases["dynamic.Dynamic.field"] = append(cases["dynamic.Dynamic.field"], []any{d, sv(name)})
			cases["dynamic.Dynamic.has"] = append(cases["dynamic.Dynamic.has"], []any{d, sv(name)})
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
		for _, by := range []string{"4", "8", "3", "0.25", "-2", "0"} {
			cases["decimal.Decimal.divide_exact"] = append(cases["decimal.Decimal.divide_exact"], []any{d, dec(t, by)})
		}
	}
	cases["decimal.Decimal.divide_exact"] = append(cases["decimal.Decimal.divide_exact"], []any{dec(t, "10.00"), dec(t, "4")})
	for _, x := range []float64{0.1, 3.14159, -2.5, math.NaN(), math.Inf(1), 1e300} {
		for _, m := range []string{"HalfEven", "Floor", "Unnecessary"} {
			cases["float.Float.to_decimal"] = append(cases["float.Float.to_decimal"], []any{fv(x), iv(2), mode(m)})
		}
	}
	// Int's arithmetic: the limits, the signs, zero and each trap.
	edges := []int64{0, 1, -1, 2, -3, 12, 18, math.MaxInt64, math.MinInt64}
	for _, a := range edges {
		cases["int.Int.sqrt"] = append(cases["int.Int.sqrt"], []any{iv(a)})
		for _, b := range edges {
			cases["int.Int.gcd"] = append(cases["int.Int.gcd"], []any{iv(a), iv(b)})
			cases["int.Int.lcm"] = append(cases["int.Int.lcm"], []any{iv(a), iv(b)})
		}
		for _, e := range []int64{-1, 0, 1, 2, 63, 64, math.MaxInt64} {
			cases["int.Int.pow"] = append(cases["int.Int.pow"], []any{iv(a), iv(e)})
		}
		for _, base := range []int64{1, 2, 10, 16, math.MaxInt64} {
			cases["int.int_to_digits"] = append(cases["int.int_to_digits"], []any{iv(a), iv(base)})
		}
	}
	cases["int.Int.sqrt"] = append(cases["int.Int.sqrt"], []any{iv(15)}, []any{iv(16)}, []any{iv(3037000499 * 3037000499)}, []any{iv(3037000499*3037000499 - 1)})
	for _, digits := range [][]int64{{}, {0}, {1, 2, 3}, {0, 0, 7}, {1, 10}, {-1}, {9, 2, 2, 3, 3, 7, 2, 0, 3, 6, 8, 5, 4, 7, 7, 5, 8, 0, 7}, {9, 2, 2, 3, 3, 7, 2, 0, 3, 6, 8, 5, 4, 7, 7, 5, 8, 0, 8}} {
		items := make([]rt.Value, len(digits))
		for i, d := range digits {
			items[i] = d
		}
		for _, base := range []int64{0, 2, 10, 16} {
			cases["int.int_from_digits"] = append(cases["int.int_from_digits"], []any{rtList(items...), iv(base)})
		}
	}
	// Float.round at scale 0 and HalfEven is the unary Float.round the
	// RtFuncs suite recorded over these values, so those outcomes hold.
	roundFloats := []float64{0, math.Copysign(0, -1), 1.5, -2.5, 2.5, 0.5, -0.4, 1e21, 1e-7, 0.1 + 0.2,
		math.Inf(1), math.Inf(-1), math.NaN(), 9.3e18, -9.3e18, 9.2e18, -9.223372036854775808e18}
	for _, x := range roundFloats {
		cases["float.float_round"] = append(cases["float.float_round"], []any{fv(x), iv(0), mode("HalfEven")})
	}
	for _, x := range []float64{2.675, -2.675, 1.25, 1234.5, 0.004, -0.004, 2.0, math.MaxFloat64, 5e-324, math.NaN(), math.Inf(-1)} {
		for _, scale := range []int64{-400, -2, 0, 2, 1100, math.MinInt64} {
			for _, m := range []string{"HalfUp", "HalfDown", "Floor", "Up", "Unnecessary"} {
				cases["float.float_round"] = append(cases["float.float_round"], []any{fv(x), iv(scale), mode(m)})
			}
		}
	}
	specials := []float64{0, math.Copysign(0, -1), 1, -1, 0.5, 2, -8, 1e308, math.Inf(1), math.Inf(-1), math.NaN()}
	for _, x := range specials {
		cases["float.require_tolerance"] = append(cases["float.require_tolerance"], []any{fv(x)})
	}
	for _, x := range specials {
		for _, k := range []string{"sqrt", "exp", "log", "log2", "log10", "sin", "cos", "tan"} {
			cases["float.Float."+k] = append(cases["float.Float."+k], []any{fv(x)})
		}
		for _, y := range specials {
			cases["float.Float.pow"] = append(cases["float.Float.pow"], []any{fv(x), fv(y)})
			cases["float.Float.atan2"] = append(cases["float.Float.atan2"], []any{fv(x), fv(y)})
		}
	}
	for _, d := range append(decimals, dec(t, "-1.5"), dec(t, "6.25"), dec(t, "0.0625"), dec(t, "2")) {
		for _, e := range []int64{-1, 0, 1, 3} {
			cases["decimal.Decimal.pow"] = append(cases["decimal.Decimal.pow"], []any{d, iv(e)})
		}
		for _, scale := range []int64{-1, 0, 1, 4} {
			for _, m := range []string{"HalfEven", "HalfUp", "HalfDown", "Down", "Up", "Unnecessary"} {
				cases["decimal.Decimal.sqrt"] = append(cases["decimal.Decimal.sqrt"], []any{d, iv(scale), mode(m)})
			}
		}
	}
	cases["decimal.Decimal.pow"] = append(cases["decimal.Decimal.pow"], []any{dec(t, "0.01"), iv(1 << 30)}, []any{dec(t, "2"), iv(1 << 40)}, []any{dec(t, "-1"), iv(1<<40 + 1)})

	keys := make([]string, 0, len(cases))
	for k := range cases {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	compared, faults, skipped := 0, 0, 0
	var lines []string
	for _, key := range keys {
		fn := table[key]
		if fn == nil {
			t.Errorf("%s: no adapter", key)
			continue
		}
		for n, args := range cases[key] {
			label := strings.ReplaceAll(fmt.Sprintf("%s#%d%s", key, n, hostArgText(args)), dir, "<dir>")
			// The cases on files were recorded on a Unix file system: its
			// modes, its error texts and paths that come back with dir's
			// spelling. Windows answers each differently.
			if runtime.GOOS == "windows" && strings.Contains(label, "<dir>") {
				skipped++
				continue
			}
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
	if skipped == 0 {
		updateOutcomes(t, builtinGoldenPath, lines)
	}
	if len(lines)+skipped != len(golden) {
		t.Errorf("%d argument sets ran, %d were skipped, and %s records %d", len(lines), skipped, builtinGoldenPath, len(golden))
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
